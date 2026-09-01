package localsecurity

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func setupTestRouter(secret string) *gin.Engine {
	SetSessionSecret(secret)

	router := gin.New()
	router.Use(SessionSecretMiddleware())
	router.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	return router
}

func TestSessionSecretMiddlewareValidSecret(t *testing.T) {
	router := setupTestRouter("test-secret-123")

	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Session-Secret", "test-secret-123")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d", http.StatusOK, w.Code)
	}
}

func TestSessionSecretMiddlewareValidSecretQueryParam(t *testing.T) {
	router := setupTestRouter("test-secret-456")

	req, _ := http.NewRequest("GET", "/test?session_secret=test-secret-456", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d", http.StatusOK, w.Code)
	}
}

func TestSessionSecretMiddlewareMismatchSecret(t *testing.T) {
	router := setupTestRouter("correct-secret")

	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Session-Secret", "wrong-secret")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected %d, got %d", http.StatusForbidden, w.Code)
	}
	if w.Header().Get("X-Session-Refresh-Required") != "true" {
		t.Fatal("expected session refresh header")
	}
	if !strings.Contains(w.Body.String(), "session_secret_mismatch") {
		t.Fatalf("expected mismatch body, got %s", w.Body.String())
	}
}

func TestSessionSecretMiddlewareMissingSecret(t *testing.T) {
	router := setupTestRouter("required-secret")

	req, _ := http.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected %d, got %d", http.StatusForbidden, w.Code)
	}
	if w.Header().Get("X-Session-Refresh-Required") != "true" {
		t.Fatal("expected session refresh header")
	}
	if !strings.Contains(w.Body.String(), "session_secret_missing") {
		t.Fatalf("expected missing body, got %s", w.Body.String())
	}
}

func TestSessionSecretMiddlewareEmptyServerSecret(t *testing.T) {
	router := setupTestRouter("")

	req, _ := http.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d", http.StatusOK, w.Code)
	}
}
