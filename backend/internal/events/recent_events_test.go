package events

import (
	"path/filepath"
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
