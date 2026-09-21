package k8s

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/rest"
)

// fakeListServer answers list requests the way the API server does: a list
// served from the watch cache (resourceVersion=0) ignores limit and returns
// every object, while a plain limited list returns one page plus
// remainingItemCount. It records how many objects it had to send.
func fakeListServer(t *testing.T, total int, sent *atomic.Int64) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		n := total
		listMeta := map[string]interface{}{"resourceVersion": "1"}
		if limit, err := strconv.Atoi(q.Get("limit")); err == nil && limit > 0 && limit < total && q.Get("resourceVersion") != "0" {
			n = limit
			listMeta["continue"] = "next"
			listMeta["remainingItemCount"] = total - limit
		}
		items := make([]map[string]interface{}, n)
		for i := range items {
			items[i] = map[string]interface{}{"metadata": map[string]interface{}{"name": "pod-" + strconv.Itoa(i), "namespace": "ns"}}
		}
		sent.Add(int64(n))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"kind": "PartialObjectMetadataList", "apiVersion": "meta.k8s.io/v1", "metadata": listMeta, "items": items,
		})
	}))
}

func TestGetResourceCountDoesNotDownloadTheWholeList(t *testing.T) {
	const total = 780
	var sent atomic.Int64
	srv := fakeListServer(t, total, &sent)
	defer srv.Close()

	mc, err := metadata.NewForConfig(&rest.Config{Host: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	c := &Client{metadata: map[string]metadata.Interface{"test": mc}}

	got, err := c.GetResourceCount(context.Background(), "test", schema.GroupVersionResource{Version: "v1", Resource: "pods"})
	if err != nil {
		t.Fatal(err)
	}
	if got != total {
		t.Errorf("count = %d, want %d", got, total)
	}
	if n := sent.Load(); n != 1 {
		t.Errorf("server sent %d objects to answer one count, want 1", n)
	}
}
