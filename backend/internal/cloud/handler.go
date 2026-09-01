package cloud

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	service           *Service
	onClusterImported func()
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) SetOnClusterImported(fn func()) {
	h.onClusterImported = fn
}

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, middleware ...gin.HandlerFunc) {
	cloud := rg.Group("/cloud", middleware...)
	{
		cloud.GET("/status", h.GetAuthStatus)

		aws := cloud.Group("/aws")
		{
			aws.GET("/profiles", h.ListAWSProfiles)
			aws.POST("/login", h.LoginAWS)
			aws.POST("/sso/start", h.StartAWSSSOLogin)
			aws.GET("/sso/sessions", h.GetAWSSSOSessions)
			aws.GET("/sso/accounts", h.GetAWSSSOAccounts)
			aws.GET("/sso/roles", h.GetAWSSSOAccountRoles)
			aws.POST("/sso/activate", h.ActivateAWSSSOAccount)
			aws.POST("/sso/deactivate", h.DeactivateAWSSSOAccount)
			aws.POST("/sso/session", h.SaveSSOSession)
			aws.PUT("/sso/session/label", h.UpdateSSOSessionLabel)
			aws.DELETE("/sso/session", h.DeleteSSOSession)
			aws.GET("/sso/active-account", h.GetSSOActiveAccount)
			aws.POST("/sso/active-account", h.SetSSOActiveAccount)
			aws.DELETE("/sso/active-account", h.ClearSSOActiveAccount)
			aws.GET("/account", h.GetAWSAccountID)
			aws.POST("/refresh", h.RefreshAWSCredentials)
		}

		gcp := cloud.Group("/gcp")
		{
			gcp.GET("/projects", h.ListGCPProjects)
			gcp.POST("/login", h.LoginGCP)
			gcp.GET("/locations", h.GetGCPLocations)
			gcp.POST("/service-account", h.UseGCPServiceAccount)
		}

		azure := cloud.Group("/azure")
		{
			azure.GET("/subscriptions", h.ListAzureSubscriptions)
			azure.POST("/login", h.LoginAzure)
		}

		cloud.POST("/discover", h.DiscoverClusters)
		cloud.POST("/discover/all", h.DiscoverAllClusters)
		cloud.GET("/imported", h.GetImportedClusters)
		cloud.POST("/import", h.ImportCluster)
		cloud.POST("/import/batch", h.BatchImportClusters)
		cloud.GET("/import/batch/status", h.GetBatchImportStatus)
	}
}

func (h *Handler) GetAuthStatus(c *gin.Context) {
	status := h.service.GetAuthStatus(c.Request.Context())
	c.JSON(http.StatusOK, gin.H{"status": status})
}

func (h *Handler) ListAWSProfiles(c *gin.Context) {
	profiles, err := h.service.ListAWSProfiles()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"profiles": profiles})
}

func (h *Handler) LoginAWS(c *gin.Context) {
	var req struct {
		Profile string `json:"profile"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Profile == "" {
		req.Profile = "default"
	}
	if err := h.service.LoginAWS(c.Request.Context(), req.Profile); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *Handler) StartAWSSSOLogin(c *gin.Context) {
	var req SSOLoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	resp, err := h.service.StartAWSSSOLogin(c.Request.Context(), req.StartURL, req.Region)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (h *Handler) GetAWSSSOSessions(c *gin.Context) {
	sessions := h.service.GetAWSSSOSessions()
	c.JSON(http.StatusOK, gin.H{"sessions": sessions})
}

func (h *Handler) GetAWSSSOAccounts(c *gin.Context) {
	startURL := c.Query("startUrl")
	if startURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "startUrl is required"})
		return
	}
	accounts, err := h.service.GetAWSSSOAccounts(c.Request.Context(), startURL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"accounts": accounts})
}

func (h *Handler) GetAWSSSOAccountRoles(c *gin.Context) {
	startURL := c.Query("startUrl")
	accountID := c.Query("accountId")
	if startURL == "" || accountID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "startUrl and accountId are required"})
		return
	}
	roles, err := h.service.GetAWSSSOAccountRoles(c.Request.Context(), startURL, accountID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"roles": roles})
}

func (h *Handler) ActivateAWSSSOAccount(c *gin.Context) {
	var req struct {
		StartURL  string `json:"startUrl"`
		AccountID string `json:"accountId"`
		RoleName  string `json:"roleName"`
		Region    string `json:"region"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.StartURL == "" || req.AccountID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "startUrl and accountId are required"})
		return
	}
	result, err := h.service.ActivateAWSSSOAccount(c.Request.Context(), req.StartURL, req.AccountID, req.RoleName, req.Region)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *Handler) DeactivateAWSSSOAccount(c *gin.Context) {
	if err := h.service.DeactivateAWSSSOAccount(c.Request.Context()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *Handler) GetAWSAccountID(c *gin.Context) {
	profile := c.Query("profile")
	if profile == "" {
		profile = "default"
	}
	accountID, err := h.service.GetAWSAccountID(c.Request.Context(), profile)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"accountId": accountID})
}

func (h *Handler) RefreshAWSCredentials(c *gin.Context) {
	var req struct {
		Profile string `json:"profile"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.service.RefreshAWSCredentials(c.Request.Context(), req.Profile); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *Handler) ListGCPProjects(c *gin.Context) {
	projects, err := h.service.ListGCPProjects(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"projects": projects})
}

func (h *Handler) LoginGCP(c *gin.Context) {
	if err := h.service.LoginGCP(c.Request.Context()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *Handler) GetGCPLocations(c *gin.Context) {
	projectID := c.Query("projectId")
	if projectID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "projectId is required"})
		return
	}
	locations, err := h.service.GetGCPLocations(c.Request.Context(), projectID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"locations": locations})
}

func (h *Handler) UseGCPServiceAccount(c *gin.Context) {
	var req struct {
		KeyFilePath string `json:"keyFilePath"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.service.UseGCPServiceAccount(c.Request.Context(), req.KeyFilePath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *Handler) ListAzureSubscriptions(c *gin.Context) {
	subs, err := h.service.ListAzureSubscriptions(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"subscriptions": subs})
}

func (h *Handler) LoginAzure(c *gin.Context) {
	if err := h.service.LoginAzure(c.Request.Context()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *Handler) DiscoverClusters(c *gin.Context) {
	var req DiscoverRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	clusters, err := h.service.DiscoverClusters(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"clusters": clusters})
}

func (h *Handler) DiscoverAllClusters(c *gin.Context) {
	clusters, err := h.service.DiscoverAllClusters(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"clusters": clusters})
}

func (h *Handler) ImportCluster(c *gin.Context) {
	var req ImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	log.Printf("[Handler.ImportCluster] Received request: provider=%s, name=%s, region=%s, accountId=%s, ssoStartUrl=%s, profile=%s, ssoRoleName=%s",
		req.Provider, req.Name, req.Region, req.AccountID, req.SSOStartURL, req.Profile, req.SSORoleName)
	if err := h.service.ImportCluster(c.Request.Context(), req); err != nil {
		log.Printf("[Handler.ImportCluster] ERROR: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	log.Printf("[Handler.ImportCluster] SUCCESS")
	if h.onClusterImported != nil {
		h.onClusterImported()
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *Handler) BatchImportClusters(c *gin.Context) {
	var req BatchImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	log.Printf("[Handler.BatchImportClusters] Starting batch import for %d clusters", len(req.Clusters))
	jobID := h.service.StartBatchImport(c.Request.Context(), req)
	c.JSON(http.StatusAccepted, gin.H{"jobId": jobID})
}

func (h *Handler) GetBatchImportStatus(c *gin.Context) {
	jobID := c.Query("jobId")
	if jobID != "" {
		job := h.service.GetBatchImportJob(jobID)
		if job == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "job not found"})
			return
		}
		c.JSON(http.StatusOK, job)
		return
	}
	job := h.service.GetActiveBatchImportJob()
	if job == nil {
		c.JSON(http.StatusOK, gin.H{"active": false})
		return
	}
	c.JSON(http.StatusOK, job)
}

func (h *Handler) GetImportedClusters(c *gin.Context) {
	clusters := h.service.GetImportedClusterIDs()
	c.JSON(http.StatusOK, gin.H{"clusters": clusters})
}

func (h *Handler) SaveSSOSession(c *gin.Context) {
	var req struct {
		StartURL  string `json:"startUrl"`
		Region    string `json:"region"`
		Label     string `json:"label"`
		ExpiresAt int64  `json:"expiresAt"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.StartURL == "" || req.Region == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "startUrl and region are required"})
		return
	}
	if err := h.service.SaveSSOSession(req.StartURL, req.Region, req.Label, req.ExpiresAt); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *Handler) UpdateSSOSessionLabel(c *gin.Context) {
	var req struct {
		StartURL string `json:"startUrl"`
		Label    string `json:"label"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.StartURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "startUrl is required"})
		return
	}
	if err := h.service.UpdateSSOSessionLabel(req.StartURL, req.Label); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *Handler) DeleteSSOSession(c *gin.Context) {
	startURL := c.Query("startUrl")
	if startURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "startUrl is required"})
		return
	}
	if err := h.service.DeleteSSOSession(startURL); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *Handler) GetSSOActiveAccount(c *gin.Context) {
	account, err := h.service.GetSSOActiveAccount()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"account": nil})
		return
	}
	c.JSON(http.StatusOK, gin.H{"account": account})
}

func (h *Handler) SetSSOActiveAccount(c *gin.Context) {
	var req struct {
		StartURL    string `json:"startUrl"`
		AccountID   string `json:"accountId"`
		AccountName string `json:"accountName"`
		ProfileName string `json:"profileName"`
		RoleName    string `json:"roleName"`
		ExpiresAt   int64  `json:"expiresAt"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.service.SetSSOActiveAccount(req.StartURL, req.AccountID, req.AccountName, req.ProfileName, req.RoleName, req.ExpiresAt); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *Handler) ClearSSOActiveAccount(c *gin.Context) {
	if err := h.service.ClearSSOActiveAccount(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}
