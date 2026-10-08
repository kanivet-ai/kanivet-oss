package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kanivet/backend/internal/cache"
	"github.com/kanivet/backend/internal/k8s"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

var deploymentsGVR = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}

// discoveryClient resolves Kinds to resource names the way discovery does and
// writes updates through its dynamic client.
type discoveryClient struct {
	*k8s.MockClient
}

func (d *discoveryClient) GetResourceName(cluster, group, version, kind string) string {
	if kind == "Deployment" {
		return "deployments"
	}
	return kind
}

func (d *discoveryClient) UpdateResource(ctx context.Context, cluster string, gvr schema.GroupVersionResource, namespace, name string, obj *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	return d.DynamicClient.Resource(gvr).Namespace(namespace).Update(ctx, obj, metav1.UpdateOptions{})
}

func deployment(replicas int64) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata":   map[string]interface{}{"name": "d1", "namespace": "ns"},
		"spec":       map[string]interface{}{"replicas": replicas},
	}}
}

type detailFixture struct {
	h   *Handler
	bus *cache.InvalidationBus
	dyn *dynamicfake.FakeDynamicClient
}

func newDetailFixture(t *testing.T) *detailFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	bus := cache.NewInvalidationBus()
	c := cache.NewCacheWithInvalidation(time.Minute, 0, bus)
	dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), deployment(1))
	return &detailFixture{
		h:   &Handler{k8s: &discoveryClient{MockClient: &k8s.MockClient{DynamicClient: dyn}}, cache: c.Cache, invalidationBus: bus},
		bus: bus,
		dyn: dyn,
	}
}

// replicas loads the deployment through the detail endpoint, as the detail
// view and the YAML editor do: by Kind.
func (f *detailFixture) replicas(t *testing.T) int64 {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/cluster/resource/apps/v1/Deployment/ns/d1?cluster=c1", nil)
	c.Params = gin.Params{{Key: "group", Value: "apps"}, {Key: "version", Value: "v1"}, {Key: "kind", Value: "Deployment"}, {Key: "namespace", Value: "ns"}, {Key: "name", Value: "d1"}}
	f.h.GetResourceDetailsQuery(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("detail GET: %d %s", rec.Code, rec.Body.String())
	}
	var obj struct {
		Spec struct {
			Replicas int64 `json:"replicas"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &obj); err != nil {
		t.Fatal(err)
	}
	return obj.Spec.Replicas
}

// The watcher evicts a changed object's detail by its resource name, the
// only name it has; the detail view's cached copy must be the one it evicts.
func TestWatchEventEvictsCachedDetail(t *testing.T) {
	f := newDetailFixture(t)
	if got := f.replicas(t); got != 1 {
		t.Fatalf("replicas = %d, want 1", got)
	}
	if _, err := f.dyn.Resource(deploymentsGVR).Namespace("ns").Update(context.Background(), deployment(2), metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}

	// What watcher.emitResourceEvent invalidates for a MODIFIED deployment.
	f.bus.InvalidatePattern("detail:" + strings.Join([]string{"c1", "apps", "v1", "deployments", "ns", "d1"}, ":"))

	if got := f.replicas(t); got != 2 {
		t.Fatalf("detail still serves the pre-change object: replicas = %d, want 2", got)
	}
}

// Reopening the YAML editor right after a save must load the saved object,
// or its stale resourceVersion makes the next save fail with a conflict.
func TestYamlSaveEvictsCachedDetail(t *testing.T) {
	f := newDetailFixture(t)
	if got := f.replicas(t); got != 1 {
		t.Fatalf("replicas = %d, want 1", got)
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: d1\n  namespace: ns\nspec:\n  replicas: 3\n"
	c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/cluster/resources?cluster=c1", strings.NewReader(body))
	f.h.UpdateResource(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rec.Code, rec.Body.String())
	}

	if got := f.replicas(t); got != 3 {
		t.Fatalf("editor reloads the pre-save object: replicas = %d, want 3", got)
	}
}
