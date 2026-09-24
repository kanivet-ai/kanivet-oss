package cloud

import (
	"context"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kanivet/backend/internal/db"
)

func KanivetKubeconfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".kube", "kanivet-clusters")
}

// AuthChangedFunc receives the provider whose sign-in state changed.
type AuthChangedFunc func(provider Provider)

type Service struct {
	aws             *AWSProvider
	gcp             *GCPProvider
	azure           *AzureProvider
	db              *db.DB
	mu              sync.RWMutex
	importJobs      map[string]*BatchImportJob
	importMu        sync.RWMutex
	onBatchComplete func()

	onAuthChanged      AuthChangedFunc
	kubeconfigResolver func(cluster string) string
	credentials        *credentialChecker

	summaryMu     sync.Mutex
	gcpSummary    *ProviderAuthSummary
	azureSummary  *ProviderAuthSummary
	summaryFlight map[Provider]chan struct{}

	lastSessionsJSON string
}

const providerSummaryTTL = 45 * time.Second

func NewService(database *db.DB) *Service {
	s := &Service{
		aws:           NewAWSProvider(),
		gcp:           NewGCPProvider(),
		azure:         NewAzureProvider(),
		db:            database,
		importJobs:    make(map[string]*BatchImportJob),
		summaryFlight: make(map[Provider]chan struct{}),
		credentials:   newCredentialChecker(awsIdentity),
	}
	s.aws.SetOnAuthChanged(func() { s.notifyAuthChanged(ProviderAWS) })
	s.gcp.SetOnAuthChanged(func() { s.invalidateProviderSummary(ProviderGCP); s.notifyAuthChanged(ProviderGCP) })
	s.azure.SetOnAuthChanged(func() { s.invalidateProviderSummary(ProviderAzure); s.notifyAuthChanged(ProviderAzure) })
	return s
}

func (s *Service) SetOnBatchComplete(fn func()) {
	s.onBatchComplete = fn
}

// SetOnAuthChanged registers the broadcaster that tells connected UIs to
// re-read /cloud/auth.
func (s *Service) SetOnAuthChanged(fn AuthChangedFunc) {
	s.mu.Lock()
	s.onAuthChanged = fn
	s.mu.Unlock()
}

// SetKubeconfigResolver wires the k8s client's context → kubeconfig lookup so
// DescribeClusterAuth can inspect any context, not just imported ones.
func (s *Service) SetKubeconfigResolver(fn func(cluster string) string) {
	s.kubeconfigResolver = fn
}

func (s *Service) notifyAuthChanged(provider Provider) {
	if provider == ProviderAWS {
		s.credentials.forget()
	}
	s.mu.RLock()
	fn := s.onAuthChanged
	s.mu.RUnlock()
	if fn != nil {
		fn(provider)
	}
}

// NotifyExternalConfigChange is called by the config watcher when ~/.aws or the
// SSO cache changes on disk (terminal `aws sso login`, Leapp rotating keys…).
func (s *Service) NotifyExternalConfigChange(path string) {
	if strings.Contains(path, string(filepath.Separator)+".aws") {
		s.aws.ResetConfigCache()
		s.notifyAuthChanged(ProviderAWS)
	}
}

func (s *Service) invalidateProviderSummary(provider Provider) {
	s.summaryMu.Lock()
	switch provider {
	case ProviderGCP:
		s.gcpSummary = nil
	case ProviderAzure:
		s.azureSummary = nil
	}
	s.summaryMu.Unlock()
}

// providerSummary returns a memoised gcloud/az status; both shell out, so a
// UI that polls must not trigger a CLI run every time.
func (s *Service) providerSummary(ctx context.Context, provider Provider, force bool) ProviderAuthSummary {
	s.summaryMu.Lock()
	var cached *ProviderAuthSummary
	switch provider {
	case ProviderGCP:
		cached = s.gcpSummary
	case ProviderAzure:
		cached = s.azureSummary
	}
	if cached != nil && !force && time.Since(time.UnixMilli(cached.CheckedAt)) < providerSummaryTTL {
		out := *cached
		s.summaryMu.Unlock()
		s.attachLogin(&out)
		return out
	}
	if flight, ok := s.summaryFlight[provider]; ok {
		s.summaryMu.Unlock()
		<-flight
		return s.providerSummary(ctx, provider, false)
	}
	flight := make(chan struct{})
	s.summaryFlight[provider] = flight
	s.summaryMu.Unlock()

	var result ProviderAuthSummary
	switch provider {
	case ProviderGCP:
		result = s.gcp.Status(ctx)
	case ProviderAzure:
		result = s.azure.Status(ctx)
	}

	s.summaryMu.Lock()
	switch provider {
	case ProviderGCP:
		s.gcpSummary = &result
	case ProviderAzure:
		s.azureSummary = &result
	}
	delete(s.summaryFlight, provider)
	close(flight)
	s.summaryMu.Unlock()
	return result
}

func (s *Service) attachLogin(summary *ProviderAuthSummary) {
	switch summary.Provider {
	case ProviderGCP:
		if job, ok := s.gcp.cli.running("gcp"); ok {
			summary.Login = job
		} else {
			summary.Login = nil
		}
	case ProviderAzure:
		if job, ok := s.azure.cli.running("azure"); ok {
			summary.Login = job
		} else {
			summary.Login = nil
		}
	}
}

// GetAuthSummary is the one call the UI needs to render every provider.
func (s *Service) GetAuthSummary(ctx context.Context, force bool) CloudAuthSummary {
	var summary CloudAuthSummary
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		summary.AWS = s.awsSummary()
	}()
	go func() {
		defer wg.Done()
		summary.GCP = s.providerSummary(ctx, ProviderGCP, force)
	}()
	go func() {
		defer wg.Done()
		summary.Azure = s.providerSummary(ctx, ProviderAzure, force)
	}()
	wg.Wait()
	summary.UpdatedAt = time.Now().UnixMilli()
	return summary
}

func (s *Service) awsSummary() AWSAuthSummary {
	out := AWSAuthSummary{Sessions: s.GetAWSSSOSessions()}
	_, out.CLIInstalled = lookPath("aws")
	if !out.CLIInstalled {
		out.CLIInstallHint = cliInstallHint("aws")
	}
	if profiles, err := s.aws.ListProfiles(); err == nil {
		out.ProfileCount = len(profiles)
	}
	s.aws.cli.mu.Lock()
	for _, job := range s.aws.cli.byID {
		if job.State == cliJobRunning {
			out.ProfileLogins = append(out.ProfileLogins, job.snapshot())
		}
	}
	s.aws.cli.mu.Unlock()
	return out
}

// GetAuthStatus keeps the old boolean shape for callers that only need
// "is anything signed in".
func (s *Service) GetAuthStatus(ctx context.Context) map[Provider]bool {
	summary := s.GetAuthSummary(ctx, false)
	awsOK := false
	for _, sess := range summary.AWS.Sessions {
		if sess.IsValid || sess.Refreshable {
			awsOK = true
			break
		}
	}
	return map[Provider]bool{
		ProviderAWS:   awsOK || summary.AWS.ProfileCount > 0,
		ProviderGCP:   summary.GCP.SignedIn,
		ProviderAzure: summary.Azure.SignedIn,
	}
}

func (s *Service) ListAWSProfiles() ([]AWSProfile, error) {
	return s.aws.ListProfiles()
}

func (s *Service) LoginAWSProfile(ctx context.Context, profile string) (*CLILoginJob, error) {
	return s.aws.LoginWithProfile(ctx, profile)
}

func (s *Service) BeginAWSSSOLogin(ctx context.Context, startURL, region string, openBrowser bool) (*SSOLoginSession, error) {
	return s.aws.BeginSSOLogin(ctx, startURL, region, openBrowser)
}

func (s *Service) GetAWSSSOLogin(id string) (*SSOLoginSession, bool) {
	return s.aws.GetSSOLogin(id)
}

func (s *Service) CancelAWSSSOLogin(id string) bool {
	return s.aws.CancelSSOLogin(id)
}

func (s *Service) RefreshAWSSSOSession(ctx context.Context, startURL string) (*SSOSessionStatus, error) {
	if _, err := s.aws.RefreshSSOSession(ctx, startURL); err != nil {
		return nil, err
	}
	for _, sess := range s.GetAWSSSOSessions() {
		if sess.StartURL == normalizeStartURL(startURL) {
			return &sess, nil
		}
	}
	return nil, nil
}

func (s *Service) SignOutAWSSSO(startURL string) error {
	return s.aws.SignOutSSO(startURL)
}

func (s *Service) GetAWSSSOAccountRoles(ctx context.Context, startURL, accountID string) ([]string, error) {
	return s.aws.GetSSORoles(ctx, startURL, accountID)
}

func (s *Service) GetAWSSSOAccounts(ctx context.Context, startURL string) ([]SSOAccount, error) {
	return s.aws.GetSSOAccounts(ctx, startURL)
}

// GetLoginJob looks a CLI login job up across providers.
func (s *Service) GetLoginJob(id string) (*CLILoginJob, bool) {
	for _, mgr := range []*cliLoginManager{s.aws.cli, s.gcp.cli, s.azure.cli} {
		if job, ok := mgr.get(id); ok {
			return job, true
		}
	}
	return nil, false
}

func (s *Service) CancelLoginJob(id string) bool {
	for _, mgr := range []*cliLoginManager{s.aws.cli, s.gcp.cli, s.azure.cli} {
		if _, ok := mgr.get(id); ok {
			return mgr.cancel(id)
		}
	}
	return false
}

func (s *Service) ListGCPProjects(ctx context.Context) ([]GCPProject, error) {
	return s.gcp.ListProjects(ctx)
}

func (s *Service) LoginGCP(ctx context.Context) (*CLILoginJob, error) {
	return s.gcp.Login(ctx)
}

func (s *Service) ListAzureSubscriptions(ctx context.Context) ([]AzureSubscription, error) {
	return s.azure.ListSubscriptions(ctx)
}

func (s *Service) LoginAzure(ctx context.Context) (*CLILoginJob, error) {
	return s.azure.Login(ctx)
}

// DescribeClusterAuth explains how a kubeconfig context authenticates.
func (s *Service) DescribeClusterAuth(ctx context.Context, cluster string) (*ClusterAuthInfo, error) {
	path := ""
	if s.kubeconfigResolver != nil {
		path = s.kubeconfigResolver(cluster)
	}
	if path == "" {
		if kubeconfig, err := clientcmdLoad(KanivetKubeconfigPath()); err == nil {
			if _, ok := kubeconfig.Contexts[cluster]; ok {
				path = KanivetKubeconfigPath()
			}
		}
	}
	return s.aws.describeBoundClusterAuth(cluster, path, s.clusterSSOBinding(cluster))
}

func (s *Service) DiscoverClusters(ctx context.Context, req DiscoverRequest) ([]DiscoveredCluster, error) {
	switch req.Provider {
	case ProviderAWS:
		var regions []string
		if req.Region != "" {
			regions = []string{req.Region}
		}
		if req.SSOStartURL != "" {
			return s.aws.DiscoverClustersWithSSO(ctx, req.SSOStartURL, regions, req.AccountIDs)
		}
		profile := req.Profile
		if profile == "" {
			profile = "default"
		}
		return s.aws.DiscoverClusters(ctx, profile, regions)
	case ProviderGCP:
		var locations []string
		if req.Region != "" {
			locations = []string{req.Region}
		}
		return s.gcp.DiscoverClusters(ctx, req.ProjectID, locations)
	case ProviderAzure:
		if req.Subscription == "" {
			return nil, fmt.Errorf("subscription is required for Azure discovery")
		}
		return s.azure.DiscoverClusters(ctx, req.Subscription)
	default:
		return nil, fmt.Errorf("unsupported provider: %s", req.Provider)
	}
}

func (s *Service) ImportCluster(ctx context.Context, req ImportRequest) error {
	switch req.Provider {
	case ProviderAWS:
		return s.aws.ImportCluster(ctx, req)
	case ProviderGCP:
		return s.gcp.ImportCluster(ctx, req)
	case ProviderAzure:
		return s.azure.ImportCluster(ctx, req)
	default:
		return fmt.Errorf("unsupported provider: %s", req.Provider)
	}
}

func (s *Service) StartBatchImport(ctx context.Context, req BatchImportRequest) string {
	jobID := uuid.New().String()
	job := &BatchImportJob{
		ID:         jobID,
		Total:      len(req.Clusters),
		InProgress: true,
		Results:    make([]BatchImportResult, len(req.Clusters)),
		StartedAt:  time.Now().UnixMilli(),
	}
	s.importMu.Lock()
	cutoff := time.Now().Add(-time.Hour).UnixMilli()
	for id, j := range s.importJobs {
		if !j.InProgress && j.StartedAt < cutoff {
			delete(s.importJobs, id)
		}
	}
	s.importJobs[jobID] = job
	s.importMu.Unlock()

	go func() {
		var wg sync.WaitGroup
		for i, cluster := range req.Clusters {
			wg.Add(1)
			go func(idx int, c ImportRequest) {
				defer wg.Done()
				result := BatchImportResult{ClusterID: c.ClusterID, Name: c.Name}
				if err := s.ImportCluster(context.Background(), c); err != nil {
					result.Success = false
					result.Error = err.Error()
					s.importMu.Lock()
					job.Failed++
					s.importMu.Unlock()
				} else {
					result.Success = true
					s.importMu.Lock()
					job.Successful++
					s.importMu.Unlock()
				}
				s.importMu.Lock()
				job.Results[idx] = result
				job.Completed++
				s.importMu.Unlock()
			}(i, cluster)
		}
		wg.Wait()
		if s.onBatchComplete != nil && job.Successful > 0 {
			s.onBatchComplete()
		}
		s.importMu.Lock()
		job.InProgress = false
		s.importMu.Unlock()
	}()

	return jobID
}

func (s *Service) GetBatchImportJob(jobID string) *BatchImportJob {
	s.importMu.RLock()
	defer s.importMu.RUnlock()
	return s.importJobs[jobID]
}

func (s *Service) GetActiveBatchImportJob() *BatchImportJob {
	s.importMu.RLock()
	defer s.importMu.RUnlock()
	for _, job := range s.importJobs {
		if job.InProgress {
			return job
		}
	}
	for _, job := range s.importJobs {
		if time.Now().UnixMilli()-job.StartedAt < 60000 {
			return job
		}
	}
	return nil
}

func (s *Service) GetAWSAccountID(ctx context.Context, profile string) (string, error) {
	return s.aws.GetAccountID(ctx, profile)
}

func (s *Service) GetGCPLocations(ctx context.Context, projectID string) ([]string, error) {
	return s.gcp.ListLocations(ctx, projectID)
}

func (s *Service) UseGCPServiceAccount(ctx context.Context, keyFilePath string) error {
	return s.gcp.UseServiceAccount(ctx, keyFilePath)
}

func defaultSessionLabel(startURL string, names []string) string {
	if len(names) > 0 {
		return names[0]
	}
	if parsed, err := url.Parse(startURL); err == nil && parsed.Hostname() != "" {
		host := parsed.Hostname()
		if strings.HasSuffix(host, ".awsapps.com") {
			return strings.TrimSuffix(host, ".awsapps.com")
		}
		return strings.Split(host, ".")[0]
	}
	return "AWS SSO"
}

// GetAWSSSOSessions merges the portals Kanivet was told about (DB rows carry
// user labels) with everything found in ~/.aws/config and the token cache.
func (s *Service) GetAWSSSOSessions() []SSOSessionStatus {
	var dbSessions []db.SSOSession
	if s.db != nil {
		if rows, err := s.db.GetSSOSessions(); err == nil {
			dbSessions = rows
		}
	}
	live := s.aws.GetSSOSessions()
	byURL := make(map[string]int, len(live))
	for i, sess := range live {
		byURL[normalizeStartURL(sess.StartURL)] = i
	}
	for _, row := range dbSessions {
		key := normalizeStartURL(row.StartURL)
		if idx, ok := byURL[key]; ok {
			live[idx].Managed = true
			if live[idx].Source == "cache" {
				live[idx].Source = "kanivet"
			}
			if row.Label != "" {
				live[idx].Label = row.Label
			}
			if live[idx].Region == "" {
				live[idx].Region = row.Region
			}
			continue
		}
		status := s.aws.sessionStatus(row.StartURL, row.Region, time.Now())
		status.Source = "kanivet"
		status.Managed = true
		status.Label = row.Label
		byURL[key] = len(live)
		live = append(live, status)
	}
	for i := range live {
		if live[i].Label == "" {
			live[i].Label = defaultSessionLabel(live[i].StartURL, live[i].SessionNames)
		}
	}
	sort.SliceStable(live, func(i, j int) bool {
		return strings.ToLower(live[i].Label) < strings.ToLower(live[j].Label)
	})
	return live
}

func (s *Service) SaveSSOSession(startURL, region, label string) error {
	if s.db == nil {
		return nil
	}
	now := time.Now().UnixMilli()
	err := s.db.SaveSSOSession(&db.SSOSession{
		StartURL:  normalizeStartURL(startURL),
		Region:    region,
		Label:     label,
		CreatedAt: now,
		UpdatedAt: now,
	})
	s.notifyAuthChanged(ProviderAWS)
	return err
}

func (s *Service) UpdateSSOSessionLabel(startURL, label string) error {
	if s.db == nil {
		return nil
	}
	err := s.db.UpdateSSOSessionLabel(normalizeStartURL(startURL), label)
	s.notifyAuthChanged(ProviderAWS)
	return err
}

// DeleteSSOSession forgets a Kanivet-added portal: its DB row and Kanivet's
// own cached tokens. Tokens and profiles created by the AWS CLI stay.
func (s *Service) DeleteSSOSession(startURL string) error {
	if err := s.aws.ForgetSSOSession(startURL); err != nil {
		log.Printf("Warning: failed to remove cached SSO tokens: %v", err)
	}
	if s.db == nil {
		return nil
	}
	err := s.db.DeleteSSOSession(normalizeStartURL(startURL))
	s.notifyAuthChanged(ProviderAWS)
	return err
}

func (s *Service) GetImportedClusterIDs() []string {
	return s.aws.GetImportedClusterIDs()
}

func (s *Service) DiscoverAllClusters(ctx context.Context) ([]DiscoveredCluster, error) {
	var allClusters []DiscoveredCluster
	var mu sync.Mutex
	var wg sync.WaitGroup

	wg.Add(3)

	go func() {
		defer wg.Done()
		profiles, err := s.aws.ListProfiles()
		if err != nil {
			return
		}
		for _, profile := range profiles {
			clusters, err := s.aws.DiscoverClusters(ctx, profile.Name, nil)
			if err != nil {
				continue
			}
			mu.Lock()
			allClusters = append(allClusters, clusters...)
			mu.Unlock()
		}
	}()

	go func() {
		defer wg.Done()
		projects, err := s.gcp.ListProjects(ctx)
		if err != nil {
			return
		}
		for _, project := range projects {
			clusters, err := s.gcp.DiscoverClusters(ctx, project.ID, nil)
			if err != nil {
				continue
			}
			mu.Lock()
			allClusters = append(allClusters, clusters...)
			mu.Unlock()
		}
	}()

	go func() {
		defer wg.Done()
		subs, err := s.azure.ListSubscriptions(ctx)
		if err != nil {
			return
		}
		for _, sub := range subs {
			clusters, err := s.azure.DiscoverClusters(ctx, sub.ID)
			if err != nil {
				continue
			}
			mu.Lock()
			allClusters = append(allClusters, clusters...)
			mu.Unlock()
		}
	}()

	wg.Wait()
	return allClusters, nil
}

func (s *Service) DiscoverClustersStreaming(ctx context.Context, req DiscoverRequest, eventCh chan<- DiscoveryEvent) {
	if req.Provider != ProviderAWS {
		defer close(eventCh)
		clusters, err := s.DiscoverClusters(ctx, req)
		if err != nil {
			eventCh <- DiscoveryEvent{Type: DiscoveryEventError, Error: err.Error()}
			return
		}
		for _, c := range clusters {
			cluster := c
			eventCh <- DiscoveryEvent{Type: DiscoveryEventCluster, Cluster: &cluster}
		}
		eventCh <- DiscoveryEvent{Type: DiscoveryEventComplete}
		return
	}

	var regions []string
	if req.Region != "" {
		regions = []string{req.Region}
	}
	if req.SSOStartURL != "" {
		s.aws.DiscoverClustersWithSSOStreaming(ctx, req.SSOStartURL, regions, req.AccountIDs, eventCh)
	} else {
		profile := req.Profile
		if profile == "" {
			profile = "default"
		}
		s.aws.DiscoverClustersStreaming(ctx, profile, regions, eventCh)
	}
}

func (s *Service) AWSProvider() *AWSProvider {
	return s.aws
}

// StartAuthMonitor renews expiring SSO tokens in the background (silently,
// with refresh tokens) and broadcasts whenever the sign-in picture changes,
// including changes made from a terminal. It never opens a browser.
func (s *Service) StartAuthMonitor(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	slow := time.NewTicker(5 * time.Minute)
	defer slow.Stop()

	s.lastSessionsJSON = s.sessionsFingerprint()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refreshCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
			s.aws.RefreshExpiringSSOTokens(refreshCtx)
			cancel()
			if fp := s.sessionsFingerprint(); fp != s.lastSessionsJSON {
				s.lastSessionsJSON = fp
				s.notifyAuthChanged(ProviderAWS)
			}
		case <-slow.C:
			for _, provider := range []Provider{ProviderGCP, ProviderAzure} {
				before := s.providerSummary(ctx, provider, false)
				after := s.providerSummary(ctx, provider, true)
				if before.State != after.State || before.Identity != after.Identity {
					s.notifyAuthChanged(provider)
				}
			}
		}
	}
}

func (s *Service) sessionsFingerprint() string {
	sessions := s.aws.GetSSOSessions()
	type fp struct {
		URL   string
		State string
		Exp   int64
		Login bool
	}
	out := make([]fp, 0, len(sessions))
	for _, sess := range sessions {
		out = append(out, fp{URL: sess.StartURL, State: sess.State, Exp: sess.ExpiresAt, Login: sess.Login != nil})
	}
	data, _ := jsonv2.Marshal(out)
	return string(data)
}

// IsLoginRequired reports whether err means "sign in first".
func IsLoginRequired(err error) bool {
	return errors.Is(err, ErrSSOLoginRequired)
}
