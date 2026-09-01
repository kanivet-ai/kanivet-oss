package api

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/bytedance/sonic"
	"github.com/gin-gonic/gin"
	"github.com/kanivet/backend/internal/cache"
	"github.com/kanivet/backend/internal/db"
	"github.com/kanivet/backend/internal/events"
	"github.com/kanivet/backend/internal/faults"
	"github.com/kanivet/backend/internal/k8s"
	"github.com/kanivet/backend/internal/metrics"
	"github.com/kanivet/backend/internal/models"
	"github.com/kanivet/backend/internal/search"
	"github.com/kanivet/backend/internal/topics"
	"github.com/kanivet/backend/internal/websocket/core"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var categoryMap = map[string][]models.Resource{
	"workloads": {{Name: "pods", Group: "", Version: "v1", Kind: "Pod", Namespaced: true},
		{Name: "deployments", Group: "apps", Version: "v1", Kind: "Deployment", Namespaced: true},
		{Name: "statefulsets", Group: "apps", Version: "v1", Kind: "StatefulSet", Namespaced: true},
		{Name: "daemonsets", Group: "apps", Version: "v1", Kind: "DaemonSet", Namespaced: true},
		{Name: "jobs", Group: "batch", Version: "v1", Kind: "Job", Namespaced: true},
		{Name: "cronjobs", Group: "batch", Version: "v1", Kind: "CronJob", Namespaced: true}},
	"networking": {{Name: "services", Group: "", Version: "v1", Kind: "Service", Namespaced: true},
		{Name: "endpoints", Group: "", Version: "v1", Kind: "Endpoints", Namespaced: true},
		{Name: "ingresses", Group: "networking.k8s.io", Version: "v1", Kind: "Ingress", Namespaced: true},
		{Name: "ingressclasses", Group: "networking.k8s.io", Version: "v1", Kind: "IngressClass", Namespaced: false},
		{Name: "gatewayclasses", Group: "gateway.networking.k8s.io", Version: "v1", Kind: "GatewayClass", Namespaced: false},
		{Name: "gateways", Group: "gateway.networking.k8s.io", Version: "v1", Kind: "Gateway", Namespaced: true},
		{Name: "httproutes", Group: "gateway.networking.k8s.io", Version: "v1", Kind: "HTTPRoute", Namespaced: true},
		{Name: "grpcroutes", Group: "gateway.networking.k8s.io", Version: "v1", Kind: "GRPCRoute", Namespaced: true},
		{Name: "networkpolicies", Group: "networking.k8s.io", Version: "v1", Kind: "NetworkPolicy", Namespaced: true},
		{Name: "endpointslices", Group: "discovery.k8s.io", Version: "v1", Kind: "EndpointSlice", Namespaced: true}},
	"config": {{Name: "configmaps", Group: "", Version: "v1", Kind: "ConfigMap", Namespaced: true},
		{Name: "secrets", Group: "", Version: "v1", Kind: "Secret", Namespaced: true},
		{Name: "serviceaccounts", Group: "", Version: "v1", Kind: "ServiceAccount", Namespaced: true},
		{Name: "limitranges", Group: "", Version: "v1", Kind: "LimitRange", Namespaced: true},
		{Name: "resourcequotas", Group: "", Version: "v1", Kind: "ResourceQuota", Namespaced: true},
		{Name: "horizontalpodautoscalers", Group: "autoscaling", Version: "v2", Kind: "HorizontalPodAutoscaler", Namespaced: true}},
	"storage": {{Name: "persistentvolumes", Group: "", Version: "v1", Kind: "PersistentVolume", Namespaced: false},
		{Name: "persistentvolumeclaims", Group: "", Version: "v1", Kind: "PersistentVolumeClaim", Namespaced: true},
		{Name: "storageclasses", Group: "storage.k8s.io", Version: "v1", Kind: "StorageClass", Namespaced: false},
		{Name: "volumeattachments", Group: "storage.k8s.io", Version: "v1", Kind: "VolumeAttachment", Namespaced: false}},
	"rbac": {{Name: "roles", Group: "rbac.authorization.k8s.io", Version: "v1", Kind: "Role", Namespaced: true},
		{Name: "rolebindings", Group: "rbac.authorization.k8s.io", Version: "v1", Kind: "RoleBinding", Namespaced: true},
		{Name: "clusterroles", Group: "rbac.authorization.k8s.io", Version: "v1", Kind: "ClusterRole", Namespaced: false},
		{Name: "clusterrolebindings", Group: "rbac.authorization.k8s.io", Version: "v1", Kind: "ClusterRoleBinding", Namespaced: false}},
	"cluster": {{Name: "namespaces", Group: "", Version: "v1", Kind: "Namespace", Namespaced: false},
		{Name: "nodes", Group: "", Version: "v1", Kind: "Node", Namespaced: false},
		{Name: "priorityclasses", Group: "scheduling.k8s.io", Version: "v1", Kind: "PriorityClass", Namespaced: false},
		{Name: "runtimeclasses", Group: "node.k8s.io", Version: "v1", Kind: "RuntimeClass", Namespaced: false},
		{Name: "leases", Group: "coordination.k8s.io", Version: "v1", Kind: "Lease", Namespaced: true},
		{Name: "certificatesigningrequests", Group: "certificates.k8s.io", Version: "v1", Kind: "CertificateSigningRequest", Namespaced: false},
		{Name: "apiservices", Group: "apiregistration.k8s.io", Version: "v1", Kind: "APIService", Namespaced: false},
		{Name: "mutatingwebhookconfigurations", Group: "admissionregistration.k8s.io", Version: "v1", Kind: "MutatingWebhookConfiguration", Namespaced: false},
		{Name: "validatingwebhookconfigurations", Group: "admissionregistration.k8s.io", Version: "v1", Kind: "ValidatingWebhookConfiguration", Namespaced: false},
		{Name: "poddisruptionbudgets", Group: "policy", Version: "v1", Kind: "PodDisruptionBudget", Namespaced: true}},
}

var builtinResources = map[string]string{
	"pods": "", "services": "", "endpoints": "", "namespaces": "", "nodes": "",
	"configmaps": "", "secrets": "", "serviceaccounts": "",
	"persistentvolumes": "", "persistentvolumeclaims": "", "deployments": "apps",
	"statefulsets": "apps", "daemonsets": "apps", "jobs": "batch",
	"cronjobs": "batch", "ingresses": "networking.k8s.io", "ingressclasses": "networking.k8s.io",
	"networkpolicies": "networking.k8s.io",
	"gatewayclasses":  "gateway.networking.k8s.io", "gateways": "gateway.networking.k8s.io",
	"httproutes": "gateway.networking.k8s.io", "grpcroutes": "gateway.networking.k8s.io",
	"roles": "rbac.authorization.k8s.io", "rolebindings": "rbac.authorization.k8s.io",
	"clusterroles": "rbac.authorization.k8s.io", "clusterrolebindings": "rbac.authorization.k8s.io",
	"storageclasses": "storage.k8s.io", "volumeattachments": "storage.k8s.io",
	"priorityclasses": "scheduling.k8s.io", "runtimeclasses": "node.k8s.io",
	"limitranges": "", "resourcequotas": "",
	"horizontalpodautoscalers": "autoscaling", "poddisruptionbudgets": "policy", "endpointslices": "discovery.k8s.io",
	"leases": "coordination.k8s.io", "certificatesigningrequests": "certificates.k8s.io",
	"apiservices":                     "apiregistration.k8s.io",
	"mutatingwebhookconfigurations":   "admissionregistration.k8s.io",
	"validatingwebhookconfigurations": "admissionregistration.k8s.io",
}

func isBuiltinResource(name, group string) bool {
	expectedGroup, exists := builtinResources[name]
	if !exists {
		return false
	}
	return group == expectedGroup
}

const maxConcurrentResourceCounts = 10

var categories = []models.Category{
	{Name: "Workloads", ID: "workloads"},
	{Name: "Networking", ID: "networking"},
	{Name: "Configuration", ID: "config"},
	{Name: "Storage", ID: "storage"},
	{Name: "RBAC", ID: "rbac"},
	{Name: "Cluster", ID: "cluster"},
	{Name: "Crossplane", ID: "crossplane"},
	{Name: "Argo CD", ID: "argocd"},
	{Name: "Custom Resources", ID: "custom"},
}

type Handler struct {
	k8s             k8s.Interface
	cache           *cache.Cache
	invalidationBus *cache.InvalidationBus
	db              *db.DB
	search          *search.Service
	metrics         *metrics.Service
	detailTabs      *DetailTabManager
	wsHub           *core.Hub
	watcherBridge   *search.WatcherBridge
	eventListener   *events.EventListener
	navigation      *NavigationService
	statusManager   StatusManagerInterface
}

type StatusManagerInterface interface {
	GetStatus(cluster string) *k8s.ClusterStatus
	GetAllStatuses() map[string]*k8s.ClusterStatus
	RefreshCluster(cluster string) *k8s.ClusterStatus
}

type CountUpdateMessage struct {
	core.BaseMessage `json:",inline"`
	Channel          string `json:"channel"`
	Topic            string `json:"topic"`
	Group            string `json:"group"`
	Resource         string `json:"resource"`
	Count            int    `json:"count"`
}

type ClusterErrorMessage struct {
	Type         string `json:"type"`
	Cluster      string `json:"cluster"`
	ErrorCode    string `json:"errorCode"`
	ErrorMessage string `json:"errorMessage"`
	Details      string `json:"details,omitempty"`
	Recoverable  bool   `json:"recoverable"`
}

type ClustersRefreshedMessage struct {
	Type     string            `json:"type"`
	Reason   string            `json:"reason"`
	Clusters []k8s.ClusterInfo `json:"clusters"`
}

func (m *CountUpdateMessage) Marshal() ([]byte, error) {
	return core.MarshalMessage(m)
}

type clusterStatus = k8s.ClusterStatus
type k8sRolloutStatusRequest = k8s.RolloutStatusRequest
type corev1Pod = v1.Pod

var corev1ResourceCPU = v1.ResourceCPU
var corev1ResourceMemory = v1.ResourceMemory

func NewHandlerWithDeps(k8sClient k8s.Interface, cacheInstance *cache.Cache, invalidationBus *cache.InvalidationBus) *Handler {
	database, err := db.New()
	if err != nil {
		log.Printf("CRITICAL ERROR: Failed to initialize database: %v", err)
		faults.CaptureExceptionWithContext(err, map[string]any{
			"component": "database_init",
			"stage":     "handler_creation",
		})
		panic(fmt.Sprintf("Failed to initialize database: %v", err))
	}
	log.Printf("Database initialized successfully")

	concreteK8sClient, ok := k8sClient.(*k8s.Client)
	if !ok {
		initErr := fmt.Errorf("search service requires concrete k8s.Client, got %T", k8sClient)
		faults.CaptureException(initErr)
		panic(initErr.Error())
	}
	eventListener := events.NewEventListener(k8sClient, database)
	metricsService := metrics.NewService(k8sClient, cacheInstance, invalidationBus)

	return &Handler{
		k8s:             k8sClient,
		cache:           cacheInstance,
		invalidationBus: invalidationBus,
		db:              database,
		detailTabs:      NewDetailTabManager(),
		search:          search.NewService(concreteK8sClient, cacheInstance, database, invalidationBus),
		metrics:         metricsService,
		eventListener:   eventListener,
		navigation:      NewNavigationService(),
	}
}

func (h *Handler) AttachHub(hub *core.Hub) {
	h.wsHub = hub
}

func (h *Handler) broadcastClusterError(cluster, errorMsg string) {
	if h.wsHub == nil {
		log.Printf("Cannot broadcast cluster error: wsHub is nil")
		return
	}
	log.Printf("broadcastClusterError called for %s: %s", cluster, errorMsg)
	errorCode, errorMessage, ok := k8s.ClassifyClusterError(errorMsg)
	if !ok {
		errorCode = "cluster_error"
		errorMessage = errorMsg
	}
	msg := &ClusterErrorMessage{
		Type:         "cluster_error",
		Cluster:      cluster,
		ErrorCode:    errorCode,
		ErrorMessage: errorMessage,
		Details:      errorMsg,
		Recoverable:  true,
	}
	data, err := sonic.Marshal(msg)
	if err != nil {
		log.Printf("Failed to marshal cluster error: %v", err)
		return
	}
	h.wsHub.RangeConnections(func(conn *core.Connection) bool {
		_ = conn.Send(data)
		return true
	})
	log.Printf("Broadcast cluster error for %s: %s - %s", cluster, errorCode, errorMessage)
}

func (h *Handler) BroadcastClustersRefreshed(clusters []k8s.ClusterInfo, reason string) {
	if h.wsHub == nil {
		log.Printf("Cannot broadcast clusters refreshed event: wsHub is nil")
		return
	}

	msg := &ClustersRefreshedMessage{
		Type:     "clusters_refreshed",
		Reason:   reason,
		Clusters: clusters,
	}
	data, err := sonic.Marshal(msg)
	if err != nil {
		log.Printf("Failed to marshal clusters refreshed event: %v", err)
		return
	}
	h.wsHub.RangeConnections(func(conn *core.Connection) bool {
		_ = conn.Send(data)
		return true
	})
	log.Printf("Broadcast clusters refreshed event for %d clusters (%s)", len(clusters), reason)
}

func (h *Handler) GetDB() *db.DB                           { return h.db }
func (h *Handler) GetSearchService() *search.Service       { return h.search }
func (h *Handler) GetMetricsService() *metrics.Service     { return h.metrics }
func (h *Handler) GetEventListener() *events.EventListener { return h.eventListener }

func (h *Handler) SetWatcherBridge(bridge *search.WatcherBridge) {
	h.watcherBridge = bridge
}

func (h *Handler) SetStatusManager(sm StatusManagerInterface) {
	h.statusManager = sm
}

func (h *Handler) Shutdown() {
	log.Println("Shutting down handler components...")
	if h.eventListener != nil {
		h.eventListener.StopAll()
	}
	if h.db != nil {
		if sqlDB, err := h.db.DB.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}
	log.Println("Handler shutdown complete")
}

func (h *Handler) respond(c *gin.Context, code int, data interface{}, err error) {
	if err != nil {
		if code >= 500 {
			faults.CaptureExceptionWithContext(err, map[string]any{
				"path":   c.Request.URL.Path,
				"method": c.Request.Method,
				"status": code,
			})
		}
		c.JSON(code, gin.H{"error": err.Error()})
		return
	}
	c.JSON(code, data)
}

func (h *Handler) requireCluster(c *gin.Context) (string, bool) {
	cluster := c.Query("cluster")
	if cluster == "" {
		h.respond(c, http.StatusBadRequest, nil, fmt.Errorf("cluster parameter is required"))
		return "", false
	}
	return cluster, true
}

func (h *Handler) cleanVerboseFields(resource *unstructured.Unstructured) {
	if resource == nil {
		return
	}
	h.cleanMetadataFields(resource.Object)
}

func (h *Handler) cleanMetadataFields(obj map[string]interface{}) {
	if obj == nil {
		return
	}
	if metadata, ok := obj["metadata"].(map[string]interface{}); ok {
		delete(metadata, "managedFields")
		if annotations, ok := metadata["annotations"].(map[string]interface{}); ok {
			delete(annotations, "kubectl.kubernetes.io/last-applied-configuration")
			if len(annotations) == 0 {
				delete(metadata, "annotations")
			}
		}
	}
	for _, value := range obj {
		switch v := value.(type) {
		case map[string]interface{}:
			h.cleanMetadataFields(v)
		case []interface{}:
			for _, item := range v {
				if itemMap, ok := item.(map[string]interface{}); ok {
					h.cleanMetadataFields(itemMap)
				}
			}
		}
	}
}

func (h *Handler) parallelResourceCount(ctx context.Context, cluster string, resources []models.Resource) {
	if len(resources) == 0 {
		return
	}
	type countResult struct {
		index int
		count int
		err   error
	}
	resultChan := make(chan countResult, len(resources))
	sem := make(chan struct{}, maxConcurrentResourceCounts)

	for i := range resources {
		i := i
		sem <- struct{}{}
		go func() {
			defer func() { <-sem }()
			gvr := schema.GroupVersionResource{
				Group:    resources[i].Group,
				Version:  resources[i].Version,
				Resource: resources[i].Name,
			}
			count, err := h.k8s.GetResourceCount(ctx, cluster, gvr)
			if err != nil {
				log.Printf("Error getting count for resource %s: %v", resources[i].Name, err)
			}
			resultChan <- countResult{index: i, count: count, err: err}
		}()
	}

	for i := 0; i < len(resources); i++ {
		result := <-resultChan
		if result.err != nil {
			zero := 0
			resources[result.index].Count = &zero
		} else {
			c := result.count
			resources[result.index].Count = &c
		}
	}
	close(resultChan)
}

func (h *Handler) publishCrossplaneCounts(cluster string, resources []models.Resource) {
	if h.wsHub == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	countsTopic := topics.BuildCountsTopic(cluster)

	sem := make(chan struct{}, maxConcurrentResourceCounts)
	var wg sync.WaitGroup

	for _, r := range resources {
		wg.Add(1)
		sem <- struct{}{}
		go func(res models.Resource) {
			defer func() {
				<-sem
				wg.Done()
			}()
			gvr := schema.GroupVersionResource{
				Group:    res.Group,
				Version:  res.Version,
				Resource: res.Name,
			}
			count, err := h.k8s.GetResourceCount(ctx, cluster, gvr)
			if err != nil {
				log.Printf("Error getting count for Crossplane resource %s: %v", res.Name, err)
				count = 0
			}
			msg := &CountUpdateMessage{
				BaseMessage: core.BaseMessage{
					MessageType: "count",
					Timestamp:   time.Now(),
				},
				Channel:  "counts",
				Topic:    countsTopic,
				Group:    res.Group,
				Resource: res.Name,
				Count:    count,
			}
			if err := h.wsHub.Broadcast(countsTopic, msg); err != nil {
				log.Printf("Error broadcasting count for %s: %v", res.Name, err)
			}
		}(r)
	}
	wg.Wait()
}

func getPodReadyStatus(pod v1.Pod) bool {
	for _, cond := range pod.Status.Conditions {
		if cond.Type == v1.PodReady && cond.Status == v1.ConditionTrue {
			return true
		}
	}
	return false
}

func getPodRestartCount(pod v1.Pod) int32 {
	var count int32
	for _, cs := range pod.Status.ContainerStatuses {
		count += cs.RestartCount
	}
	return count
}
