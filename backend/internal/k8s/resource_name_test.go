package k8s

import (
	"errors"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	discoveryfake "k8s.io/client-go/discovery/fake"
	clienttesting "k8s.io/client-go/testing"
)

// flakyDiscovery fails every lookup while down is set.
type flakyDiscovery struct {
	*discoveryfake.FakeDiscovery
	down bool
}

func (f *flakyDiscovery) ServerResourcesForGroupVersion(gv string) (*metav1.APIResourceList, error) {
	if f.down {
		return nil, errors.New("discovery unavailable")
	}
	return f.FakeDiscovery.ServerResourcesForGroupVersion(gv)
}

// A plural guessed while discovery was failing is only a stopgap: once it
// expires discovery is asked again, so a custom resource with an irregular
// plural is not watched and edited at the wrong endpoint for good.
func TestGetResourceNameRetriesAGuessedPlural(t *testing.T) {
	c := newTestClient()
	d := &flakyDiscovery{down: true, FakeDiscovery: &discoveryfake.FakeDiscovery{Fake: &clienttesting.Fake{
		Resources: []*metav1.APIResourceList{{
			GroupVersion: "zoo.example.com/v1",
			APIResources: []metav1.APIResource{{Name: "octopi", Kind: "Octopus"}},
		}},
	}}}
	c.discovery["c1"] = d
	name := func() string { return c.GetResourceName("c1", "zoo.example.com", "v1", "Octopus") }

	if got := name(); got != "octopuses" {
		t.Fatalf("guess while discovery is down = %q, want octopuses", got)
	}
	d.down = false
	if got := name(); got != "octopuses" {
		t.Fatalf("a fresh guess should be reused rather than hitting discovery on every call, got %q", got)
	}

	c.resourceNameMu.Lock()
	for k := range c.resourceNameGuesses {
		c.resourceNameGuesses[k] = time.Now().Add(-time.Second)
	}
	c.resourceNameMu.Unlock()

	if got := name(); got != "octopi" {
		t.Fatalf("expired guess was not resolved again: got %q, want octopi", got)
	}
	d.down = true
	if got := name(); got != "octopi" {
		t.Fatalf("a resolved name must stay cached, got %q", got)
	}
}
