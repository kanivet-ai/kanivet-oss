package cloud

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestClusterSSOBindingEndpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s, _ := newBindingTestService(t)
	router := gin.New()
	NewHandler(s).RegisterRoutes(router.Group("/api/v1"))
	do := func(method, target, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	rec := do(http.MethodPut, "/api/v1/cloud/cluster-auth/sso", `{"cluster":"`+bindingTestCluster+`","startUrl":"`+bindingTestStartURL+`","accountId":"243517631187","roleName":"AdministratorAccess"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d %s", rec.Code, rec.Body)
	}
	var bound ClusterSSOBinding
	if err := json.Unmarshal(rec.Body.Bytes(), &bound); err != nil || bound.Profile != "sandbox-admin" {
		t.Fatalf("PUT body = %s (err %v)", rec.Body, err)
	}

	rec = do(http.MethodPut, "/api/v1/cloud/cluster-auth/sso", `{"cluster":"`+bindingTestCluster+`","startUrl":"`+bindingTestStartURL+`","accountId":"585768152950","roleName":"AdministratorAccess"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("PUT for the wrong account = %d, want 400", rec.Code)
	}

	rec = do(http.MethodDelete, "/api/v1/cloud/cluster-auth/sso?cluster="+url.QueryEscape(bindingTestCluster), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE = %d %s", rec.Code, rec.Body)
	}
	if got := s.AWSProfileForCluster(bindingTestCluster); got != "" {
		t.Errorf("still bound to %q after DELETE", got)
	}
}
