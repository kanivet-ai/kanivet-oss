package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kanivet/backend/internal/k8s"
)

type hostPodResolver struct {
	k8s.MockClient
	hostPod *k8s.VClusterHostPod
	err     error
}

func (r *hostPodResolver) ResolveVClusterHostPod(string, string, string) (*k8s.VClusterHostPod, error) {
	return r.hostPod, r.err
}

func getVClusterHostPod(h *Handler, query string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/cluster/vclusters/host-pod?"+query, nil)
	h.GetVClusterHostPod(c)
	return w
}

func TestGetVClusterHostPod(t *testing.T) {
	resolver := &hostPodResolver{hostPod: &k8s.VClusterHostPod{Host: "prod", Namespace: "vc-ns", Name: "app-x-work-x-vc1", NodeName: "ip-10-0-1-5"}}
	w := getVClusterHostPod(&Handler{k8s: resolver}, "cluster=vcluster:prod:vc-ns:vc1&namespace=work&name=app")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"nodeName":"ip-10-0-1-5"`) {
		t.Fatalf("response = %d %s", w.Code, w.Body.String())
	}
}

func TestGetVClusterHostPodRejectsNonVCluster(t *testing.T) {
	w := getVClusterHostPod(&Handler{k8s: &hostPodResolver{}}, "cluster=prod&namespace=work&name=app")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("response = %d %s", w.Code, w.Body.String())
	}
}

func TestGetVClusterHostPodNotFound(t *testing.T) {
	resolver := &hostPodResolver{err: fmt.Errorf("%w: work/app", k8s.ErrVClusterHostPodNotFound)}
	w := getVClusterHostPod(&Handler{k8s: resolver}, "cluster=vcluster:prod:vc-ns:vc1&namespace=work&name=app")
	if w.Code != http.StatusNotFound {
		t.Fatalf("response = %d %s", w.Code, w.Body.String())
	}
}
