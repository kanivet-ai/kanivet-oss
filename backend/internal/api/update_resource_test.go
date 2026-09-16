package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kanivet/backend/internal/cache"
	"github.com/kanivet/backend/internal/k8s"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// goneClient simulates an apiserver where the edited object has been deleted.
// Any Create call is a test failure: the endpoint must never recreate it.
type goneClient struct {
	*k8s.MockClient
	t *testing.T
}

func (g *goneClient) UpdateResource(ctx context.Context, cluster string, gvr schema.GroupVersionResource, namespace, name string, obj *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	return nil, fmt.Errorf("%w: %s %q", k8s.ErrResourceGone, gvr.Resource, name)
}

func (g *goneClient) CreateResource(ctx context.Context, cluster string, gvr schema.GroupVersionResource, namespace, name string, obj *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	g.t.Fatalf("UpdateResource endpoint must not fall back to Create (got create for %s/%s)", namespace, name)
	return nil, nil
}

func TestUpdateResourceReturns404WhenObjectIsGone(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{
		k8s:   &goneClient{MockClient: &k8s.MockClient{}, t: t},
		cache: cache.New(time.Minute, time.Minute),
	}

	body := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cfg\n  namespace: ns\ndata:\n  key: stale\n"
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/cluster/resources?cluster=c1", strings.NewReader(body))

	h.UpdateResource(c)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404 for a vanished object, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "was not recreated") {
		t.Fatalf("error body should tell the user nothing was recreated, got: %s", rec.Body.String())
	}
}
