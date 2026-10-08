package podcache

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer/protobuf"
	"k8s.io/client-go/kubernetes/scheme"

	"github.com/kanivet/backend/internal/k8s"
	"github.com/kanivet/backend/internal/k8s/watcher"
)

// These tests run the real watcher service and client against a small API
// server, with the cache as its pods sink: what the app does when a cluster's
// overview and its Pods list are both open.

// podServer serves /api/v1/pods the way an apiserver does: a quick first
// page, the full list as protobuf, and a watch.
type podServer struct {
	*httptest.Server
	pods      []corev1.Pod
	fullLists atomic.Int64
	watches   atomic.Int64
	// hold, when set, stops the full list half way until it is closed.
	hold   chan struct{}
	events chan []byte
}

func newPodServer(t *testing.T, n int) *podServer {
	t.Helper()
	s := &podServer{events: make(chan []byte, 8)}
	for i := range n {
		s.pods = append(s.pods, serverPod(fmt.Sprintf("pod-%03d", i)))
	}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

func serverPod(name string) corev1.Pod {
	return corev1.Pod{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"},
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: "ns", UID: "uid-" + "pod", ResourceVersion: "90",
			CreationTimestamp: metav1.NewTime(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)),
			Labels:            map[string]string{"app": "api"},
			Annotations:       map[string]string{"vcluster.loft.sh/token-abc": strings.Repeat("x", 4096)},
			ManagedFields:     []metav1.ManagedFieldsEntry{{Manager: "kubelet", FieldsV1: &metav1.FieldsV1{Raw: []byte(`{"f:status":{}}`)}}},
		},
		Spec: corev1.PodSpec{
			NodeName: "node-a",
			Containers: []corev1.Container{{
				Name: "app", Image: "registry/app:1",
				Env:       []corev1.EnvVar{{Name: "SECRET", Value: "x"}},
				Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("250m")}},
			}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
}

func (s *podServer) serve(w http.ResponseWriter, r *http.Request) {
	writeJSON := func(v any) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(v)
	}
	switch r.URL.Path {
	case "/api":
		writeJSON(map[string]any{"kind": "APIVersions", "versions": []string{"v1"}})
	case "/apis":
		writeJSON(map[string]any{"kind": "APIGroupList", "apiVersion": "v1", "groups": []any{}})
	case "/api/v1":
		writeJSON(map[string]any{"kind": "APIResourceList", "groupVersion": "v1", "resources": []any{
			map[string]any{"name": "pods", "singularName": "pod", "namespaced": true, "kind": "Pod", "verbs": []string{"get", "list", "watch"}},
		}})
	case "/api/v1/pods":
		s.servePods(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *podServer) servePods(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	flush := w.(http.Flusher).Flush
	if q.Get("watch") != "" {
		s.watches.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		flush()
		for {
			select {
			case ev := <-s.events:
				w.Write(ev)
				flush()
			case <-r.Context().Done():
				return
			}
		}
	}
	list := &corev1.PodList{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "PodList"}, ListMeta: metav1.ListMeta{ResourceVersion: "100"}}
	w.Header().Set("Content-Type", "application/vnd.kubernetes.protobuf")
	if q.Get("limit") != "" {
		n := min(25, len(s.pods))
		list.Items = s.pods[:n]
		if left := int64(len(s.pods) - n); left > 0 {
			list.Continue, list.RemainingItemCount = "next", &left
		}
		w.Write(encodePods(list))
		return
	}
	s.fullLists.Add(1)
	list.Items = s.pods
	data := encodePods(list)
	w.Write(data[:len(data)/2])
	flush()
	if s.hold != nil {
		select {
		case <-s.hold:
		case <-r.Context().Done():
			return
		}
	}
	w.Write(data[len(data)/2:])
}

func encodePods(list *corev1.PodList) []byte {
	var buf bytes.Buffer
	if err := protobuf.NewSerializer(scheme.Scheme, scheme.Scheme).Encode(list, &buf); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// add sends a watch event for a new pod.
func (s *podServer) add(name string) {
	pod := serverPod(name)
	pod.ResourceVersion = "101"
	raw, err := json.Marshal(&pod)
	if err != nil {
		panic(err)
	}
	ev, err := json.Marshal(metav1.WatchEvent{Type: "ADDED", Object: runtime.RawExtension{Raw: raw}})
	if err != nil {
		panic(err)
	}
	s.events <- append(ev, '\n')
}

// listHub records what the watcher sends the Pods list's subscribers.
type listHub struct {
	mu    sync.Mutex
	rows  map[string]bool
	syncs []*watcher.InitialSyncMessage
	total int
}

func (h *listHub) BroadcastDirect(topic string, m watcher.Message) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch v := m.(type) {
	case *watcher.BulkListMessage:
		if h.rows == nil {
			h.rows = map[string]bool{}
		}
		if v.Total > 0 {
			h.total = v.Total
		}
		for _, item := range v.Items {
			if !v.IsMinimal {
				h.rows[item["name"].(string)] = true
			}
		}
	case *watcher.InitialSyncMessage:
		h.syncs = append(h.syncs, v)
	}
	return nil
}

func (h *listHub) Broadcast(topic string, m watcher.Message) error {
	if ev, ok := m.(*watcher.ResourceEventMessage); ok && ev.Action == "added" {
		h.mu.Lock()
		h.rows[ev.Item["name"].(string)] = true
		h.mu.Unlock()
	}
	return nil
}

func (h *listHub) BroadcastAll(watcher.Message) error        { return nil }
func (h *listHub) FlushTopic(string) error                   { return nil }
func (h *listHub) SetSortPreference(string, string, string)  {}
func (h *listHub) GetSortPreference(string) (string, string) { return "age", "desc" }
func (h *listHub) CleanupTopic(string)                       {}
func (h *listHub) sent() (rows int, completed []int, total int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, s := range h.syncs {
		if s.Epoch > 0 {
			completed = append(completed, s.ItemCount)
		}
	}
	return len(h.rows), completed, h.total
}

// start runs a watcher service against the server, with a cache as its sink.
func start(t *testing.T, srv *podServer) (*watcher.Service, *Cache, *listHub) {
	t.Helper()
	// The kubeconfig sits where the client looks by default, under a home
	// of the test's own (USERPROFILE is the one Windows reads), and the
	// developer's KUBECONFIG is kept out of it.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("KUBECONFIG", "")
	cfg := "apiVersion: v1\nkind: Config\nclusters:\n- name: c\n  cluster:\n    server: " + srv.URL +
		"\ncontexts:\n- name: c\n  context:\n    cluster: c\n    user: u\nusers:\n- name: u\n  user:\n    token: t\n"
	path := filepath.Join(home, ".kube", "config")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	hub := &listHub{}
	svc := watcher.NewService(k8s.NewClient(), hub)
	cache := New(svc, time.Minute)
	t.Cleanup(func() {
		cache.Close()
		svc.Shutdown()
	})
	return svc, cache, hub
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// The overview opens first, then the Pods list: one list and one watch serve
// both, the list's rows go out while it is still downloading, and the cache
// only answers once it holds every pod.
func TestOverviewAndPodsListShareOneListAndWatch(t *testing.T) {
	srv := newPodServer(t, 300)
	srv.hold = make(chan struct{})
	svc, cache, hub := start(t, srv)

	read := make(chan []*corev1.Pod, 1)
	go func() { read <- mustList(t, cache, "c") }()
	eventually(t, "the rows of the half that has arrived", func() bool {
		rows, _, _ := hub.sent()
		return rows >= 100
	})
	if err := svc.StartWatch("c", "", "v1", "pods", "", "age", "desc"); err != nil {
		t.Fatal(err)
	}
	select {
	case pods := <-read:
		t.Fatalf("the cache answered with %d pods while the list was still downloading", len(pods))
	case <-time.After(50 * time.Millisecond):
	}
	if rows, completed, total := hub.sent(); rows >= 300 || len(completed) != 0 || total != 300 {
		t.Fatalf("mid-list: %d rows sent, syncs %v, total %d; want part of the rows, no sync, total 300", rows, completed, total)
	}

	close(srv.hold)
	pods := <-read
	if len(pods) != 300 {
		t.Fatalf("the cache holds %d pods, want 300", len(pods))
	}
	if p := pods[0]; p.ManagedFields != nil || len(p.Annotations) != 0 || p.Spec.Containers[0].Env != nil ||
		p.Spec.NodeName != "node-a" || p.Spec.Containers[0].Resources.Requests.Cpu().MilliValue() != 250 {
		t.Fatalf("cached pod is not the slim copy: %+v", p)
	}
	eventually(t, "the list's sync and its replay to the late subscriber", func() bool {
		rows, completed, _ := hub.sent()
		return rows == 300 && len(completed) == 2
	})
	if _, completed, _ := hub.sent(); completed[0] != 300 || completed[1] != 300 {
		t.Fatalf("syncs for %v items, want 300 each", completed)
	}

	// A live change reaches the list and the cache alike, from the one watch.
	eventually(t, "the watch", func() bool { return srv.watches.Load() == 1 })
	srv.add("pod-new")
	eventually(t, "the new pod in the cache", func() bool { return len(mustList(t, cache, "c")) == 301 })
	eventually(t, "the new pod's row", func() bool { rows, _, _ := hub.sent(); return rows == 301 })

	if lists, watches := srv.fullLists.Load(), srv.watches.Load(); lists != 1 || watches != 1 {
		t.Fatalf("the server saw %d full lists and %d watches, want 1 and 1", lists, watches)
	}
}

// The Pods list opens first: the overview that follows reads the pods the
// list's watch already holds and asks the cluster for nothing.
func TestOverviewReadsThePodsAnOpenListAlreadyHolds(t *testing.T) {
	srv := newPodServer(t, 120)
	svc, cache, hub := start(t, srv)

	if err := svc.StartWatch("c", "", "v1", "pods", "", "age", "desc"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the list", func() bool { _, completed, _ := hub.sent(); return len(completed) == 1 })

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	pods, err := cache.List(ctx, "c")
	if err != nil || len(pods) != 120 {
		t.Fatalf("cache read: %d pods, err %v", len(pods), err)
	}
	if lists := srv.fullLists.Load(); lists != 1 {
		t.Fatalf("the server saw %d full lists, want 1", lists)
	}

	// The list tab closing does not take the pods from under the overview.
	svc.StopWatch("c", "", "v1", "pods", "")
	srv.add("pod-new")
	eventually(t, "the new pod in the cache", func() bool { return len(mustList(t, cache, "c")) == 121 })
}
