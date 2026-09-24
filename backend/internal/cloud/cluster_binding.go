package cloud

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/kanivet/backend/internal/db"
)

// ErrInvalidBinding marks a binding request the caller got wrong, as opposed
// to a failure while preparing the profile.
var ErrInvalidBinding = errors.New("invalid cluster binding")

func bindingFromRow(row *db.ClusterAWSBinding) *ClusterSSOBinding {
	if row == nil {
		return nil
	}
	return &ClusterSSOBinding{StartURL: row.StartURL, AccountID: row.AccountID, RoleName: row.RoleName, Profile: row.Profile}
}

func (s *Service) clusterSSOBinding(cluster string) *ClusterSSOBinding {
	if s.db == nil {
		return nil
	}
	row, err := s.db.GetClusterAWSBinding(cluster)
	if err != nil {
		log.Printf("[ClusterBinding] failed to read binding for %s: %v", cluster, err)
		return nil
	}
	return bindingFromRow(row)
}

// BindClusterSSO makes Kanivet reach a kubeconfig context with an Identity
// Center account and role, whatever profile its exec block would pick up. It
// reuses the user's own profile for that account and role when one exists and
// writes a kanivet-sso-* profile otherwise. Calling it again switches role.
func (s *Service) BindClusterSSO(ctx context.Context, cluster, startURL, accountID, roleName string) (*ClusterSSOBinding, error) {
	cluster = strings.TrimSpace(cluster)
	accountID = strings.TrimSpace(accountID)
	roleName = strings.TrimSpace(roleName)
	if cluster == "" || startURL == "" || accountID == "" || roleName == "" {
		return nil, fmt.Errorf("%w: cluster, startUrl, accountId and roleName are required", ErrInvalidBinding)
	}
	if s.db == nil {
		return nil, fmt.Errorf("no database to store the binding in")
	}
	info, err := s.DescribeClusterAuth(ctx, cluster)
	if err != nil {
		return nil, err
	}
	if info.Provider != "aws" {
		return nil, fmt.Errorf("%w: %s is not an EKS context", ErrInvalidBinding, cluster)
	}
	if info.AccountID != "" && info.AccountID != accountID {
		return nil, fmt.Errorf("%w: cluster %s is in account %s, not %s", ErrInvalidBinding, cluster, info.AccountID, accountID)
	}

	startURL = normalizeStartURL(startURL)
	profile, err := s.aws.ssoProfileForImport(ctx, startURL, accountID, roleName, "")
	if err != nil {
		return nil, fmt.Errorf("failed to prepare an AWS profile for %s/%s: %w", accountID, roleName, err)
	}
	row := &db.ClusterAWSBinding{Cluster: cluster, StartURL: startURL, AccountID: accountID, RoleName: roleName, Profile: profile}
	if err := s.db.SaveClusterAWSBinding(row); err != nil {
		return nil, err
	}
	log.Printf("[ClusterBinding] %s now uses %s/%s via profile %s", cluster, accountID, roleName, profile)
	s.notifyAuthChanged(ProviderAWS)
	return bindingFromRow(row), nil
}

// UnbindClusterSSO returns a context to whatever its kubeconfig says.
func (s *Service) UnbindClusterSSO(cluster string) error {
	if s.db == nil {
		return nil
	}
	if err := s.db.DeleteClusterAWSBinding(cluster); err != nil {
		return err
	}
	s.notifyAuthChanged(ProviderAWS)
	return nil
}
