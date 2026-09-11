package watcher

import (
	"context"
	"testing"
	"time"

	"github.com/kanivet/backend/internal/k8s/printercolumns"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestListSyncAttachesPrinterColumns(t *testing.T) {
	hub := &captureBroadcaster{sortBy: "age", sortOrder: "desc"}
	s := newServiceForResync(hub)
	s.countThrottler = newCountThrottler(time.Hour, func(string, string, string, int) {})
	s.printerCols = printercolumns.NewResolver(func(ctx context.Context, cluster, crdName string) (map[string]interface{}, error) {
		if crdName != "tenantsv2.cloud.physicsx.ai" {
			t.Fatalf("unexpected CRD %q", crdName)
		}
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
								"name":     "COMPOSITION",
								"type":     "string",
								"jsonPath": ".spec.compositionRef.name",
							},
						},
					},
				},
			},
		}, nil
	})

	lister := &fakeListLister{items: []unstructured.Unstructured{{
		Object: map[string]interface{}{
			"apiVersion": "cloud.physicsx.ai/v1alpha1",
			"kind":       "TenantV2",
			"metadata": map[string]interface{}{
				"name":              "demo-lab-az-1",
				"uid":               "uid-1",
				"resourceVersion":   "1",
				"creationTimestamp": "2026-06-07T00:00:00Z",
			},
			"spec": map[string]interface{}{
				"compositionRef": map[string]interface{}{"name": "tenant-v2"},
			},
			"status": map[string]interface{}{
				"conditions": []interface{}{
					map[string]interface{}{"type": "InfrastructureReady", "status": "True"},
				},
			},
		},
	}}}
	gvr := schema.GroupVersionResource{Group: "cloud.physicsx.ai", Version: "v1alpha1", Resource: "tenantsv2"}
	topic := "items:cluster-a:cloud.physicsx.ai:v1alpha1:tenantsv2:"

	if _, err := s.fetchAndBroadcastListSync(context.Background(), "cluster-a", gvr, "", topic, lister); err != nil {
		t.Fatal(err)
	}

	var found map[string]interface{}
	for _, page := range hub.bulkLists() {
		for _, item := range page.Items {
			if item["name"] == "demo-lab-az-1" {
				if _, ok := item["printerColumns"]; ok {
					found = item
				}
			}
		}
	}
	if found == nil {
		t.Fatal("expected listed tenant to include printerColumns")
	}
	cells, _ := found["printerColumns"].([]interface{})
	values := map[string]string{}
	for _, raw := range cells {
		cell, _ := raw.(map[string]interface{})
		name, _ := cell["name"].(string)
		val, _ := cell["value"].(string)
		values[name] = val
	}
	if values["INFRA"] != "True" || values["COMPOSITION"] != "tenant-v2" {
		t.Fatalf("unexpected printer cells: %#v", values)
	}
}

func TestPrinterColumnsSkippedForAdapters(t *testing.T) {
	hub := &captureBroadcaster{sortBy: "age", sortOrder: "desc"}
	s := newServiceForResync(hub)
	s.printerCols = printercolumns.NewResolver(func(ctx context.Context, cluster, crdName string) (map[string]interface{}, error) {
		t.Fatal("should not fetch CRDs for kinds with list adapters")
		return nil, nil
	})
	lister := &fakeListLister{items: []unstructured.Unstructured{mkListObj("d-1")}}
	gvr := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	topic := "items:cluster-a:apps:v1:deployments:ns"
	if _, err := s.fetchAndBroadcastListSync(context.Background(), "cluster-a", gvr, "ns", topic, lister); err != nil {
		t.Fatal(err)
	}
}
