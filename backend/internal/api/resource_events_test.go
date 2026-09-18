package api

import "testing"

func TestEventsFieldSelectorIncludesKind(t *testing.T) {
	got := eventsFieldSelector("api", "prod", "Deployment")
	want := "involvedObject.name=api,involvedObject.namespace=prod,involvedObject.kind=Deployment"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestEventsFieldSelectorClusterScoped(t *testing.T) {
	got := eventsFieldSelector("node-1", "", "Node")
	want := "involvedObject.name=node-1,involvedObject.kind=Node"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestEventsFieldSelectorWithoutKind(t *testing.T) {
	got := eventsFieldSelector("api", "prod", "")
	want := "involvedObject.name=api,involvedObject.namespace=prod"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
