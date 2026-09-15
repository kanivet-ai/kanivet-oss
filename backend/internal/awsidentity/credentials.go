package awsidentity

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/kanivet/backend/internal/cache"
	"github.com/kanivet/backend/internal/cloud"
	"github.com/kanivet/backend/internal/db"
	"github.com/kanivet/backend/internal/k8s"
)

const (
	defaultRegion        = "us-east-1"
	nodeProviderCacheTTL = 10 * time.Minute
	clusterNameCacheTTL  = 10 * time.Minute
)

var eksClusterArnRe = regexp.MustCompile(`^arn:aws[a-z-]*:eks:([a-z0-9-]+):(\d{12}):cluster/(.+)$`)

type ExecHint struct {
	IsEKS       bool
	ClusterName string
	Region      string
	Profile     string
	AccountID   string
}

type ErrNoCredentialsForAccount struct {
	AccountID string
}

func (e *ErrNoCredentialsForAccount) Error() string {
	return fmt.Sprintf("no AWS profile available for account %s", e.AccountID)
}

func parseExecHint(exec *clientcmdapi.ExecConfig) ExecHint {
	var hint ExecHint
	if exec == nil {
		return hint
	}
	cmd := strings.ToLower(strings.TrimSuffix(filepath.Base(exec.Command), ".exe"))
	args := exec.Args

	switch cmd {
	case "aws":
		if !containsAll(args, "eks", "get-token") {
			return hint
		}
		hint.IsEKS = true
	case "aws-iam-authenticator":
		if !contains(args, "token") {
			return hint
		}
		hint.IsEKS = true
	default:
		return hint
	}

	flags := parseFlags(args)
	hint.ClusterName = firstNonEmpty(flags["--cluster-name"], flags["-i"], flags["--cluster-id"])
	hint.Region = flags["--region"]
	hint.Profile = flags["--profile"]

	for _, env := range exec.Env {
		switch env.Name {
		case "AWS_PROFILE":
			if hint.Profile == "" {
				hint.Profile = env.Value
			}
		case "AWS_REGION", "AWS_DEFAULT_REGION":
			if hint.Region == "" {
				hint.Region = env.Value
			}
		}
	}
	return hint
}

func parseFlags(args []string) map[string]string {
	flags := make(map[string]string)
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") {
			continue
		}
		if eq := strings.IndexByte(arg, '='); eq > 0 {
			flags[arg[:eq]] = arg[eq+1:]
			continue
		}
		if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			flags[arg] = args[i+1]
			i++
		}
	}
	return flags
}

func hintFromClusterName(cluster string) ExecHint {
	m := eksClusterArnRe.FindStringSubmatch(cluster)
	if m == nil {
		return ExecHint{}
	}
	return ExecHint{IsEKS: true, Region: m[1], AccountID: m[2], ClusterName: m[3]}
}

type Resolver struct {
	k8s   k8s.Interface
	cloud *cloud.Service
	db    *db.DB
	cache *cache.Cache
}

func NewResolver(k k8s.Interface, cloudSvc *cloud.Service, c *cache.Cache, database *db.DB) *Resolver {
	return &Resolver{k8s: k, cloud: cloudSvc, db: database, cache: c}
}

func (r *Resolver) Hint(cluster string) ExecHint {
	hint := hintFromClusterName(cluster)
	_, cfg, err := r.k8s.GetClientAndConfig(cluster)
	if err != nil || cfg == nil {
		return hint
	}
	execHint := parseExecHint(cfg.ExecProvider)
	if !execHint.IsEKS {
		return hint
	}
	execHint.ClusterName = firstNonEmpty(execHint.ClusterName, hint.ClusterName)
	execHint.Region = firstNonEmpty(execHint.Region, hint.Region)
	execHint.AccountID = hint.AccountID
	return execHint
}

func (r *Resolver) override(cluster string) *db.AWSIdentityCredentials {
	if r.db == nil {
		return nil
	}
	creds, err := r.db.GetAWSIdentityCredentials(cluster)
	if err != nil {
		return nil
	}
	return creds
}

func (r *Resolver) Status(ctx context.Context, cluster string) ClusterStatus {
	hint := r.Hint(cluster)
	override := r.override(cluster)
	if !hint.IsEKS && override == nil && !r.nodesRunOnAWS(ctx, cluster) {
		return ClusterStatus{
			Available:   false,
			Reason:      "Cluster does not run on AWS",
			Credentials: Credentials{Source: CredentialSourceNone},
		}
	}
	creds, _, err := r.Resolve(ctx, cluster)
	if err != nil && creds.Error == "" {
		creds.Error = err.Error()
	}
	return ClusterStatus{Available: true, Credentials: creds}
}

func (r *Resolver) nodesRunOnAWS(ctx context.Context, cluster string) bool {
	key := r.cache.BuildKey("awsidentity-nodes-aws", cluster)
	value, err := r.cache.GetOrSet(key, nodeProviderCacheTTL, func() (interface{}, error) {
		clientset, err := r.k8s.GetClientForCluster(cluster)
		if err != nil {
			return nil, err
		}
		nodes, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{Limit: 1})
		if err != nil {
			return nil, err
		}
		if len(nodes.Items) == 0 {
			return false, nil
		}
		return strings.HasPrefix(nodes.Items[0].Spec.ProviderID, "aws://"), nil
	})
	if err != nil {
		return false
	}
	onAWS, _ := value.(bool)
	return onAWS
}

// EKS clusters reached through non-exec auth (or a profile override) carry no
// cluster name in the kubeconfig, so match the API endpoint against EKS instead.
func (r *Resolver) discoverClusterName(ctx context.Context, cfg aws.Config, cluster, profile, region string) string {
	_, restCfg, err := r.k8s.GetClientAndConfig(cluster)
	if err != nil || restCfg == nil || restCfg.Host == "" {
		return ""
	}
	endpoint := strings.TrimSuffix(strings.ToLower(restCfg.Host), "/")
	key := r.cache.BuildKey("awsidentity-cluster-name", cluster, profile, region)
	value, err := r.cache.GetOrSet(key, clusterNameCacheTTL, func() (interface{}, error) {
		client := eks.NewFromConfig(cfg)
		paginator := eks.NewListClustersPaginator(client, &eks.ListClustersInput{})
		for paginator.HasMorePages() {
			page, err := paginator.NextPage(ctx)
			if err != nil {
				return "", err
			}
			for _, name := range page.Clusters {
				out, err := client.DescribeCluster(ctx, &eks.DescribeClusterInput{Name: aws.String(name)})
				if err != nil || out.Cluster == nil {
					continue
				}
				if strings.TrimSuffix(strings.ToLower(aws.ToString(out.Cluster.Endpoint)), "/") == endpoint {
					return name, nil
				}
			}
		}
		return "", nil
	})
	if err != nil {
		return ""
	}
	name, _ := value.(string)
	return name
}

func (r *Resolver) Resolve(ctx context.Context, cluster string) (Credentials, aws.Config, error) {
	hint := r.Hint(cluster)
	creds := Credentials{Source: CredentialSourceNone, EKSClusterName: hint.ClusterName}

	if override := r.override(cluster); override != nil {
		creds.Source = CredentialSourceOverride
		creds.Profile = override.Profile
		creds.Region = firstNonEmpty(override.Region, hint.Region)
	} else if hint.IsEKS {
		creds.Source = CredentialSourceExec
		creds.Profile = firstNonEmpty(hint.Profile, os.Getenv("AWS_PROFILE"), r.profileForAccount(hint.AccountID))
		creds.Region = hint.Region
	} else if active, err := r.cloud.GetSSOActiveAccount(); err == nil && active != nil && active.ProfileName != "" {
		creds.Source = CredentialSourceSSO
		creds.Profile = active.ProfileName
		creds.AccountID = active.AccountID
	} else {
		creds.Error = "No AWS credentials could be derived for this cluster"
		return creds, aws.Config{}, errors.New(creds.Error)
	}

	provider := r.cloud.AWSProvider()
	if creds.Region == "" {
		creds.Region = provider.GetProfileRegion(creds.Profile)
	}
	if creds.Region == "" {
		creds.Region = defaultRegion
	}

	var cfg aws.Config
	var err error
	if creds.Profile == "" {
		cfg, err = config.LoadDefaultConfig(ctx, config.WithRegion(creds.Region))
	} else {
		cfg, err = provider.GetConfig(ctx, creds.Profile, creds.Region)
	}
	if err != nil {
		creds.Error = err.Error()
		return creds, aws.Config{}, err
	}

	account, err := r.callerAccount(ctx, cfg, creds.Profile, creds.Region)
	if err != nil {
		if creds.Profile == "" {
			creds.Error = "The kubeconfig for this cluster does not name an AWS profile and no default credentials were found; choose a profile to use for IAM lookups"
		} else {
			creds.Error = describeCredentialError(err, creds.Profile)
		}
		return creds, cfg, err
	}
	creds.AccountID = account
	if creds.EKSClusterName == "" {
		creds.EKSClusterName = r.discoverClusterName(ctx, cfg, cluster, creds.Profile, creds.Region)
	}
	return creds, cfg, nil
}

func (r *Resolver) profileForAccount(accountID string) string {
	if accountID == "" {
		return ""
	}
	if active, err := r.cloud.GetSSOActiveAccount(); err == nil && active != nil && active.AccountID == accountID && active.ProfileName != "" {
		return active.ProfileName
	}
	profiles, err := r.cloud.AWSProvider().ListProfiles()
	if err != nil {
		return ""
	}
	for _, p := range profiles {
		if p.AccountID == accountID {
			return p.Name
		}
	}
	return ""
}

func (r *Resolver) callerAccount(ctx context.Context, cfg aws.Config, profile, region string) (string, error) {
	key := r.cache.BuildKey("awsidentity-account", profile, region)
	value, err := r.cache.GetOrSet(key, 5*time.Minute, func() (interface{}, error) {
		out, err := sts.NewFromConfig(cfg).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
		if err != nil {
			return nil, err
		}
		return aws.ToString(out.Account), nil
	})
	if err != nil {
		return "", err
	}
	return value.(string), nil
}

func (r *Resolver) ConfigForAccount(ctx context.Context, base aws.Config, baseAccount, targetAccount, region string) (aws.Config, string, error) {
	if targetAccount == "" || targetAccount == baseAccount {
		return base, "", nil
	}
	profiles, err := r.cloud.AWSProvider().ListProfiles()
	if err != nil {
		return aws.Config{}, "", &ErrNoCredentialsForAccount{AccountID: targetAccount}
	}
	for _, p := range profiles {
		if p.AccountID != targetAccount {
			continue
		}
		cfg, err := r.cloud.AWSProvider().GetConfig(ctx, p.Name, firstNonEmpty(region, p.Region, defaultRegion))
		if err != nil {
			continue
		}
		if account, err := r.callerAccount(ctx, cfg, p.Name, cfg.Region); err == nil && account == targetAccount {
			return cfg, p.Name, nil
		}
	}
	return aws.Config{}, "", &ErrNoCredentialsForAccount{AccountID: targetAccount}
}

func describeCredentialError(err error, profile string) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "token has expired") || strings.Contains(msg, "ExpiredToken") || strings.Contains(msg, "expired"):
		if profile != "" {
			return fmt.Sprintf("AWS credentials for profile %q have expired; log in again", profile)
		}
		return "AWS credentials have expired; log in again"
	case strings.Contains(msg, "no EC2 IMDS role found") || strings.Contains(msg, "failed to refresh cached credentials"):
		if profile != "" {
			return fmt.Sprintf("Could not load AWS credentials for profile %q", profile)
		}
		return "Could not load AWS credentials from the default credential chain"
	}
	return msg
}

func containsAll(list []string, values ...string) bool {
	for _, v := range values {
		if !contains(list, v) {
			return false
		}
	}
	return true
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
