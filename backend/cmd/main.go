package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	runtimepprof "runtime/pprof"
	"strings"
	"syscall"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/kanivet/backend/internal/api"
	"github.com/kanivet/backend/internal/cache"
	"github.com/kanivet/backend/internal/cloud"
	"github.com/kanivet/backend/internal/cluster"
	"github.com/kanivet/backend/internal/faults"
	"github.com/kanivet/backend/internal/finops"
	"github.com/kanivet/backend/internal/helm"
	"github.com/kanivet/backend/internal/incidents"
	"github.com/kanivet/backend/internal/k8s"
	"github.com/kanivet/backend/internal/k8s/watcher"
	"github.com/kanivet/backend/internal/localsecurity"
	"github.com/kanivet/backend/internal/logger"
	"github.com/kanivet/backend/internal/search"
	"github.com/kanivet/backend/internal/terminal/infrastructure"
	"github.com/kanivet/backend/internal/terminal/service"
	terminalws "github.com/kanivet/backend/internal/terminal/websocket"
	"github.com/kanivet/backend/internal/themes"
	kanivetws "github.com/kanivet/backend/internal/websocket"
	"github.com/kanivet/backend/internal/websocket/handlers"
)

func setupLogging() error {
	// Initialize the logger - behavior is determined at build time
	// Development build: full logging to file and stdout
	// Production build (with -tags production): all logging disabled
	return logger.Init()
}

func RequestLogger() gin.HandlerFunc {
	// Skip request logging in production to avoid performance overhead
	if logger.IsProduction() {
		return func(c *gin.Context) {
			c.Next()
		}
	}

	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		method := c.Request.Method

		logger.Debug("[REQUEST START] %s %s", method, path)

		c.Next()

		duration := time.Since(start)
		status := c.Writer.Status()

		logger.Debug("[REQUEST END] %s %s - Status: %d - Duration: %v", method, path, status, duration)
	}
}

// CompositeBroadcaster broadcasts to multiple broadcasters
type CompositeBroadcaster struct {
	broadcasters []watcher.Broadcaster
}

func (cb *CompositeBroadcaster) Broadcast(topic string, message watcher.Message) error {
	var errs []error
	for _, b := range cb.broadcasters {
		if err := b.Broadcast(topic, message); err != nil {
			log.Printf("Broadcast error on topic %s: %v", topic, err)
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (cb *CompositeBroadcaster) BroadcastDirect(topic string, message watcher.Message) error {
	var errs []error
	for _, b := range cb.broadcasters {
		if err := b.BroadcastDirect(topic, message); err != nil {
			log.Printf("BroadcastDirect error on topic %s: %v", topic, err)
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (cb *CompositeBroadcaster) BroadcastAll(message watcher.Message) error {
	var errs []error
	for _, b := range cb.broadcasters {
		if err := b.BroadcastAll(message); err != nil {
			log.Printf("BroadcastAll error: %v", err)
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (cb *CompositeBroadcaster) FlushTopic(topic string) error {
	var errs []error
	for _, b := range cb.broadcasters {
		if err := b.FlushTopic(topic); err != nil {
			log.Printf("FlushTopic error on topic %s: %v", topic, err)
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (cb *CompositeBroadcaster) SetSortPreference(topic, sortBy, sortOrder string) {
	for _, b := range cb.broadcasters {
		b.SetSortPreference(topic, sortBy, sortOrder)
	}
}

func (cb *CompositeBroadcaster) GetSortPreference(topic string) (string, string) {
	if len(cb.broadcasters) > 0 {
		return cb.broadcasters[0].GetSortPreference(topic)
	}
	return "age", "desc"
}

func (cb *CompositeBroadcaster) CleanupTopic(topic string) {
	for _, b := range cb.broadcasters {
		b.CleanupTopic(topic)
	}
}

func listenOnConfiguredAddr() (net.Listener, error) {
	port := os.Getenv("PORT")
	defaultPort := port == ""
	if defaultPort {
		port = "53727"
	}

	addr := port
	if !strings.Contains(addr, ":") {
		addr = "127.0.0.1:" + addr
	} else if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}

	listener, err := net.Listen("tcp", addr)
	if err == nil {
		return listener, nil
	}
	if defaultPort && errors.Is(err, syscall.EADDRINUSE) {
		log.Printf("Default backend port %s is already in use; falling back to an ephemeral port", addr)
		return net.Listen("tcp", "127.0.0.1:0")
	}
	return nil, err
}

func main() {
	// Global panic recovery - captures panics in main goroutine then re-panics
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[FATAL] Panic in main: %v", r)
			faults.CaptureExceptionWithContext(
				fmt.Errorf("panic in main: %v", r),
				map[string]any{"panic": r, "stack": string(debug.Stack())},
			)
			panic(r) // Re-panic to crash - users need to know
		}
	}()

	// Cap the Go heap with a soft memory limit so the GC keeps RSS near a
	// ceiling instead of drifting toward 2x the live set (default GOGC=100
	// behaviour). The search index is the dominant consumer and re-indexing
	// churns a lot of short-lived garbage; without a limit, freed pages linger
	// as RSS. Honours an explicit GOMEMLIMIT env var if the operator set one.
	if os.Getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(2 << 30) // 2 GiB soft limit
	}

	_ = faults.Init()

	if err := setupLogging(); err != nil {
		faults.CaptureExceptionWithContext(
			fmt.Errorf("failed to setup logging: %w", err),
			map[string]any{"phase": "setupLogging"},
		)
		log.Fatal("Failed to setup logging:", err)
	}

	if logger.IsProduction() {
		gin.SetMode(gin.ReleaseMode)
	}

	router := gin.Default()
	router.Use(RequestLogger())
	router.Use(cors.New(cors.Config{
		AllowOrigins: []string{"http://localhost:3000", "http://localhost:5173"},
		AllowOriginFunc: func(origin string) bool {
			if origin == "" || origin == "file://" || origin == "null" {
				return true
			}
			return strings.HasPrefix(origin, "app://") || strings.HasPrefix(origin, "capacitor://") || strings.HasPrefix(origin, "http://localhost:")
		},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Cache-Control", "Upgrade", "Connection", "Sec-WebSocket-Key", "Sec-WebSocket-Version", "Sec-WebSocket-Extensions", "Sec-WebSocket-Protocol", "X-Session-Secret"},
		AllowCredentials: true,
		AllowWebSockets:  true,
	}))

	router.GET("/api/v1/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	router.Use(localsecurity.SessionSecretMiddleware())

	k8sClient := k8s.NewClient()

	// Create cache invalidation bus for coordinating cache invalidations
	invalidationBus := cache.NewInvalidationBus()

	// Create shared cache instance with invalidation support
	cacheInstance := cache.NewCacheWithInvalidation(5*time.Minute, 10*time.Minute, invalidationBus)

	// Create API handler with dependencies
	apiHandler := api.NewHandlerWithDeps(k8sClient, cacheInstance.Cache, invalidationBus)

	// Create and start cluster status manager for background monitoring
	statusManager := cluster.NewStatusManager(k8sClient)
	appCtx, appCancel := context.WithCancel(context.Background())
	defer appCancel()
	apiHandler.SetStatusManager(statusManager)

	// WebSocket infrastructure
	wsServer := kanivetws.NewServer()
	// Background status probes only run while a UI is connected.
	statusManager.SetActiveCheck(func() bool { return wsServer.Hub().CountConnections() > 0 })
	statusManager.Start(appCtx)
	// Attach hub to API handler for broadcasting features like counts
	apiHandler.AttachHub(wsServer.Hub())

	// Create a special broadcaster that routes events to the search service
	searchBroadcaster := &search.SearchBroadcaster{}

	// Create k8s watcher service with a composite broadcaster
	compositeBroadcaster := &CompositeBroadcaster{
		broadcasters: []watcher.Broadcaster{
			wsServer.GetBroadcaster(), // Proper WebSocket broadcaster
			searchBroadcaster,         // Search service broadcaster
		},
	}
	watcherService := watcher.NewService(k8sClient, compositeBroadcaster)
	watcherService.SetDB(apiHandler.GetDB())
	watcherService.SetInvalidationBus(invalidationBus)

	// Setup WebSocket handlers
	wsServer.SetupDefaultHandlers(watcherService)

	// Setup terminal service and handler
	terminalFactory := infrastructure.NewPTYTerminalFactory()
	terminalService := service.NewTerminalService(terminalFactory)
	terminalHandler := terminalws.NewTerminalHandler(terminalService)

	// Register terminal handler
	wsServer.RegisterHandler("terminal", terminalHandler)

	// Register metrics handler
	metricsHandler := kanivetws.NewMetricsStreamHandler(apiHandler.GetMetricsService())
	wsServer.RegisterHandler("metrics", metricsHandler)

	// Wire the per-cluster Mimir tenant lookup so X-Scope-OrgID is sent on
	// every Mimir request. Without this, multi-tenant Mimir silently returns
	// empty results and dashboards render blank.
	{
		dbForLookup := apiHandler.GetDB()
		apiHandler.GetMetricsService().SetMimirTenantLookup(func(cluster string) string {
			s, err := dbForLookup.GetClusterMetricsSettings(cluster)
			if err != nil || s == nil {
				return ""
			}
			return s.MimirTenant
		})
		apiHandler.GetMetricsService().SetMimirServiceLookup(func(cluster string) (string, string) {
			s, err := dbForLookup.GetClusterMetricsSettings(cluster)
			if err != nil || s == nil {
				return "", ""
			}
			return s.MimirNamespace, s.MimirService
		})
	}

	// Register logs handler
	logsHandler := handlers.NewLogsHandler(k8sClient)
	wsServer.RegisterHandler("logs", logsHandler)

	// Register dashboard handler
	dashboardHandler := handlers.NewDashboardHandler(k8sClient, wsServer.Hub())
	wsServer.RegisterHandler("dashboard", dashboardHandler)

	// Setup themes service
	homeDir, _ := os.UserHomeDir()
	themeDataDir := filepath.Join(homeDir, ".kanivet", "themes")
	themeStorage, err := themes.NewFileStorage(themeDataDir)
	if err != nil {
		faults.CaptureExceptionWithContext(
			fmt.Errorf("failed to initialize theme storage: %w", err),
			map[string]any{"phase": "themeStorage", "dir": themeDataDir},
		)
		log.Fatal("Failed to initialize theme storage:", err)
	}
	themeService := themes.NewService(themeStorage)
	themeHandler := themes.NewHandler(themeService)

	// Setup Helm service
	helmService := helm.NewService(k8sClient)
	helmHandler := helm.NewHandler(helmService)

	// Setup FinOps service
	finopsService := finops.NewService(k8sClient, cacheInstance.Cache)
	finopsHandler := finops.NewHandler(finopsService)

	// Setup Incident Timeline service (reuses existing event listener + db)
	incidentService := incidents.NewService(apiHandler.GetDB(), apiHandler.GetEventListener())
	incidentHandler := api.NewIncidentTimelineHandler(incidentService)

	// Register Helm streaming handler (after helmService is created)
	helmWsHandler := handlers.NewHelmHandler(helmService, wsServer.Hub())
	wsServer.RegisterHandler("helm", helmWsHandler)

	// Setup Cloud service for discovery
	cloudService := cloud.NewService(apiHandler.GetDB())
	cloudService.SetOnBatchComplete(func() {
		cacheInstance.Delete("clusters")
		k8sClient.RefreshClusterCache("")
	})

	// Register Cloud discovery streaming handler
	cloudDiscoveryHandler := handlers.NewCloudDiscoveryHandler(cloudService)
	wsServer.RegisterHandler("cloud.discover", cloudDiscoveryHandler)

	// Start terminal cleanup worker
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[PANIC] TerminalCleanupWorker: %v", r)
				faults.CaptureExceptionWithContext(
					fmt.Errorf("panic in TerminalCleanupWorker: %v", r),
					map[string]any{"panic": r, "stack": string(debug.Stack())},
				)
				panic(r) // Re-panic to crash
			}
		}()
		terminalService.StartCleanupWorker(context.Background())
	}()

	// Create the bridge to connect watcher to search
	searchService := apiHandler.GetSearchService()
	if searchService != nil {
		bridge := search.NewWatcherBridge(searchService, watcherService)
		searchBroadcaster.SetBridge(bridge)
		searchService.SetWatchedChecker(watcherService.IsWatching)

		// Update API handler with the bridge for managing watches
		apiHandler.SetWatcherBridge(bridge)

		// Trigger cheap schema indexing (API resource kinds only) when a new
		// cluster is watched. Resource data is populated incrementally by the
		// watcher's own List/Watch feeding through SearchBroadcaster, and the
		// 5-minute periodic reindexer fills gaps.
		watcherService.SetOnClusterWatched(func(cluster string) {
			log.Printf("[SEARCH] Cluster %s opened via watcher, indexing kinds...", cluster)
			if err := searchService.IndexClusterKindsOnly(cluster); err != nil {
				log.Printf("[SEARCH] Failed to index kinds for cluster %s: %v", cluster, err)
			}
		})

		// Note: We no longer start watchers for ALL resources after indexing.
		// This saves ~500MB-1GB per cluster by not maintaining 900+ watchers.
		// Real-time updates still work for resources users are actively viewing
		// (via WebSocket subscriptions). Search data refreshes every 5 minutes.
	}

	wsRoutes := router.Group("/api/v1/ws")
	{
		wsRoutes.GET("", func(c *gin.Context) { wsServer.ServeHTTP(c.Writer, c.Request) })
		wsRoutes.GET("/exec", apiHandler.HandleExecWebSocket)
		wsRoutes.GET("/node-exec", apiHandler.HandleNodeExecWebSocket)
	}

	pprofRoutes := router.Group("/debug/pprof")
	{
		pprofRoutes.GET("/", gin.WrapF(pprof.Index))
		pprofRoutes.GET("/cmdline", gin.WrapF(pprof.Cmdline))
		pprofRoutes.GET("/profile", gin.WrapF(pprof.Profile))
		pprofRoutes.POST("/symbol", gin.WrapF(pprof.Symbol))
		pprofRoutes.GET("/symbol", gin.WrapF(pprof.Symbol))
		pprofRoutes.GET("/trace", gin.WrapF(pprof.Trace))
		pprofRoutes.GET("/allocs", gin.WrapH(pprof.Handler("allocs")))
		pprofRoutes.GET("/block", gin.WrapH(pprof.Handler("block")))
		pprofRoutes.GET("/goroutine", gin.WrapH(pprof.Handler("goroutine")))
		pprofRoutes.GET("/heap", gin.WrapH(pprof.Handler("heap")))
		pprofRoutes.GET("/mutex", gin.WrapH(pprof.Handler("mutex")))
		pprofRoutes.GET("/threadcreate", gin.WrapH(pprof.Handler("threadcreate")))
		pprofRoutes.POST("/start-cpu-profile", func(c *gin.Context) {
			f, err := os.Create("/tmp/cpu.prof")
			if err != nil {
				c.JSON(500, gin.H{"error": err.Error()})
				return
			}
			if err := runtimepprof.StartCPUProfile(f); err != nil {
				f.Close()
				c.JSON(500, gin.H{"error": err.Error()})
				return
			}
			c.JSON(200, gin.H{"message": "CPU profiling started", "file": "/tmp/cpu.prof"})
		})
		pprofRoutes.POST("/stop-cpu-profile", func(c *gin.Context) {
			runtimepprof.StopCPUProfile()
			c.JSON(200, gin.H{"message": "CPU profiling stopped", "file": "/tmp/cpu.prof"})
		})
		pprofRoutes.POST("/heap-profile", func(c *gin.Context) {
			f, err := os.Create("/tmp/heap.prof")
			if err != nil {
				c.JSON(500, gin.H{"error": err.Error()})
				return
			}
			defer f.Close()
			runtime.GC()
			if err := runtimepprof.WriteHeapProfile(f); err != nil {
				c.JSON(500, gin.H{"error": err.Error()})
				return
			}
			c.JSON(200, gin.H{"message": "Heap profile written", "file": "/tmp/heap.prof"})
		})
	}

	v1 := router.Group("/api/v1")
	{
		v1.GET("/clusters", apiHandler.ListClusters)
		v1.POST("/clusters/refresh", apiHandler.RefreshClusters)
		v1.POST("/clusters/release", func(c *gin.Context) {
			var req struct {
				Cluster string `json:"cluster"`
			}
			if err := c.ShouldBindJSON(&req); err != nil || req.Cluster == "" {
				c.JSON(400, gin.H{"error": "cluster is required"})
				return
			}
			watcherService.StopAllForCluster(req.Cluster)
			if el := apiHandler.GetEventListener(); el != nil {
				el.StopListening(req.Cluster)
			}
			log.Printf("Released cluster %s: idle watches reaped, event stream stopped", req.Cluster)
			c.JSON(200, gin.H{"status": "released"})
		})
		v1.GET("/cluster/status", apiHandler.GetClusterStatusQuery)
		v1.POST("/clusters/status/batch", apiHandler.GetBatchClusterStatus)
		v1.GET("/cluster/dashboard", apiHandler.GetClusterDashboard)
		v1.GET("/cluster/dashboard/stream", apiHandler.StreamClusterDashboard)
		v1.GET("/cluster/categories", apiHandler.GetCategoriesQuery)
		v1.GET("/cluster/resources", apiHandler.ListResourcesQuery)
		v1.GET("/resources/categories", apiHandler.GetCategoriesQuery)
		v1.GET("/resources/list", apiHandler.ListResourcesQuery)
		v1.GET("/cluster/namespaces", apiHandler.ListNamespaces)
		v1.GET("/cluster/api-resources", apiHandler.ListAPIResources)
		v1.GET("/cluster/resource/:group/:version/:kind/:namespace/:name", apiHandler.GetResourceDetailsQuery)
		v1.GET("/cluster/resource-events/:group/:version/:kind/:namespace/:name", apiHandler.GetResourceEventsQuery)
		v1.POST("/cluster/pods/:namespace/:pod/portforward", apiHandler.CreatePortForward)
		v1.POST("/cluster/services/:namespace/:service/portforward", apiHandler.CreateServicePortForward)
		v1.DELETE("/cluster/portforward/*id", apiHandler.StopPortForward)
		v1.GET("/cluster/vclusters", apiHandler.ListVClusters)
		v1.POST("/cluster/vclusters/connect", apiHandler.ConnectVCluster)
		v1.DELETE("/cluster/vclusters/*id", apiHandler.DisconnectVCluster)
		v1.POST("/cluster/resources", apiHandler.CreateResource)
		v1.PUT("/cluster/resources", apiHandler.UpdateResource)
		v1.DELETE("/cluster/resources/:group/:version/:kind", apiHandler.DeleteResources)
		v1.POST("/cluster/resources/:group/:version/:kind/remove-finalizers", apiHandler.RemoveFinalizers)
		v1.POST("/cluster/resources/:group/:version/:kind/force-refresh", apiHandler.ForceRefreshResources)
		v1.POST("/cluster/resources/:group/:version/:kind/:namespace/:name/restart", apiHandler.RestartResource)
		v1.POST("/cluster/cronjobs/:namespace/:name/trigger", apiHandler.TriggerCronJob)
		v1.POST("/cluster/resources/bulk-restart", apiHandler.BulkRestartResources)
		v1.GET("/cluster/resources/:group/:version/:kind/:namespace/:name/rollout-status", apiHandler.GetRolloutStatus)
		v1.POST("/cluster/batch-rollout-status", apiHandler.GetBatchRolloutStatus)
		v1.POST("/cluster/resources/:group/:version/:kind/:namespace/:name/scale", apiHandler.ScaleResource)
		v1.POST("/cluster/nodes/:name/taint", apiHandler.TaintNode)
		v1.DELETE("/cluster/nodes/:name/taint", apiHandler.RemoveTaint)
		v1.POST("/cluster/nodes/:name/drain", apiHandler.DrainNode)
		v1.POST("/cluster/nodes/:name/cordon", apiHandler.CordonNode)
		v1.GET("/cluster/crossplane/check/:group/:version/:kind", apiHandler.CheckCrossplaneResource)
		v1.GET("/cluster/crossplane/trace/:group/:version/:kind/:namespace/:name", apiHandler.TraceCrossplaneResource)
		v1.GET("/cluster/argo/detect", apiHandler.GetArgoDetection)
		v1.GET("/cluster/argo/destinations", apiHandler.GetArgoDestinations)
		v1.GET("/cluster/argo/stats", apiHandler.GetArgoStats)
		v1.GET("/cluster/argo/applications-summary", apiHandler.GetArgoApplicationsSummary)
		v1.GET("/cluster/argo/applications/:namespace/:name/tree", apiHandler.GetArgoApplicationTree)
		v1.GET("/cluster/argo/applications/:namespace/:name/topology", apiHandler.GetArgoApplicationTopology)
		v1.GET("/cluster/argo/applications/:namespace/:name/managed-resources", apiHandler.GetArgoManagedResources)
		v1.POST("/cluster/argo/applications/:namespace/:name/sync", apiHandler.ArgoSyncApplication)
		v1.POST("/cluster/argo/applications/:namespace/:name/refresh", apiHandler.ArgoRefreshApplication)
		v1.POST("/cluster/argo/applications/:namespace/:name/rollback", apiHandler.ArgoRollbackApplication)
		v1.GET("/cluster/resource-schema/:group/:version/:kind", apiHandler.GetResourceSchema)

		// Cluster groups management
		v1.GET("/cluster-groups", apiHandler.ListClusterGroups)
		v1.POST("/cluster-groups", apiHandler.CreateClusterGroup)
		v1.PUT("/cluster-groups/order", apiHandler.UpdateGroupOrder)
		v1.PUT("/cluster-groups/:id", apiHandler.UpdateClusterGroup)
		v1.DELETE("/cluster-groups/:id", apiHandler.DeleteClusterGroup)
		v1.POST("/cluster-groups/assign", apiHandler.AssignClusterToGroup)
		v1.DELETE("/cluster-groups/assign/*clusterName", apiHandler.RemoveClusterFromGroup)
		v1.GET("/cluster-groups/clusters", apiHandler.GetClustersByGroup)
		v1.GET("/cluster-groups/assignments", apiHandler.GetClusterAssignments)

		// Cluster aliases
		v1.GET("/cluster-aliases", apiHandler.GetClusterAliases)
		v1.POST("/cluster-aliases", apiHandler.SetClusterAlias)
		v1.DELETE("/cluster-aliases/*clusterName", apiHandler.DeleteClusterAlias)

		// Search endpoints
		v1.GET("/search", apiHandler.Search)
		v1.GET("/search/recent", apiHandler.SearchRecentQueries)
		v1.POST("/search/recent", apiHandler.SaveSearchHistory)
		v1.POST("/search/index/cluster", apiHandler.IndexCluster)
		v1.GET("/search/index/status", apiHandler.GetSearchIndexingStatus)

		// ServiceAccount related endpoints
		v1.GET("/cluster/serviceaccount/:namespace/:name/associated", apiHandler.GetServiceAccountAssociatedResources)

		// Detail tab state endpoints
		v1.GET("/detail-tab-state", apiHandler.GetDetailTabState)
		v1.POST("/detail-tab-state", apiHandler.SetDetailTabState)

		// Metrics endpoints
		v1.GET("/metrics/providers", apiHandler.GetAvailableMetricProviders)
		v1.GET("/metrics/detect", apiHandler.DetectMetricsProvider)
		v1.POST("/metrics/install", apiHandler.InstallMetricsProvider)
		v1.POST("/metrics/pods/:namespace/:pod", apiHandler.QueryPodMetrics)
		v1.GET("/metrics/settings", apiHandler.GetMetricsSettings)
		v1.PUT("/metrics/settings", apiHandler.PutMetricsSettings)
		v1.GET("/metrics/tenants", apiHandler.DiscoverMetricsTenants)
		v1.GET("/metrics/mimir-services", apiHandler.ListMimirServices)

		// Workload endpoints
		v1.GET("/cluster/workloads/:kind/:namespace/:name/pods", apiHandler.GetWorkloadPods)

		// Navigation endpoints
		v1.POST("/navigation/add/*tabId", apiHandler.AddNavigationEntry)
		v1.GET("/navigation/history/*tabId", apiHandler.GetNavigationHistory)
		v1.POST("/navigation/back/*tabId", apiHandler.NavigateBack)
		v1.POST("/navigation/forward/*tabId", apiHandler.NavigateForward)
		v1.DELETE("/navigation/clear/*tabId", apiHandler.ClearNavigationHistory)

		// Cache endpoints
		v1.POST("/cache/invalidate/api-resources", apiHandler.InvalidateAPIResourcesCache)

		// Theme endpoints
		v1.GET("/themes", themeHandler.ListThemes)
		v1.GET("/themes/:id", themeHandler.GetTheme)
		v1.DELETE("/themes/:id", themeHandler.DeleteTheme)
		v1.POST("/themes/apply", themeHandler.ApplyTheme)
		v1.GET("/themes/current", themeHandler.GetCurrentSettings)
		v1.GET("/themes/marketplace", themeHandler.SearchMarketplaceThemes)
		v1.POST("/themes/download", themeHandler.DownloadTheme)

		// Helm endpoints
		v1.GET("/helm/releases", helmHandler.ListReleases)
		v1.GET("/helm/releases/:namespace/:name", helmHandler.GetRelease)
		v1.GET("/helm/releases/:namespace/:name/values", helmHandler.GetReleaseValues)
		v1.GET("/helm/releases/:namespace/:name/manifest", helmHandler.GetReleaseManifest)
		v1.GET("/helm/releases/:namespace/:name/history", helmHandler.GetReleaseHistory)
		v1.POST("/helm/releases/:namespace/:name/rollback", helmHandler.RollbackRelease)
		v1.POST("/helm/releases/:namespace/:name/upgrade", helmHandler.UpgradeReleaseValues)
		v1.DELETE("/helm/releases/:namespace/:name", helmHandler.UninstallRelease)

		finops := v1.Group("/finops")
		finops.GET("/dashboard", finopsHandler.GetDashboard)
		finops.GET("/dashboard/stream", finopsHandler.StreamDashboardEndpoint)
		finops.GET("/summary", finopsHandler.GetClusterCostSummary)
		finops.GET("/nodes", finopsHandler.GetNodeCosts)
		finops.GET("/pods", finopsHandler.GetPodCosts)
		finops.GET("/namespaces", finopsHandler.GetNamespaceCosts)
		finops.GET("/workloads", finopsHandler.GetWorkloadCosts)
		finops.GET("/recommendations", finopsHandler.GetCostRecommendations)
		finops.GET("/resource/:kind/:namespace/:name", finopsHandler.GetResourceCost)
		finops.GET("/pricing-status", finopsHandler.GetPricingStatus)
		finops.GET("/pricing-debug", finopsHandler.GetPricingDebug)
		finops.POST("/preload-pricing", finopsHandler.PreloadPricing)

		// Incident Timeline endpoint
		v1.GET("/incidents/timeline", incidentHandler.GetTimeline)

		cloudService := cloud.NewService(apiHandler.GetDB())
		refreshClustersAndBroadcast := func(reason string) {
			cacheInstance.Delete("clusters")
			k8sClient.RefreshClusterCache("")
			clusters, err := k8sClient.ListClusters()
			if err != nil {
				log.Printf("Failed to refresh cluster inventory after %s: %v", reason, err)
				return
			}
			cacheInstance.Set("clusters", clusters, 1*time.Minute)
			apiHandler.BroadcastClustersRefreshed(clusters, reason)
		}
		invalidateClusterCache := func() {
			refreshClustersAndBroadcast("cloud_import")
		}
		cloudService.SetOnBatchComplete(invalidateClusterCache)
		cloudHandler := cloud.NewHandler(cloudService)
		cloudHandler.SetOnClusterImported(invalidateClusterCache)
		cloudHandler.RegisterRoutes(v1)

		configWatcher := cloud.NewConfigWatcher(cloud.DefaultConfigWatchPaths(), func(reason string) {
			refreshClustersAndBroadcast("external_config_change:" + reason)
		})
		go configWatcher.Start(appCtx)

	}

	listener, err := listenOnConfiguredAddr()
	if err != nil {
		log.Printf("Failed to bind server: %v", err)
		faults.CaptureException(err)
		return
	}
	if tcpAddr, ok := listener.Addr().(*net.TCPAddr); ok {
		fmt.Printf("KANIVET_PORT=%d\n", tcpAddr.Port)
	}

	srv := &http.Server{Handler: router}

	// Wait for interrupt signal or stdin close to gracefully shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// Start server in a goroutine
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[PANIC] HTTP Server: %v", r)
				faults.CaptureExceptionWithContext(
					fmt.Errorf("panic in HTTP server: %v", r),
					map[string]any{"panic": r, "stack": string(debug.Stack())},
				)
				panic(r) // Re-panic to crash
			}
		}()
		log.Printf("Backend server starting on %s", listener.Addr().String())
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("Failed to start server: %v", err)
			faults.CaptureException(err)
			quit <- syscall.SIGTERM
		}
	}()

	// Monitor stdin only when spawned by Electron (KANIVET_SESSION_SECRET is set)
	if os.Getenv("KANIVET_SESSION_SECRET") != "" {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[PANIC] Stdin monitor: %v", r)
					faults.CaptureExceptionWithContext(
						fmt.Errorf("panic in stdin monitor: %v", r),
						map[string]any{"panic": r, "stack": string(debug.Stack())},
					)
					panic(r) // Re-panic to crash
				}
			}()
			buf := make([]byte, 1)
			for {
				_, err := os.Stdin.Read(buf)
				if err != nil {
					log.Println("Stdin closed, parent process died")
					quit <- syscall.SIGTERM
					return
				}
			}
		}()
	}

	<-quit
	log.Println("Shutting down server...")

	// Create shutdown context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	statusManager.Stop()
	k8sClient.ShutdownVClusters()

	// Shutdown watcher service first (cancels watches and persists list
	// snapshots) — it needs the DB that apiHandler.Shutdown closes.
	watcherService.Shutdown()

	// Shutdown handler components (event listeners, DB, etc.)
	apiHandler.Shutdown()

	// Shutdown websocket server
	if err := wsServer.Shutdown(ctx); err != nil {
		log.Printf("WebSocket server shutdown error: %v", err)
	}

	// Shutdown HTTP server
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("Server shutdown error: %v", err)
	}

	log.Println("Server exited")
}
