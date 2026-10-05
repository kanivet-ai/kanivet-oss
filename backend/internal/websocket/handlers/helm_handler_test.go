package handlers

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kanivet/backend/internal/helm"
	ws "github.com/kanivet/backend/internal/websocket"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
)

type fakeHelmBackend struct {
	client   kubernetes.Interface
	releases []helm.Release
	failList atomic.Bool
	calls    atomic.Int32
}

func (f *fakeHelmBackend) StreamReleases(ctx context.Context, cluster string) (<-chan []helm.Release, <-chan helm.StreamProgress, error) {
	f.calls.Add(1)
	if f.failList.Load() {
		return nil, nil, errors.New("boom")
	}
	rc := make(chan []helm.Release, 1)
	pc := make(chan helm.StreamProgress, 1)
	rc <- f.releases
	close(rc)
	close(pc)
	return rc, pc, nil
}

func (f *fakeHelmBackend) GetK8sClient(string) (kubernetes.Interface, *rest.Config, error) {
	return f.client, nil, nil
}

func (f *fakeHelmBackend) ParseReleaseFromSecret(any) (*helm.Release, error) {
	return nil, errors.New("unused")
}

func (f *fakeHelmBackend) GetReleaseBasic(context.Context, string, string, string) (*helm.Release, error) {
	return nil, errors.New("unused")
}

type helmTestEnv struct {
	url     string
	handler *HelmHandler
	backend *fakeHelmBackend
}

func newHelmTestEnv(t *testing.T, nReleases int) *helmTestEnv {
	t.Helper()
	b := &fakeHelmBackend{client: fake.NewSimpleClientset()}
	for i := 0; i < nReleases; i++ {
		b.releases = append(b.releases, helm.Release{Name: "rel" + string(rune('a'+i)), Namespace: "ns", Chart: "c", Status: "deployed"})
	}
	server := ws.NewServer()
	h := newHelmHandler(b, server.Hub())
	server.RegisterHandler("helm", h)
	srv := httptest.NewServer(server)
	t.Cleanup(srv.Close)
	return &helmTestEnv{url: srv.URL, handler: h, backend: b}
}

func (e *helmTestEnv) dial(t *testing.T) *websocket.Conn {
	t.Helper()
	c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(e.url, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func helmSubscribe(t *testing.T, c *websocket.Conn) {
	t.Helper()
	if err := c.WriteJSON(map[string]any{"type": "helm", "payload": map[string]any{"action": "subscribe", "cluster": "c1"}}); err != nil {
		t.Fatal(err)
	}
}

// readUntilDone collects releases until a done frame; it returns the releases
// seen before the done frame and the done frame's own release count.
func readUntilDone(t *testing.T, c *websocket.Conn) (names []string, doneReleases int, errMsg string) {
	t.Helper()
	for {
		c.SetReadDeadline(time.Now().Add(5 * time.Second))
		var m struct {
			Releases []helm.Release `json:"releases"`
			Done     bool           `json:"done"`
			Error    string         `json:"error"`
		}
		if err := c.ReadJSON(&m); err != nil {
			t.Fatalf("waiting for done frame: %v", err)
		}
		if m.Done {
			return names, len(m.Releases), m.Error
		}
		for _, r := range m.Releases {
			names = append(names, r.Name)
		}
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestHelmSecondSubscriberGetsSnapshotAndDoneIsSeparate(t *testing.T) {
	env := newHelmTestEnv(t, 3)
	c1 := env.dial(t)
	helmSubscribe(t, c1)
	names, doneN, _ := readUntilDone(t, c1)
	if len(names) != 3 || doneN != 0 {
		t.Fatalf("first subscriber: names=%v doneReleases=%d", names, doneN)
	}

	c2 := env.dial(t)
	helmSubscribe(t, c2)
	names, doneN, _ = readUntilDone(t, c2)
	if len(names) != 3 || doneN != 0 {
		t.Fatalf("second subscriber got no snapshot: names=%v doneReleases=%d", names, doneN)
	}
	if got := env.backend.calls.Load(); got != 1 {
		t.Fatalf("second subscriber restarted the stream: %d list calls", got)
	}
}

func TestHelmDuplicateSubscribeResendsSnapshotWithoutDuplicatingStream(t *testing.T) {
	env := newHelmTestEnv(t, 2)
	c := env.dial(t)
	helmSubscribe(t, c)
	readUntilDone(t, c)

	helmSubscribe(t, c)
	names, _, _ := readUntilDone(t, c)
	if len(names) != 2 {
		t.Fatalf("re-subscribe did not resend the snapshot: %v", names)
	}
	if got := env.backend.calls.Load(); got != 1 {
		t.Fatalf("duplicate subscribe started a second stream: %d", got)
	}
}

func TestHelmStreamStopsWhenLastSubscriberDisconnects(t *testing.T) {
	env := newHelmTestEnv(t, 1)
	c1, c2 := env.dial(t), env.dial(t)
	helmSubscribe(t, c1)
	readUntilDone(t, c1)
	helmSubscribe(t, c2)
	readUntilDone(t, c2)

	c1.Close()
	time.Sleep(200 * time.Millisecond)
	if !env.handler.HasActiveStream("c1") {
		t.Fatal("stream stopped while a subscriber remained")
	}
	c2.Close()
	waitFor(t, "stream cleanup after last disconnect", func() bool { return !env.handler.HasActiveStream("c1") })
}

func TestHelmFailedStreamIsRemovedAndRestartedByNextSubscriber(t *testing.T) {
	env := newHelmTestEnv(t, 1)
	env.backend.failList.Store(true)
	c := env.dial(t)
	helmSubscribe(t, c)
	_, _, errMsg := readUntilDone(t, c)
	if errMsg == "" {
		t.Fatal("expected an error frame when the initial load fails")
	}
	waitFor(t, "failed stream removal", func() bool { return !env.handler.HasActiveStream("c1") })

	env.backend.failList.Store(false)
	helmSubscribe(t, c)
	names, _, errMsg := readUntilDone(t, c)
	if errMsg != "" || len(names) != 1 {
		t.Fatalf("restart after failure: names=%v err=%q", names, errMsg)
	}
}

func TestChunkReleasesBoundsFrameSize(t *testing.T) {
	big := strings.Repeat("x", 100*1024)
	var rels []helm.Release
	for i := 0; i < 10; i++ {
		rels = append(rels, helm.Release{Name: "r", Description: big})
	}
	chunks := chunkReleases(rels)
	if len(chunks) < 3 {
		t.Fatalf("expected several chunks, got %d", len(chunks))
	}
	total := 0
	for _, c := range chunks {
		total += len(c)
	}
	if total != 10 {
		t.Fatalf("lost releases: %d", total)
	}
}
