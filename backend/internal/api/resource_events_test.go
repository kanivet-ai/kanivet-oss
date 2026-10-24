package api

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kanivet/backend/internal/db"
)

func TestEventsFieldSelectorIncludesKind(t *testing.T) {
	got := eventsFieldSelector(db.EventObject{Name: "api", Namespace: "prod", Kind: "Deployment"})
	want := "involvedObject.name=api,involvedObject.namespace=prod,involvedObject.kind=Deployment"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestEventsFieldSelectorClusterScoped(t *testing.T) {
	got := eventsFieldSelector(db.EventObject{Name: "node-1", Kind: "Node"})
	want := "involvedObject.name=node-1,involvedObject.kind=Node"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestEventsFieldSelectorWithoutKind(t *testing.T) {
	got := eventsFieldSelector(db.EventObject{Name: "api", Namespace: "prod"})
	want := "involvedObject.name=api,involvedObject.namespace=prod"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestEventsFieldSelectorWithUID(t *testing.T) {
	got := eventsFieldSelector(db.EventObject{Name: "web-0", Namespace: "prod", Kind: "Pod", UID: "abc"})
	want := "involvedObject.name=web-0,involvedObject.namespace=prod,involvedObject.kind=Pod,involvedObject.uid=abc"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func testContext(url string) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", url, nil)
	return c
}

func TestEventsObjectReadsQueryParams(t *testing.T) {
	got := eventsObject(testContext("/x?uid=abc&apiVersion=apps/v1"), "Deployment", "prod", "api")
	want := db.EventObject{Namespace: "prod", Name: "api", Kind: "Deployment", APIVersion: "apps/v1", UID: "abc"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if got := eventsObject(testContext("/x?kind=StatefulSet"), "statefulsets", "prod", "db"); got.Kind != "StatefulSet" || got.UID != "" {
		t.Fatalf("kind query param not applied: %+v", got)
	}
}

func TestWantsFresh(t *testing.T) {
	for url, want := range map[string]bool{"/x": false, "/x?fresh=1": true, "/x?fresh=true": true, "/x?fresh=0": false} {
		if got := wantsFresh(testContext(url)); got != want {
			t.Errorf("%s: wantsFresh = %v, want %v", url, got, want)
		}
	}
}
