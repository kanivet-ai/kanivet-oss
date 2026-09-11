package printercolumns

import (
	"context"
	"reflect"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

func tenantV2CRD() map[string]interface{} {
	return map[string]interface{}{
		"spec": map[string]interface{}{
			"versions": []interface{}{
				map[string]interface{}{
					"name":    "v1alpha1",
					"served":  true,
					"storage": true,
					"additionalPrinterColumns": []interface{}{
						map[string]interface{}{
							"name":     "INFRA",
							"type":     "string",
							"jsonPath": ".status.conditions[?(@.type=='InfrastructureReady')].status",
						},
						map[string]interface{}{
							"name":     "APPS",
							"type":     "string",
							"jsonPath": ".status.conditions[?(@.type=='ApplicationsReady')].status",
						},
						map[string]interface{}{
							"name":     "APPS-REASON",
							"type":     "string",
							"priority": float64(1),
							"jsonPath": ".status.conditions[?(@.type=='ApplicationsReady')].reason",
						},
						map[string]interface{}{
							"name":     "SYNCED",
							"type":     "string",
							"jsonPath": ".status.conditions[?(@.type=='Synced')].status",
						},
						map[string]interface{}{
							"name":     "READY",
							"type":     "string",
							"jsonPath": ".status.conditions[?(@.type=='Ready')].status",
						},
						map[string]interface{}{
							"name":     "COMPOSITION",
							"type":     "string",
							"jsonPath": ".spec.compositionRef.name",
						},
						map[string]interface{}{
							"name":     "AGE",
							"type":     "date",
							"jsonPath": ".metadata.creationTimestamp",
						},
					},
				},
			},
		},
	}
}

func tenantObject() map[string]interface{} {
	return map[string]interface{}{
		"apiVersion": "cloud.physicsx.ai/v1alpha1",
		"kind":       "TenantV2",
		"metadata": map[string]interface{}{
			"name":              "demo-lab-az-1",
			"creationTimestamp": "2026-06-07T00:00:00Z",
		},
		"spec": map[string]interface{}{
			"compositionRef": map[string]interface{}{"name": "tenant-v2"},
		},
		"status": map[string]interface{}{
			"conditions": []interface{}{
				map[string]interface{}{"type": "InfrastructureReady", "status": "True"},
				map[string]interface{}{"type": "ApplicationsReady", "status": "False", "reason": "AppsPending"},
				map[string]interface{}{"type": "Synced", "status": "True"},
				map[string]interface{}{"type": "Ready", "status": "False"},
			},
		},
	}
}

func TestParseFromCRD(t *testing.T) {
	cols := ParseFromCRD(tenantV2CRD(), "v1alpha1")
	if len(cols) != 7 {
		t.Fatalf("got %d columns: %+v", len(cols), cols)
	}
	if cols[0].Name != "INFRA" || cols[2].Priority != 1 || cols[5].JSONPath != ".spec.compositionRef.name" {
		t.Fatalf("unexpected columns: %+v", cols)
	}
}

func TestEvaluateTenantPrinterColumns(t *testing.T) {
	cols := ParseFromCRD(tenantV2CRD(), "v1alpha1")
	cells := Evaluate(tenantObject(), cols)
	got := map[string]string{}
	for _, c := range cells {
		got[c.Name] = c.Value
	}
	want := map[string]string{
		"INFRA":       "True",
		"APPS":        "False",
		"APPS-REASON": "AppsPending",
		"SYNCED":      "True",
		"READY":       "False",
		"COMPOSITION": "tenant-v2",
		"AGE":         "2026-06-07T00:00:00Z",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("cells mismatch\ngot  %#v\nwant %#v", got, want)
	}
}

func TestEvaluateMissingCondition(t *testing.T) {
	cols := []Column{{
		Name:     "INFRA",
		Type:     "string",
		JSONPath: ".status.conditions[?(@.type=='InfrastructureReady')].status",
	}}
	obj := map[string]interface{}{
		"status": map[string]interface{}{
			"conditions": []interface{}{
				map[string]interface{}{"type": "Ready", "status": "True"},
			},
		},
	}
	cells := Evaluate(obj, cols)
	if len(cells) != 1 || cells[0].Value != "" {
		t.Fatalf("expected empty missing condition, got %+v", cells)
	}
}

func TestAttach(t *testing.T) {
	item := map[string]interface{}{"name": "x"}
	Attach(item, []Cell{{Name: "INFRA", Type: "string", Value: "True"}})
	raw, ok := item["printerColumns"].([]interface{})
	if !ok || len(raw) != 1 {
		t.Fatalf("printerColumns not attached: %+v", item)
	}
	row, _ := raw[0].(map[string]interface{})
	if row["name"] != "INFRA" || row["value"] != "True" {
		t.Fatalf("bad cell: %+v", row)
	}
}

func TestResolverCachesCRD(t *testing.T) {
	calls := 0
	r := NewResolver(func(ctx context.Context, cluster, crdName string) (map[string]interface{}, error) {
		calls++
		if crdName != "tenantsv2.cloud.physicsx.ai" {
			t.Fatalf("unexpected crd name %q", crdName)
		}
		return tenantV2CRD(), nil
	})
	gvr := schema.GroupVersionResource{Group: "cloud.physicsx.ai", Version: "v1alpha1", Resource: "tenantsv2"}
	first := r.Columns(context.Background(), "mgmt", gvr)
	second := r.Columns(context.Background(), "mgmt", gvr)
	if calls != 1 {
		t.Fatalf("expected 1 CRD fetch, got %d", calls)
	}
	if len(first) != 7 || len(second) != 7 {
		t.Fatalf("unexpected column counts %d %d", len(first), len(second))
	}
}

func TestResolverNegativeCache(t *testing.T) {
	calls := 0
	r := NewResolver(func(ctx context.Context, cluster, crdName string) (map[string]interface{}, error) {
		calls++
		return nil, context.DeadlineExceeded
	})
	now := time.Now()
	r.now = func() time.Time { return now }
	gvr := schema.GroupVersionResource{Group: "example.com", Version: "v1", Resource: "widgets"}
	if cols := r.Columns(context.Background(), "c", gvr); cols != nil {
		t.Fatalf("expected nil columns, got %+v", cols)
	}
	if cols := r.Columns(context.Background(), "c", gvr); cols != nil {
		t.Fatalf("expected cached nil, got %+v", cols)
	}
	if calls != 1 {
		t.Fatalf("expected 1 fetch, got %d", calls)
	}
}

func TestResolverSkipsCoreTypes(t *testing.T) {
	r := NewResolver(func(ctx context.Context, cluster, crdName string) (map[string]interface{}, error) {
		t.Fatal("should not fetch core types")
		return nil, nil
	})
	gvr := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}
	if cols := r.Columns(context.Background(), "c", gvr); cols != nil {
		t.Fatalf("expected nil for core type, got %+v", cols)
	}
}
