package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/kanivet/backend/internal/cache"
	"github.com/kanivet/backend/internal/db"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newSettingsHandler(t *testing.T) *Handler {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := gdb.AutoMigrate(&db.ClusterMetricsSettings{}); err != nil {
		t.Fatal(err)
	}
	return &Handler{db: &db.DB{DB: gdb}, cache: cache.New(time.Minute, time.Minute)}
}

func putMetricsSettings(h *Handler, cluster, body string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPut, "/metrics/settings?cluster="+cluster, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	h.PutMetricsSettings(c)
	return w
}

// Rightsizing reports are keyed by provider, not tenant, so they rely on this
// hook to be dropped when the tenant or Mimir instance changes.
func TestPutMetricsSettingsTellsListenersWhichClusterChanged(t *testing.T) {
	h := newSettingsHandler(t)
	var changed []string
	h.OnMetricsSettingsChanged(func(cluster string) { changed = append(changed, cluster) })
	h.cache.Set("metrics:prod:cpu", "stale", time.Minute)

	if w := putMetricsSettings(h, "prod", `{"mimirTenant":"team-b"}`); w.Code != http.StatusOK {
		t.Fatalf("tenant: %d %s", w.Code, w.Body.String())
	}
	if w := putMetricsSettings(h, "prod", `{"mimirNamespace":"mon","mimirService":"mimir"}`); w.Code != http.StatusOK {
		t.Fatalf("service: %d %s", w.Code, w.Body.String())
	}
	if len(changed) != 2 || changed[0] != "prod" || changed[1] != "prod" {
		t.Fatalf("listeners heard %v", changed)
	}
	if _, ok := h.cache.Get("metrics:prod:cpu"); ok {
		t.Fatal("cached metrics survived the settings change")
	}
}

func TestPutMetricsSettingsRejectedBodyChangesNothing(t *testing.T) {
	h := newSettingsHandler(t)
	called := false
	h.OnMetricsSettingsChanged(func(string) { called = true })
	if w := putMetricsSettings(h, "prod", `{`); w.Code != http.StatusBadRequest {
		t.Fatalf("got %d", w.Code)
	}
	if called {
		t.Fatal("a rejected request must not drop anything")
	}
}
