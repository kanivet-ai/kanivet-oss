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
	d := &DB{gdb}
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
