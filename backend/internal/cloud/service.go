package cloud

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kanivet/backend/internal/db"
)

func KanivetKubeconfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".kube", "kanivet-clusters")
}

type Service struct {
	aws             *AWSProvider
	gcp             *GCPProvider
	azure           *AzureProvider
	db              *db.DB
	mu              sync.RWMutex
	importJobs      map[string]*BatchImportJob
	importMu        sync.RWMutex
	onBatchComplete func()
}

func NewService(database *db.DB) *Service {
	return &Service{
		aws:        NewAWSProvider(),
		gcp:        NewGCPProvider(),
		azure:      NewAzureProvider(),
		db:         database,
		importJobs: make(map[string]*BatchImportJob),
	}
}

func (s *Service) SetOnBatchComplete(fn func()) {
	s.onBatchComplete = fn
}

func (s *Service) GetAuthStatus(ctx context.Context) map[Provider]bool {
	status := make(map[Provider]bool)
	var wg sync.WaitGroup
	var mu sync.Mutex

	wg.Add(3)
	go func() {
		defer wg.Done()
		profiles, err := s.aws.ListProfiles()
		mu.Lock()
		status[ProviderAWS] = err == nil && len(profiles) > 0
		mu.Unlock()
	}()
	go func() {
		defer wg.Done()
		isAuth := s.gcp.IsAuthenticated(ctx)
		mu.Lock()
		status[ProviderGCP] = isAuth
		mu.Unlock()
	}()
	go func() {
		defer wg.Done()
		isAuth := s.azure.IsAuthenticated(ctx)
		mu.Lock()
		status[ProviderAzure] = isAuth
		mu.Unlock()
	}()
	wg.Wait()

	return status
}

func (s *Service) ListAWSProfiles() ([]AWSProfile, error) {
	return s.aws.ListProfiles()
}

func (s *Service) LoginAWS(ctx context.Context, profile string) error {
	return s.aws.LoginWithProfile(ctx, profile)
}

func (s *Service) StartAWSSSOLogin(ctx context.Context, startURL, region string) (*SSOLoginResponse, error) {
	return s.aws.StartSSOLogin(ctx, startURL, region)
}

func (s *Service) GetAWSSSOAccountRoles(ctx context.Context, startURL, accountID string) ([]string, error) {
	return s.aws.GetSSORoles(ctx, startURL, accountID)
}

func (s *Service) ActivateAWSSSOAccount(ctx context.Context, startURL, accountID, roleName, region string) (*SSOActivateResponse, error) {
	return s.aws.ActivateSSOAccount(ctx, startURL, accountID, roleName, region)
}

func (s *Service) DeactivateAWSSSOAccount(ctx context.Context) error {
	return s.aws.DeactivateSSOAccount(ctx)
}

func (s *Service) ListGCPProjects(ctx context.Context) ([]GCPProject, error) {
	return s.gcp.ListProjects(ctx)
}

func (s *Service) LoginGCP(ctx context.Context) error {
	return s.gcp.Login(ctx)
}

func (s *Service) ListAzureSubscriptions(ctx context.Context) ([]AzureSubscription, error) {
	return s.azure.ListSubscriptions(ctx)
}

func (s *Service) LoginAzure(ctx context.Context) error {
	return s.azure.Login(ctx)
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

func (s *Service) RefreshAWSCredentials(ctx context.Context, profile string) error {
	return s.aws.RefreshSSOCredentials(ctx, profile)
}

func (s *Service) GetGCPLocations(ctx context.Context, projectID string) ([]string, error) {
	return s.gcp.ListLocations(ctx, projectID)
}

func (s *Service) UseGCPServiceAccount(ctx context.Context, keyFilePath string) error {
	return s.gcp.UseServiceAccount(ctx, keyFilePath)
}

func (s *Service) GetAWSSSOSessions() []SSOSessionStatus {
	start := time.Now()
	dbSessions, err := s.db.GetSSOSessions()
	log.Printf("[SSO] GetSSOSessions DB query took %v", time.Since(start))
	if err != nil {
		return s.aws.GetSSOSessions()
	}
	awsStart := time.Now()
	awsSessions := s.aws.GetSSOSessions()
	log.Printf("[SSO] GetSSOSessions AWS cache took %v", time.Since(awsStart))
	awsMap := make(map[string]SSOSessionStatus)
	for _, sess := range awsSessions {
		awsMap[normalizeStartURL(sess.StartURL)] = sess
	}
	var result []SSOSessionStatus
	for _, dbSess := range dbSessions {
		normalized := normalizeStartURL(dbSess.StartURL)
		if awsSess, ok := awsMap[normalized]; ok {
			result = append(result, SSOSessionStatus{
				StartURL:  dbSess.StartURL,
				Region:    dbSess.Region,
				ExpiresAt: awsSess.ExpiresAt,
				IsValid:   awsSess.IsValid,
				Label:     dbSess.Label,
			})
			delete(awsMap, normalized)
		} else {
			result = append(result, SSOSessionStatus{
				StartURL:  dbSess.StartURL,
				Region:    dbSess.Region,
				ExpiresAt: dbSess.ExpiresAt,
				IsValid:   false,
				Label:     dbSess.Label,
			})
		}
	}
	for _, awsSess := range awsMap {
		result = append(result, awsSess)
	}
	log.Printf("[SSO] GetAWSSSOSessions total took %v, returning %d sessions", time.Since(start), len(result))
	return result
}

func (s *Service) GetAWSSSOAccounts(ctx context.Context, startURL string) ([]SSOAccount, error) {
	return s.aws.GetSSOAccounts(ctx, startURL)
}

func (s *Service) SaveSSOSession(startURL, region, label string, expiresAt int64) error {
	now := time.Now().UnixMilli()
	return s.db.SaveSSOSession(&db.SSOSession{
		StartURL:  normalizeStartURL(startURL),
		Region:    region,
		Label:     label,
		ExpiresAt: expiresAt,
		CreatedAt: now,
		UpdatedAt: now,
	})
}

func (s *Service) UpdateSSOSessionLabel(startURL, label string) error {
	return s.db.UpdateSSOSessionLabel(normalizeStartURL(startURL), label)
}

func (s *Service) DeleteSSOSession(startURL string) error {
	if err := s.aws.InvalidateSSOSession(startURL); err != nil {
		log.Printf("Warning: failed to invalidate SSO session: %v", err)
	}
	return s.db.DeleteSSOSession(normalizeStartURL(startURL))
}

func (s *Service) GetSSOActiveAccount() (*db.SSOActiveAccount, error) {
	return s.db.GetSSOActiveAccount()
}

func (s *Service) SetSSOActiveAccount(startURL, accountID, accountName, profileName, roleName string, expiresAt int64) error {
	return s.db.SetSSOActiveAccount(&db.SSOActiveAccount{
		StartURL:    startURL,
		AccountID:   accountID,
		AccountName: accountName,
		ProfileName: profileName,
		RoleName:    roleName,
		ExpiresAt:   expiresAt,
	})
}

func (s *Service) ClearSSOActiveAccount() error {
	return s.db.ClearSSOActiveAccount()
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
