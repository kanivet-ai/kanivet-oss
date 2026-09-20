package cloud

import (
	"errors"
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
		cloud.GET("/auth", h.GetAuthSummary)
		cloud.GET("/cluster-auth", h.DescribeClusterAuth)
		cloud.GET("/login-jobs/:id", h.GetLoginJob)
		cloud.DELETE("/login-jobs/:id", h.CancelLoginJob)

		aws := cloud.Group("/aws")
		{
			aws.GET("/profiles", h.ListAWSProfiles)
			aws.POST("/login", h.LoginAWSProfile)
			aws.GET("/sso/sessions", h.GetAWSSSOSessions)
			aws.POST("/sso/login", h.BeginAWSSSOLogin)
			aws.GET("/sso/login/:id", h.GetAWSSSOLogin)
			aws.DELETE("/sso/login/:id", h.CancelAWSSSOLogin)
			aws.POST("/sso/refresh", h.RefreshAWSSSOSession)
			aws.POST("/sso/signout", h.SignOutAWSSSO)
			aws.GET("/sso/accounts", h.GetAWSSSOAccounts)
			aws.GET("/sso/roles", h.GetAWSSSOAccountRoles)
			aws.POST("/sso/session", h.SaveSSOSession)
			aws.PUT("/sso/session/label", h.UpdateSSOSessionLabel)
			aws.DELETE("/sso/session", h.DeleteSSOSession)
			aws.GET("/account", h.GetAWSAccountID)
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

// writeError maps domain errors to actionable HTTP responses. A sign-in
// requirement is 401 with a stable code so the UI can offer the right button
// instead of a generic failure.
func writeError(c *gin.Context, err error) {
	var loginReq *SSOLoginRequiredError
	if errors.As(err, &loginReq) {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":    loginReq.Error(),
			"code":     "sso_login_required",
			"startUrl": loginReq.StartURL,
		})
		return
	}
	var missing *CLIMissingError
	if errors.As(err, &missing) {
		c.JSON(http.StatusFailedDependency, gin.H{
			"error":       missing.Error(),
			"code":        "cli_missing",
			"binary":      missing.Binary,
			"installHint": cliInstallHint(missing.Binary),
		})
		return
	}
	c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
}

func (h *Handler) GetAuthStatus(c *gin.Context) {
	status := h.service.GetAuthStatus(c.Request.Context())
	c.JSON(http.StatusOK, gin.H{"status": status})
}

func (h *Handler) GetAuthSummary(c *gin.Context) {
	force := c.Query("force") == "1" || c.Query("force") == "true"
	c.JSON(http.StatusOK, h.service.GetAuthSummary(c.Request.Context(), force))
}

func (h *Handler) DescribeClusterAuth(c *gin.Context) {
	cluster := c.Query("cluster")
	if cluster == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cluster is required"})
		return
	}
	info, err := h.service.DescribeClusterAuth(c.Request.Context(), cluster)
	if err != nil && info == nil {
		writeError(c, err)
		return
	}
	if err != nil {
		info.Hint = err.Error()
	}
	c.JSON(http.StatusOK, info)
}

func (h *Handler) GetLoginJob(c *gin.Context) {
	job, ok := h.service.GetLoginJob(c.Param("id"))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "login job not found"})
		return
	}
	c.JSON(http.StatusOK, job)
}

func (h *Handler) CancelLoginJob(c *gin.Context) {
	if !h.service.CancelLoginJob(c.Param("id")) {
		c.JSON(http.StatusNotFound, gin.H{"error": "login job not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *Handler) ListAWSProfiles(c *gin.Context) {
	profiles, err := h.service.ListAWSProfiles()
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"profiles": profiles})
}

func (h *Handler) LoginAWSProfile(c *gin.Context) {
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
	job, err := h.service.LoginAWSProfile(c.Request.Context(), req.Profile)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, job)
}

func (h *Handler) BeginAWSSSOLogin(c *gin.Context) {
	var req struct {
		StartURL    string `json:"startUrl"`
		Region      string `json:"region"`
		OpenBrowser *bool  `json:"openBrowser"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.StartURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "startUrl is required"})
		return
	}
	openBrowser := req.OpenBrowser == nil || *req.OpenBrowser
	session, err := h.service.BeginAWSSSOLogin(c.Request.Context(), req.StartURL, req.Region, openBrowser)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, session)
}

func (h *Handler) GetAWSSSOLogin(c *gin.Context) {
	session, ok := h.service.GetAWSSSOLogin(c.Param("id"))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "login not found"})
		return
	}
	c.JSON(http.StatusOK, session)
}

func (h *Handler) CancelAWSSSOLogin(c *gin.Context) {
	if !h.service.CancelAWSSSOLogin(c.Param("id")) {
		c.JSON(http.StatusNotFound, gin.H{"error": "login not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *Handler) RefreshAWSSSOSession(c *gin.Context) {
	var req struct {
		StartURL string `json:"startUrl"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.StartURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "startUrl is required"})
		return
	}
	session, err := h.service.RefreshAWSSSOSession(c.Request.Context(), req.StartURL)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"session": session})
}

func (h *Handler) SignOutAWSSSO(c *gin.Context) {
	var req struct {
		StartURL string `json:"startUrl"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.StartURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "startUrl is required"})
		return
	}
	if err := h.service.SignOutAWSSSO(req.StartURL); err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
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
		writeError(c, err)
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
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"roles": roles})
}

func (h *Handler) GetAWSAccountID(c *gin.Context) {
	profile := c.Query("profile")
	if profile == "" {
		profile = "default"
	}
	accountID, err := h.service.GetAWSAccountID(c.Request.Context(), profile)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"accountId": accountID})
}

func (h *Handler) ListGCPProjects(c *gin.Context) {
	projects, err := h.service.ListGCPProjects(c.Request.Context())
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"projects": projects})
}

func (h *Handler) LoginGCP(c *gin.Context) {
	job, err := h.service.LoginGCP(c.Request.Context())
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, job)
}

func (h *Handler) GetGCPLocations(c *gin.Context) {
	projectID := c.Query("projectId")
	if projectID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "projectId is required"})
		return
	}
	locations, err := h.service.GetGCPLocations(c.Request.Context(), projectID)
	if err != nil {
		writeError(c, err)
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
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *Handler) ListAzureSubscriptions(c *gin.Context) {
	subs, err := h.service.ListAzureSubscriptions(c.Request.Context())
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"subscriptions": subs})
}

func (h *Handler) LoginAzure(c *gin.Context) {
	job, err := h.service.LoginAzure(c.Request.Context())
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, job)
}

func (h *Handler) DiscoverClusters(c *gin.Context) {
	var req DiscoverRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	clusters, err := h.service.DiscoverClusters(c.Request.Context(), req)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"clusters": clusters})
}

func (h *Handler) DiscoverAllClusters(c *gin.Context) {
	clusters, err := h.service.DiscoverAllClusters(c.Request.Context())
	if err != nil {
		writeError(c, err)
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
	if err := h.service.ImportCluster(c.Request.Context(), req); err != nil {
		log.Printf("[Handler.ImportCluster] %s: %v", req.Name, err)
		writeError(c, err)
		return
	}
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
		StartURL string `json:"startUrl"`
		Region   string `json:"region"`
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
	if req.Region == "" {
		req.Region = "us-east-1"
	}
	if err := h.service.SaveSSOSession(req.StartURL, req.Region, req.Label); err != nil {
		writeError(c, err)
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
		writeError(c, err)
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
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}
