package events

import (
	"testing"

	"github.com/kanivet/backend/internal/k8s"
	"k8s.io/client-go/kubernetes/fake"
)

func TestStopListeningStopsOnlyThatCluster(t *testing.T) {
	mock := &k8s.MockClient{TypedClient: fake.NewSimpleClientset()}
	el := NewEventListener(mock, nil)
	defer el.StopAll()

	if err := el.StartListening("cluster-a"); err != nil {
		t.Fatal(err)
	}
	if err := el.StartListening("cluster-b"); err != nil {
		t.Fatal(err)
	}
	if !el.IsListening("cluster-a") || !el.IsListening("cluster-b") {
		t.Fatal("both listeners must be active after start")
	}

	el.StopListening("cluster-a")

	if el.IsListening("cluster-a") {
		t.Fatal("cluster-a listener must be stopped")
	}
	if !el.IsListening("cluster-b") {
		t.Fatal("cluster-b listener must survive")
	}
}
