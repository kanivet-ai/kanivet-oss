package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kanivet/backend/internal/cache"
	"github.com/kanivet/backend/internal/k8s"
)

type cachedStatusManager struct{ statuses map[string]*k8s.ClusterStatus }

func (m cachedStatusManager) GetStatus(cluster string) *k8s.ClusterStatus   { return m.statuses[cluster] }
func (m cachedStatusManager) GetAllStatuses() map[string]*k8s.ClusterStatus { return m.statuses }
func (m cachedStatusManager) RefreshCluster(string) *k8s.ClusterStatus      { return nil }

func TestGetBatchClusterStatusCachedOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{cache: cache.New(time.Minute, time.Minute), statusManager: cachedStatusManager{statuses: map[string]*k8s.ClusterStatus{"ready": {Name: "ready", Healthy: true}}}}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/clusters/status/batch", bytes.NewBufferString(`{"clusters":["ready","missing"],"cachedOnly":true}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.GetBatchClusterStatus(c)

	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`"ready"`)) || bytes.Contains(w.Body.Bytes(), []byte(`"missing"`)) {
		t.Fatalf("cached-only response = %d %s", w.Code, w.Body.String())
	}
}

func TestGetBatchClusterStatusCachedOnlyWithoutStatusManager(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{cache: cache.New(time.Minute, time.Minute)}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/clusters/status/batch", bytes.NewBufferString(`{"clusters":["missing"],"cachedOnly":true}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.GetBatchClusterStatus(c)

	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`"statuses":{}`)) {
		t.Fatalf("cached-only response = %d %s", w.Code, w.Body.String())
	}
}

func TestGetBatchClusterStatusCachedOnlyOverridesForceAndDoesNotProbe(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{
		cache:         cache.New(time.Minute, time.Minute),
		statusManager: cachedStatusManager{statuses: map[string]*k8s.ClusterStatus{"ready": {Name: "ready", Healthy: true}}},
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/clusters/status/batch", bytes.NewBufferString(`{"clusters":["ready"],"force":true,"cachedOnly":true}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.GetBatchClusterStatus(c)

	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`"ready"`)) {
		t.Fatalf("cached-only response = %d %s", w.Code, w.Body.String())
	}
}

func TestGetBatchClusterStatusCachedOnlyDoesNotLimitClusters(t *testing.T) {
	gin.SetMode(gin.TestMode)
	statuses := make(map[string]*k8s.ClusterStatus, 201)
	clusters := make([]string, 201)
	for i := range clusters {
		name := fmt.Sprintf("cluster-%d", i)
		clusters[i] = name
		statuses[name] = &k8s.ClusterStatus{Name: name, Healthy: true}
	}
	h := &Handler{cache: cache.New(time.Minute, time.Minute), statusManager: cachedStatusManager{statuses: statuses}}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/clusters/status/batch", bytes.NewBufferString(fmt.Sprintf(`{"clusters":%s,"cachedOnly":true}`, mustJSON(t, clusters))))
	c.Request.Header.Set("Content-Type", "application/json")

	h.GetBatchClusterStatus(c)

	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`"cluster-200"`)) {
		t.Fatalf("cached-only response = %d %s", w.Code, w.Body.String())
	}
}

func mustJSON(t *testing.T, value interface{}) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
