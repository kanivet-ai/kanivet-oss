package events

import (
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/kanivet/backend/internal/db"
	"github.com/kanivet/backend/internal/k8s"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func openEventsDB(t *testing.T) *db.DB {
	t.Helper()
	g, err := gorm.Open(sqlite.Open("file:"+filepath.Join(t.TempDir(), "events.db")+"?_pragma=busy_timeout(5000)"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := g.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	d := &db.DB{DB: g}
	if err := d.MigrateEvents(); err != nil {
		t.Fatal(err)
	}
	return d
}

func clusterEvent(ns, name string, last time.Time) *corev1.Event {
	return &corev1.Event{
		ObjectMeta:     metav1.ObjectMeta{Name: name, Namespace: ns, UID: types.UID("uid-" + name)},
		Reason:         "Reason-" + name,
		LastTimestamp:  metav1.NewTime(last),
		InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "p", Namespace: ns},
	}
}

// Closing a cluster's tab stops its listener. A dashboard refresh still
// running then must not start it again: only opening a view of the cluster
// does. After shutdown nothing starts one.
func TestRecentEventsDoesNotRestartAReleasedCluster(t *testing.T) {
	el := NewEventListener(&k8s.MockClient{TypedClient: fake.NewSimpleClientset()}, openEventsDB(t))
	defer el.StopAll()

	el.RecentEvents("c1", 20)
	if !el.IsListening("c1") {
		t.Fatal("asking for recent events must start the cluster's listener")
	}
	el.StopListening("c1")
	el.RecentEvents("c1", 20)
	if el.IsListening("c1") {
		t.Fatal("a dashboard refresh restarted the listener of a closed cluster")
	}
	if err := el.StartListening("c1"); err != nil || !el.IsListening("c1") {
		t.Fatalf("opening the cluster again must start its listener: %v", err)
	}

	el.StopAll()
	el.RecentEvents("c2", 20)
	if err := el.StartListening("c3"); err == nil || el.IsListening("c2") || el.IsListening("c3") {
		t.Fatal("a listener started after shutdown")
	}
}

// slowClient builds the client of cluster "slow" only once gate is closed, as
// for a vcluster whose tunnel is reconnecting.
type slowClient struct {
	*k8s.MockClient
	building, gate chan struct{}
}

func (c *slowClient) GetClientForCluster(cluster string) (kubernetes.Interface, error) {
	if cluster == "slow" {
		close(c.building)
		<-c.gate
	}
	return c.MockClient.GetClientForCluster(cluster)
}

// Every cluster's dashboard asks IsListening on each refresh; a listener
// whose client takes long to build must not stall them.
func TestSlowListenerStartDoesNotBlockOtherClusters(t *testing.T) {
	building, gate := make(chan struct{}), make(chan struct{})
	el := NewEventListener(&slowClient{MockClient: &k8s.MockClient{TypedClient: fake.NewSimpleClientset()}, building: building, gate: gate}, openEventsDB(t))
	defer el.StopAll()
	started := make(chan error, 1)
	go func() { started <- el.StartListening("slow") }()
	<-building

	answered := make(chan struct{})
	go func() {
		el.IsListening("other")
		el.RecentEvents("other", 20)
		close(answered)
	}()
	select {
	case <-answered:
	case <-time.After(2 * time.Second):
		close(gate)
		t.Fatal("another cluster waited on a listener's client being built")
	}
	if el.IsListening("slow") {
		t.Fatal("listener reported active before its client was built")
	}
	close(gate)
	if err := <-started; err != nil || !el.IsListening("slow") {
		t.Fatalf("slow listener did not start: %v", err)
	}
}

// A LIST pages in namespace/name order. On a cluster with more events than
// the listener keeps, the newest events must still be the ones kept, wherever
// their namespace sorts.
func TestInitialSyncKeepsTheNewestEvents(t *testing.T) {
	now := time.Now()
	pages := []*corev1.Event{
		clusterEvent("a-ns", "a1", now.Add(-3*time.Hour)),
		clusterEvent("b-ns", "b1", now.Add(-2*time.Hour)),
		clusterEvent("z-ns", "z-newest", now.Add(-time.Minute)),
	}
	cs := fake.NewSimpleClientset()
	cs.PrependReactor("list", "events", func(a k8stesting.Action) (bool, runtime.Object, error) {
		i := 0
		if c := a.(k8stesting.ListActionImpl).ListOptions.Continue; c != "" {
			i, _ = strconv.Atoi(c)
		}
		list := &corev1.EventList{Items: []corev1.Event{*pages[i]}}
		list.ResourceVersion = "10"
		if i+1 < len(pages) {
			list.Continue = strconv.Itoa(i + 1)
		}
		return true, list, nil
	})
	el := NewEventListener(&k8s.MockClient{TypedClient: cs}, openEventsDB(t))
	el.windowSize = 2
	defer el.StopAll()

	deadline := time.Now().Add(5 * time.Second)
	for {
		rows, ok := el.RecentEvents("c1", 1)
		if ok {
			if len(rows) != 1 || rows[0].Name != "z-newest" {
				t.Fatalf("recent events %v, want [z-newest]", rows)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("listener never reported its initial sync")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The dashboard reads its recent events from the listener's store. Rows left
// from an earlier session must not be served before the listener has stored
// the cluster's current events.
func TestRecentEventsWaitForTheInitialSync(t *testing.T) {
	database := openEventsDB(t)
	stale := db.K8sEvent{UID: "uid-stale", Name: "stale", Reason: "Stale", EventTime: time.Now().Add(-20 * 24 * time.Hour)}
	if err := database.StoreEvents("c1", []db.K8sEvent{stale}); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	cs := fake.NewSimpleClientset(
		clusterEvent("a-namespace", "older", now.Add(-time.Hour)),
		clusterEvent("z-namespace", "newest", now.Add(-time.Minute)),
	)
	release := make(chan struct{})
	cs.PrependReactor("list", "events", func(k8stesting.Action) (bool, runtime.Object, error) {
		<-release
		return false, nil, nil
	})
	el := NewEventListener(&k8s.MockClient{TypedClient: cs}, database)
	defer el.StopAll()

	if _, ok := el.RecentEvents("c1", 20); ok {
		t.Fatal("recent events served before the listener stored the cluster's events")
	}
	if !el.IsListening("c1") {
		t.Fatal("asking for recent events must start the cluster's listener")
	}
	close(release)

	deadline := time.Now().Add(5 * time.Second)
	for {
		rows, ok := el.RecentEvents("c1", 2)
		if ok {
			if len(rows) != 2 || rows[0].Name != "newest" || rows[1].Name != "older" {
				names := make([]string, len(rows))
				for i, r := range rows {
					names[i] = r.Name
				}
				t.Fatalf("recent events %v, want [newest older]", names)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("listener never reported its initial sync")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
