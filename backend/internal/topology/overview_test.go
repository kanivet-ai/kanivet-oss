package topology

import (
	"context"
	"errors"
	"testing"
)

func TestClusterOverview(t *testing.T) {
	ov, err := newTestService(fixture()).ClusterOverview(context.Background(), "c")
	if err != nil {
		t.Fatal(err)
	}
	if len(ov.Namespaces) != 2 || ov.Namespaces[0].Namespace != "ns" || ov.Namespaces[1].Namespace != "other" {
		t.Fatalf("namespaces = %+v", ov.Namespaces)
	}
	s := ov.Namespaces[0]
	if s.Pods.Healthy+s.Pods.Warning+s.Pods.Error+s.Pods.Unknown == 0 {
		t.Errorf("no pods counted: %+v", s)
	}
	if s.Workloads.Healthy+s.Workloads.Warning+s.Workloads.Error+s.Workloads.Unknown != 1 {
		t.Errorf("want only the top-level Deployment counted, got %+v", s.Workloads)
	}
	if s.Health == "" {
		t.Error("missing rollup health")
	}
}

func TestClusterOverviewAllFail(t *testing.T) {
	errs := map[string]error{}
	for _, k := range overviewKinds {
		errs[k] = errors.New("boom")
	}
	if _, err := newTestService(&fakeSource{errs: errs}).ClusterOverview(context.Background(), "c"); err == nil {
		t.Error("expected an error")
	}
}

func TestHealthCountsWorst(t *testing.T) {
	if (HealthCounts{Healthy: 2, Warning: 1}).Worst() != HealthWarning {
		t.Error("warning should win over healthy")
	}
	if (HealthCounts{}).Worst() != HealthUnknown {
		t.Error("empty is unknown")
	}
}
