package handlers

import (
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kanivet/backend/internal/helm"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// helmFrame is any frame the helm topic carries.
type helmFrame struct {
	Releases   []helm.Release `json:"releases"`
	Done       bool           `json:"done"`
	Error      string         `json:"error"`
	SnapshotID uint64         `json:"snapshotId"`
	EventType  string         `json:"eventType"`
	Release    helm.Release   `json:"release"`
}

func readHelmFrame(t *testing.T, c *websocket.Conn, timeout time.Duration) (helmFrame, bool) {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(timeout))
	var m helmFrame
	if err := c.ReadJSON(&m); err != nil {
		return m, false
	}
	return m, true
}

func revisionSecret(name string, rev int) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            fmt.Sprintf("sh.helm.release.v1.%s.v%d", name, rev),
			Namespace:       "ns",
			ResourceVersion: strconv.Itoa(100 + rev),
			Labels:          map[string]string{"owner": "helm", "name": name, "version": strconv.Itoa(rev), "status": "superseded"},
		},
	}
}

func parseRevisionSecret(obj any) (*helm.Release, error) {
	s, ok := obj.(*corev1.Secret)
	if !ok {
		return nil, fmt.Errorf("not a secret")
	}
	rev, _ := strconv.Atoi(s.Labels["version"])
	return &helm.Release{Name: s.Labels["name"], Namespace: s.Namespace, Revision: rev, Status: s.Labels["status"]}, nil
}

// controlledWatch makes every secrets watch the one returned, so a test
// decides which events the handler sees and when.
func controlledWatch(cs *fake.Clientset) (*watch.FakeWatcher, <-chan struct{}) {
	w := watch.NewFakeWithChanSize(10, false)
	started := make(chan struct{})
	var once sync.Once
	cs.PrependWatchReactor("secrets", func(k8stesting.Action) (bool, watch.Interface, error) {
		once.Do(func() { close(started) })
		return true, w, nil
	})
	return w, started
}

// Every upgrade past history-max deletes the oldest revision's secret; that
// used to remove the whole release from the list.
func TestHelmDeletingARevisionDoesNotRemoveTheRelease(t *testing.T) {
	cs := fake.NewSimpleClientset()
	w, started := controlledWatch(cs)
	env := newHelmTestEnv(t, 0)
	env.backend.client = cs
	env.backend.releases = []helm.Release{{Name: "app", Namespace: "ns", Chart: "c", Status: "deployed", Revision: 3}}
	env.backend.parse = parseRevisionSecret

	var mu sync.Mutex
	remaining := map[int]bool{2: true, 3: true}
	env.backend.get = func(namespace, name string) (*helm.Release, error) {
		mu.Lock()
		defer mu.Unlock()
		latest := 0
		for r := range remaining {
			latest = max(latest, r)
		}
		if latest == 0 {
			return nil, fmt.Errorf("release %s not found: %w", name, helm.ErrReleaseNotFound)
		}
		return &helm.Release{Name: name, Namespace: namespace, Chart: "c", Status: "deployed", Revision: latest}, nil
	}
	drop := func(rev int) {
		mu.Lock()
		delete(remaining, rev)
		mu.Unlock()
		w.Delete(revisionSecret("app", rev))
	}

	c := env.dial(t)
	helmSubscribe(t, c)
	readUntilDone(t, c)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("watch never started")
	}

	// The oldest revision pruned: nothing about the release changed, so the
	// next frame is the answer to the deletion that follows.
	drop(1)

	// The latest revision deleted (a rollback of a failed upgrade): the
	// release stays, at the revision that is now the latest.
	drop(3)
	m, ok := readHelmFrame(t, c, 3*time.Second)
	if !ok || m.EventType != "modified" || m.Release.Revision != 2 {
		t.Fatalf("after deleting the latest revision: %+v (ok=%v), want modified at revision 2", m, ok)
	}

	// The last revision gone: now the release is deleted.
	drop(2)
	m, ok = readHelmFrame(t, c, 3*time.Second)
	if !ok || m.EventType != "deleted" || m.Release.Name != "app" {
		t.Fatalf("after deleting the last revision: %+v (ok=%v), want deleted", m, ok)
	}
}

// A relist (after a 410) used to merge into the snapshot, so a release
// uninstalled during the gap stayed listed as deployed.
func TestHelmRelistReplacesTheSnapshot(t *testing.T) {
	cs := fake.NewSimpleClientset()
	w, started := controlledWatch(cs)
	env := newHelmTestEnv(t, 0)
	env.backend.client = cs
	env.backend.releases = []helm.Release{
		{Name: "keep", Namespace: "ns", Chart: "c", Status: "deployed"},
		{Name: "gone", Namespace: "ns", Chart: "c", Status: "deployed"},
	}

	c := env.dial(t)
	helmSubscribe(t, c)
	var first uint64
	for {
		m, ok := readHelmFrame(t, c, 5*time.Second)
		if !ok {
			t.Fatal("no initial load")
		}
		if m.SnapshotID == 0 {
			t.Fatalf("frame without a snapshot id: %+v", m)
		}
		first = m.SnapshotID
		if m.Done {
			break
		}
	}
	<-started

	env.backend.mu.Lock()
	env.backend.releases = env.backend.releases[:1]
	env.backend.mu.Unlock()
	w.Error(&metav1.Status{Status: metav1.StatusFailure, Code: 410, Reason: metav1.StatusReasonExpired})

	var names []string
	for {
		m, ok := readHelmFrame(t, c, 5*time.Second)
		if !ok {
			t.Fatal("no relist after a 410")
		}
		if m.SnapshotID == first {
			t.Fatalf("relist reused the snapshot id %d", first)
		}
		for _, r := range m.Releases {
			names = append(names, r.Name)
		}
		if m.Done {
			break
		}
	}
	if len(names) != 1 || names[0] != "keep" {
		t.Fatalf("relist sent %v, want [keep]", names)
	}

	// A late subscriber gets the relisted snapshot, not the merged one.
	c2 := env.dial(t)
	helmSubscribe(t, c2)
	got, _, _ := readUntilDone(t, c2)
	if len(got) != 1 || got[0] != "keep" {
		t.Fatalf("snapshot after relist = %v, want [keep]", got)
	}
}
