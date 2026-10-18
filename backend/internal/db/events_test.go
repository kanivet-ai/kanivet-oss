package db

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newTestDB(t *testing.T) *DB {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	d := &DB{DB: gdb}
	if err := d.MigrateEvents(); err != nil {
		t.Fatalf("migrate events: %v", err)
	}
	return d
}

// A Deployment, Service and HorizontalPodAutoscaler commonly share one name.
// Asking for the Deployment's events must not return the HPA's.
func TestGetEventsForResourceByKindExcludesSameNameOtherKinds(t *testing.T) {
	d := newTestDB(t)
	now := time.Now()
	rows := []K8sEvent{
		{Cluster: "c1", InvolvedObjectName: "api", InvolvedObjectNamespace: "prod", InvolvedObjectKind: "Deployment", InvolvedObjectAPIVersion: "apps/v1", Reason: "ScalingReplicaSet", EventTime: now},
		{Cluster: "c1", InvolvedObjectName: "api", InvolvedObjectNamespace: "prod", InvolvedObjectKind: "HorizontalPodAutoscaler", InvolvedObjectAPIVersion: "autoscaling/v2", Reason: "SuccessfulRescale", EventTime: now},
		{Cluster: "c1", InvolvedObjectName: "api", InvolvedObjectNamespace: "prod", InvolvedObjectKind: "Service", InvolvedObjectAPIVersion: "v1", Reason: "UpdatedLoadBalancer", EventTime: now},
		{Cluster: "c1", InvolvedObjectName: "api", InvolvedObjectNamespace: "staging", InvolvedObjectKind: "Deployment", InvolvedObjectAPIVersion: "apps/v1", Reason: "OtherNamespace", EventTime: now},
	}
	if err := d.Create(&rows).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	unfiltered, err := d.GetEventsForResource("c1", "prod", "api", "")
	if err != nil {
		t.Fatalf("GetEventsForResource: %v", err)
	}
	if len(unfiltered) != 3 {
		t.Fatalf("name-only query should mix kinds (3 rows), got %d", len(unfiltered))
	}

	got, err := d.GetEventsForResourceByKind("c1", "prod", "api", "Deployment", "")
	if err != nil {
		t.Fatalf("GetEventsForResourceByKind: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want exactly the Deployment event, got %d rows", len(got))
	}
	if got[0].Reason != "ScalingReplicaSet" {
		t.Fatalf("wrong event returned: %+v", got[0])
	}
}

func TestGetEventsForResourceByKindClusterScoped(t *testing.T) {
	d := newTestDB(t)
	now := time.Now()
	rows := []K8sEvent{
		{Cluster: "c1", InvolvedObjectName: "worker-1", InvolvedObjectNamespace: "", InvolvedObjectKind: "Node", Reason: "NodeReady", EventTime: now},
		{Cluster: "c1", InvolvedObjectName: "worker-1", InvolvedObjectNamespace: "default", InvolvedObjectKind: "Pod", Reason: "Scheduled", EventTime: now},
	}
	if err := d.Create(&rows).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	got, err := d.GetEventsForResourceByKind("c1", "", "worker-1", "Node", "")
	if err != nil {
		t.Fatalf("GetEventsForResourceByKind: %v", err)
	}
	if len(got) != 1 || got[0].Reason != "NodeReady" {
		t.Fatalf("want only the Node event, got %+v", got)
	}
}

func reasons(events []K8sEvent) map[string]bool {
	out := make(map[string]bool, len(events))
	for _, e := range events {
		out[e.Reason] = true
	}
	return out
}

// A StatefulSet pod web-0 recreated after an OOM kill must not show its
// predecessor's events, which stay stored for 30 days under the same name.
func TestGetEventsForObjectByUIDSkipsPredecessors(t *testing.T) {
	d := newTestDB(t)
	now := time.Now()
	rows := []K8sEvent{
		{Cluster: "c1", InvolvedObjectUID: "old-uid", InvolvedObjectName: "web-0", InvolvedObjectNamespace: "prod", InvolvedObjectKind: "Pod", InvolvedObjectAPIVersion: "v1", Reason: "OOMKilled", EventTime: now.Add(-time.Hour)},
		{Cluster: "c1", InvolvedObjectUID: "new-uid", InvolvedObjectName: "web-0", InvolvedObjectNamespace: "prod", InvolvedObjectKind: "Pod", InvolvedObjectAPIVersion: "v1", Reason: "Started", EventTime: now},
		{Cluster: "c1", InvolvedObjectName: "web-0", InvolvedObjectNamespace: "prod", InvolvedObjectKind: "Pod", InvolvedObjectAPIVersion: "v1", Reason: "NoUID", EventTime: now},
		{Cluster: "c1", InvolvedObjectUID: "svc-uid", InvolvedObjectName: "web-0", InvolvedObjectNamespace: "prod", InvolvedObjectKind: "Service", InvolvedObjectAPIVersion: "v1", Reason: "OtherKind", EventTime: now},
	}
	if err := d.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	got, err := d.GetEventsForObject("c1", EventObject{Namespace: "prod", Name: "web-0", Kind: "Pod", APIVersion: "v1", UID: "new-uid"})
	if err != nil {
		t.Fatal(err)
	}
	if r := reasons(got); len(r) != 2 || !r["Started"] || !r["NoUID"] {
		t.Fatalf("got %v, want Started and the UID-less NoUID", r)
	}
	// Without a kind, a UID-less row cannot be told apart from another kind's.
	got, err = d.GetEventsForObject("c1", EventObject{Namespace: "prod", Name: "web-0", UID: "new-uid"})
	if err != nil {
		t.Fatal(err)
	}
	if r := reasons(got); len(r) != 1 || !r["Started"] {
		t.Fatalf("got %v, want only Started", r)
	}
	// Without a UID every incarnation of the kind matches, as before.
	got, err = d.GetEventsForObject("c1", EventObject{Namespace: "prod", Name: "web-0", Kind: "Pod"})
	if err != nil {
		t.Fatal(err)
	}
	if r := reasons(got); len(r) != 3 || r["OtherKind"] {
		t.Fatalf("got %v, want the three Pod events", r)
	}
}

func TestGetEventsForObjectMatchesTheAPIGroupNotTheVersion(t *testing.T) {
	d := newTestDB(t)
	now := time.Now()
	rows := []K8sEvent{
		{Cluster: "c1", InvolvedObjectName: "db", InvolvedObjectNamespace: "prod", InvolvedObjectKind: "Cluster", InvolvedObjectAPIVersion: "postgres.example.io/v1beta1", Reason: "OldVersion", EventTime: now},
		{Cluster: "c1", InvolvedObjectName: "db", InvolvedObjectNamespace: "prod", InvolvedObjectKind: "Cluster", InvolvedObjectAPIVersion: "other.example.io/v1", Reason: "OtherGroup", EventTime: now},
	}
	if err := d.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	got, err := d.GetEventsForObject("c1", EventObject{Namespace: "prod", Name: "db", Kind: "Cluster", APIVersion: "postgres.example.io/v1"})
	if err != nil {
		t.Fatal(err)
	}
	if r := reasons(got); len(r) != 1 || !r["OldVersion"] {
		t.Fatalf("got %v, want only the postgres.example.io event", r)
	}
}

// events.k8s.io events have an empty lastTimestamp; ordering on it first put
// all of them after every legacy event, past the incidents timeline's limit.
func TestGetClusterEventsRanksNewAPIEventsByEventTime(t *testing.T) {
	d := newTestDB(t)
	now := time.Now()
	rows := []K8sEvent{
		{Cluster: "c1", UID: "legacy-old", LastTimestamp: now.Add(-time.Hour), EventTime: now.Add(-time.Hour)},
		{Cluster: "c1", UID: "new-api", EventTime: now},
		{Cluster: "c1", UID: "legacy-mid", LastTimestamp: now.Add(-time.Minute), EventTime: now.Add(-time.Minute)},
	}
	if err := d.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	got, err := d.GetClusterEvents("c1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].UID != "new-api" || got[1].UID != "legacy-mid" {
		t.Fatalf("got %+v, want [new-api legacy-mid]", got)
	}
}
