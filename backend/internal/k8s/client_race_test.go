package k8s

import (
	"sync"
	"testing"

	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/rest"
)

func newTestClient() *Client {
	return &Client{
		configs:           make(map[string]*rest.Config),
		clients:           make(map[string]kubernetes.Interface),
		dynamic:           make(map[string]dynamic.Interface),
		interactive:       make(map[string]dynamic.Interface),
		metadata:          make(map[string]metadata.Interface),
		bulkMetadata:      make(map[string]metadata.Interface),
		discovery:         make(map[string]discovery.DiscoveryInterface),
		kubeconfigs:       []string{"/tmp/fake-kubeconfig"},
		resourceNameCache: make(map[string]string),
	}
}

func TestClientConcurrentAccess(t *testing.T) {
	c := newTestClient()
	clusters := []string{"cluster-a", "cluster-b", "cluster-c", "cluster-d"}
	const goroutines = 10
	const iterations = 50

	var wg sync.WaitGroup

	for i := range goroutines {
		cluster := clusters[i%len(clusters)]

		wg.Add(1)
		go func() {
			defer wg.Done()
			for range iterations {
				c.ListClusters()
			}
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			for range iterations {
				c.findKubeconfigForContext(cluster)
			}
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			for range iterations {
				if i%3 == 0 {
					c.RefreshClusterCache(cluster)
				} else {
					c.RefreshClusterCache("")
				}
			}
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			for range iterations {
				c.GetResourceName(cluster, "apps", "v1", "Deployment")
			}
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			for range iterations {
				c.GetClientForCluster(cluster)
			}
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			for range iterations {
				c.getConfigForCluster(cluster)
			}
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			for range iterations {
				c.GetDynamicClient(cluster)
			}
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			for range iterations {
				c.GetMetadataClient(cluster)
			}
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			for range iterations {
				c.GetDiscoveryClient(cluster)
			}
		}()
	}

	wg.Wait()
}
