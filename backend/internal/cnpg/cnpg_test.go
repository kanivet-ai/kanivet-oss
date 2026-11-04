package cnpg

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynfake "k8s.io/client-go/dynamic/fake"
)

func pgCluster(backup bool) *unstructured.Unstructured {
	spec := map[string]any{"instances": int64(3), "storage": map[string]any{"size": "10Gi"}}
	if backup {
		spec["backup"] = map[string]any{"barmanObjectStore": map[string]any{"destinationPath": "s3://b"}}
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "postgresql.cnpg.io/v1",
		"kind":       "Cluster",
		"metadata":   map[string]any{"name": "pg", "namespace": "db"},
		"spec":       spec,
		"status": map[string]any{
			"phase":          "Cluster in healthy state",
			"readyInstances": int64(3),
			"currentPrimary": "pg-1",
			"targetPrimary":  "pg-1",
			"timelineID":     int64(2),
			"instanceNames":  []any{"pg-1", "pg-2", "pg-3"},
			"instancesStatus": map[string]any{
				"healthy": []any{"pg-1", "pg-2"},
				"failed":  []any{"pg-3"},
			},
			"lastSuccessfulBackup": "2026-11-03T00:00:00Z",
		},
	}}
}

func backupObj(name, cluster, phase, started string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "postgresql.cnpg.io/v1", "kind": "Backup",
		"metadata": map[string]any{"name": name, "namespace": "db"},
		"spec":     map[string]any{"cluster": map[string]any{"name": cluster}},
		"status":   map[string]any{"phase": phase, "startedAt": started, "method": "barmanObjectStore"},
	}}
}

var det = &Detection{Installed: true, Kinds: []Kind{
	{Kind: "Cluster", Plural: "clusters", Version: "v1"},
	{Kind: "Backup", Plural: "backups", Version: "v1"},
	{Kind: "ScheduledBackup", Plural: "scheduledbackups", Version: "v1"},
	{Kind: "Pooler", Plural: "poolers", Version: "v1"},
}}

func newFake(objs ...runtime.Object) *dynfake.FakeDynamicClient {
	gvrs := map[schema.GroupVersionResource]string{}
	for _, k := range det.Kinds {
		gvrs[k.GVR()] = k.Kind + "List"
	}
	return dynfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), gvrs, objs...)
}

func TestParseClusterAndInstances(t *testing.T) {
	s := ParseCluster(pgCluster(true))
	if s.Health != HealthHealthy || s.Instances != 3 || s.Timeline != 2 || s.BackupMethod != "barmanObjectStore" {
		t.Fatalf("summary: %+v", s)
	}
	in := ParseInstances(pgCluster(true))
	if len(in) != 3 || in[0].Name != "pg-1" || in[0].Role != "primary" || in[2].Status != "failed" {
		t.Fatalf("instances: %+v", in)
	}
	merged := MergePods(in, []PodFacts{{Name: "pg-2", Node: "n2", Ready: true}, {Name: "pg-4", Ready: false}})
	if len(merged) != 4 || merged[1].Node != "n2" || !merged[1].Ready {
		t.Fatalf("merged: %+v", merged)
	}
}

func TestClusterStatusFiltersByCluster(t *testing.T) {
	dyn := newFake(pgCluster(true),
		backupObj("b-old", "pg", "completed", "2026-11-01T00:00:00Z"),
		backupObj("b-new", "pg", "running", "2026-11-04T00:00:00Z"),
		backupObj("b-other", "other", "completed", "2026-11-04T00:00:00Z"),
	)
	lag := 1.5
	st, err := GetClusterStatus(context.Background(), dyn, det, "db", "pg", Deps{
		Pods: func(context.Context, string, string) ([]PodFacts, error) {
			return []PodFacts{{Name: "pg-1", Ready: true}, {Name: "pg-2", Ready: true}, {Name: "pg-3"}}, nil
		},
		Lag: func(_ context.Context, _ string, pod string) (float64, error) { return lag, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Backups) != 2 || st.Backups[0].Name != "b-new" {
		t.Fatalf("backups: %+v", st.Backups)
	}
	if st.Instances[1].LagSeconds == nil || *st.Instances[1].LagSeconds != lag || st.Instances[0].LagSeconds != nil || st.Instances[2].LagSeconds != nil {
		t.Fatalf("lag only on ready replicas: %+v", st.Instances)
	}
	if !st.BackupConfigured {
		t.Fatal("backup configured")
	}
}

func TestBackupNow(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	if _, err := BackupNow(ctx, newFake(pgCluster(false)), det, "db", "pg", now); err == nil {
		t.Fatal("a cluster without backup config must be refused")
	}
	dyn := newFake(pgCluster(true))
	res, err := BackupNow(ctx, dyn, det, "db", "pg", now)
	if err != nil {
		t.Fatal(err)
	}
	if res.Name != "pg-20261104120000" || res.Method != "barmanObjectStore" {
		t.Fatalf("result: %+v", res)
	}
	b, err := dyn.Resource(det.Kinds[1].GVR()).Namespace("db").Get(ctx, res.Name, metav1.GetOptions{})
	if err != nil || clusterOf(b) != "pg" {
		t.Fatalf("created backup: %v %v", b, err)
	}
}

func TestCheckPromote(t *testing.T) {
	c := pgCluster(true)
	if err := CheckPromote(c, "pg-2"); err != nil {
		t.Fatalf("valid promote refused: %v", err)
	}
	if err := CheckPromote(c, "pg-1"); err == nil {
		t.Fatal("promoting the primary must be refused")
	}
	if err := CheckPromote(c, "pg-9"); err == nil {
		t.Fatal("unknown instance must be refused")
	}
	_ = unstructured.SetNestedField(c.Object, "pg-3", "status", "targetPrimary")
	if err := CheckPromote(c, "pg-2"); err == nil {
		t.Fatal("promote during a switchover must be refused")
	}
}

func TestRestart(t *testing.T) {
	ctx := context.Background()
	dyn := newFake(pgCluster(true))
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	if err := Restart(ctx, dyn, det, "db", "pg", now); err != nil {
		t.Fatal(err)
	}
	got, _ := dyn.Resource(det.Kinds[0].GVR()).Namespace("db").Get(ctx, "pg", metav1.GetOptions{})
	if got.GetAnnotations()[RestartAnnotation] != "2026-11-04T12:00:00Z" {
		t.Fatalf("annotations: %v", got.GetAnnotations())
	}
}

func TestParseReplicationLag(t *testing.T) {
	text := "# HELP cnpg_pg_replication_lag lag\n# TYPE cnpg_pg_replication_lag gauge\ncnpg_pg_replication_lag_bytes 9\ncnpg_pg_replication_lag 0.25\n"
	if v, ok := ParseReplicationLag(text); !ok || v != 0.25 {
		t.Fatalf("got %v %v", v, ok)
	}
	if v, ok := ParseReplicationLag(`cnpg_pg_replication_lag{x="a b"} 3 1700000000`); !ok || v != 3 {
		t.Fatalf("labels+timestamp: %v %v", v, ok)
	}
	if _, ok := ParseReplicationLag("other 1"); ok {
		t.Fatal("absent metric")
	}
}
