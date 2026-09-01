package crossplane

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

const maxConcurrency = 10

type ResourceNode struct {
	APIVersion string                 `json:"apiVersion"`
	Kind       string                 `json:"kind"`
	Name       string                 `json:"name"`
	Namespace  string                 `json:"namespace,omitempty"`
	Status     string                 `json:"status"`
	Ready      bool                   `json:"ready"`
	Synced     bool                   `json:"synced"`
	Message    string                 `json:"message,omitempty"`
	Children   []ResourceNode         `json:"children,omitempty"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
}

type Tracer struct {
	dynamicClient dynamic.Interface
}

func NewTracer(dynamicClient dynamic.Interface) *Tracer {
	return &Tracer{
		dynamicClient: dynamicClient,
	}
}

func (t *Tracer) Trace(ctx context.Context, gvr schema.GroupVersionResource, namespace, name string) (*ResourceNode, error) {
	// Get the root resource
	var rootResource *unstructured.Unstructured
	var err error

	if namespace != "" {
		rootResource, err = t.dynamicClient.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	} else {
		rootResource, err = t.dynamicClient.Resource(gvr).Get(ctx, name, metav1.GetOptions{})
	}

	if err != nil {
		return nil, fmt.Errorf("failed to get resource: %w", err)
	}

	// Build the trace tree
	return t.buildResourceNode(ctx, rootResource)
}

func (t *Tracer) buildResourceNode(ctx context.Context, resource *unstructured.Unstructured) (*ResourceNode, error) {
	node := &ResourceNode{
		APIVersion: resource.GetAPIVersion(),
		Kind:       resource.GetKind(),
		Name:       resource.GetName(),
		Namespace:  resource.GetNamespace(),
		Metadata:   make(map[string]interface{}),
	}

	// Extract status information
	t.extractStatus(resource, node)

	// Find related resources based on the resource type
	children, err := t.findRelatedResources(ctx, resource)
	if err != nil {
		// Don't fail the whole trace if we can't find children
		node.Message = fmt.Sprintf("Warning: failed to find related resources: %v", err)
	} else {
		node.Children = children
	}

	// Add useful metadata
	t.extractMetadata(resource, node)

	return node, nil
}

func (t *Tracer) extractStatus(resource *unstructured.Unstructured, node *ResourceNode) {
	// Extract common status fields
	status, found, _ := unstructured.NestedMap(resource.Object, "status")
	if !found {
		node.Status = "Unknown"
		return
	}

	// Check for Ready and Synced conditions
	conditions, _, _ := unstructured.NestedSlice(status, "conditions")
	var readyFound, syncedFound bool
	var statusReason, statusMessage string

	for _, c := range conditions {
		if cond, ok := c.(map[string]interface{}); ok {
			condType, _, _ := unstructured.NestedString(cond, "type")
			condStatus, _, _ := unstructured.NestedString(cond, "status")
			reason, _, _ := unstructured.NestedString(cond, "reason")
			message, _, _ := unstructured.NestedString(cond, "message")

			switch condType {
			case "Ready":
				node.Ready = condStatus == "True"
				readyFound = true
				if reason != "" && statusReason == "" {
					statusReason = reason
				}
				if message != "" && statusMessage == "" {
					statusMessage = message
				}
			case "Synced":
				node.Synced = condStatus == "True"
				syncedFound = true
				// Prefer Synced condition messages if not Ready
				if !node.Ready && reason != "" {
					statusReason = reason
				}
				if !node.Ready && message != "" {
					statusMessage = message
				}
			}
		}
	}

	// Set status based on what we found
	if statusReason != "" {
		node.Status = statusReason
	} else if readyFound && syncedFound {
		if node.Ready && node.Synced {
			node.Status = "Available"
		} else if !node.Ready {
			node.Status = "NotReady"
		} else if !node.Synced {
			node.Status = "OutOfSync"
		}
	} else if readyFound {
		if node.Ready {
			node.Status = "Ready"
		} else {
			node.Status = "NotReady"
		}
	} else {
		node.Status = "Unknown"
	}

	// Set message
	if statusMessage != "" {
		node.Message = statusMessage
	}

	// For Crossplane specific resources
	if strings.Contains(resource.GetAPIVersion(), "crossplane.io") {
		// Check for composite resource references
		if compRef, found, _ := unstructured.NestedMap(status, "compositeResource"); found {
			apiVersion, _, _ := unstructured.NestedString(compRef, "apiVersion")
			kind, _, _ := unstructured.NestedString(compRef, "kind")
			name, _, _ := unstructured.NestedString(compRef, "name")
			if apiVersion != "" && kind != "" && name != "" {
				node.Metadata["compositeResource"] = map[string]string{
					"apiVersion": apiVersion,
					"kind":       kind,
					"name":       name,
				}
			}
		}
	}
}

type resourceRef struct {
	apiVersion string
	kind       string
	name       string
	namespace  string
}

func (t *Tracer) findRelatedResources(ctx context.Context, resource *unstructured.Unstructured) ([]ResourceNode, error) {
	var refs []resourceRef
	spec, _, _ := unstructured.NestedMap(resource.Object, "spec")
	status, _, _ := unstructured.NestedMap(resource.Object, "status")
	parentNs := resource.GetNamespace()

	extractRefs := func(slice []interface{}) {
		for _, ref := range slice {
			if refMap, ok := ref.(map[string]interface{}); ok {
				apiVersion, _, _ := unstructured.NestedString(refMap, "apiVersion")
				kind, _, _ := unstructured.NestedString(refMap, "kind")
				name, _, _ := unstructured.NestedString(refMap, "name")
				namespace, _, _ := unstructured.NestedString(refMap, "namespace")
				if apiVersion != "" && kind != "" && name != "" {
					ns := namespace
					if ns == "" {
						ns = parentNs
					}
					refs = append(refs, resourceRef{apiVersion, kind, name, ns})
				}
			}
		}
	}

	if claimRef, found, _ := unstructured.NestedMap(spec, "resourceRef"); found {
		apiVersion, _, _ := unstructured.NestedString(claimRef, "apiVersion")
		kind, _, _ := unstructured.NestedString(claimRef, "kind")
		name, _, _ := unstructured.NestedString(claimRef, "name")
		namespace, _, _ := unstructured.NestedString(claimRef, "namespace")
		if apiVersion != "" && kind != "" && name != "" {
			ns := namespace
			if ns == "" {
				ns = parentNs
			}
			refs = append(refs, resourceRef{apiVersion, kind, name, ns})
		}
	}

	if specRefs, found, _ := unstructured.NestedSlice(spec, "resourceRefs"); found {
		extractRefs(specRefs)
	}

	if crossplane, found, _ := unstructured.NestedMap(spec, "crossplane"); found {
		if modernRefs, found, _ := unstructured.NestedSlice(crossplane, "resourceRefs"); found {
			extractRefs(modernRefs)
		}
	}

	if statusRefs, found, _ := unstructured.NestedSlice(status, "resources"); found {
		extractRefs(statusRefs)
	}

	if len(refs) == 0 {
		return nil, nil
	}

	var (
		wg      sync.WaitGroup
		sem     = make(chan struct{}, maxConcurrency)
		results = make([]*ResourceNode, len(refs))
	)

	for i, ref := range refs {
		wg.Add(1)
		go func(idx int, r resourceRef) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			gv, err := schema.ParseGroupVersion(r.apiVersion)
			if err != nil {
				return
			}

			gvr := schema.GroupVersionResource{
				Group:    gv.Group,
				Version:  gv.Version,
				Resource: strings.ToLower(r.kind) + "s",
			}

			var managedResource *unstructured.Unstructured
			if r.namespace != "" {
				managedResource, err = t.dynamicClient.Resource(gvr).Namespace(r.namespace).Get(ctx, r.name, metav1.GetOptions{})
			} else {
				managedResource, err = t.dynamicClient.Resource(gvr).Get(ctx, r.name, metav1.GetOptions{})
			}

			if err != nil || managedResource == nil {
				return
			}

			childNode, err := t.buildResourceNode(ctx, managedResource)
			if err != nil {
				return
			}

			results[idx] = childNode
		}(i, ref)
	}

	wg.Wait()

	children := make([]ResourceNode, 0, len(refs))
	for _, node := range results {
		if node != nil {
			children = append(children, *node)
		}
	}
	sort.Slice(children, func(i, j int) bool {
		if children[i].Kind != children[j].Kind {
			return children[i].Kind < children[j].Kind
		}
		return children[i].Name < children[j].Name
	})
	return children, nil
}

func (t *Tracer) extractMetadata(resource *unstructured.Unstructured, node *ResourceNode) {
	// Add generation info
	node.Metadata["generation"] = resource.GetGeneration()

	// Add important labels
	labels := resource.GetLabels()
	if len(labels) > 0 {
		importantLabels := make(map[string]string)
		for k, v := range labels {
			if strings.Contains(k, "crossplane.io") {
				importantLabels[k] = v
			}
		}
		if len(importantLabels) > 0 {
			node.Metadata["labels"] = importantLabels
		}
	}

	// Add important annotations
	annotations := resource.GetAnnotations()
	if len(annotations) > 0 {
		importantAnnotations := make(map[string]string)
		for k, v := range annotations {
			if strings.Contains(k, "crossplane.io") {
				importantAnnotations[k] = v
			}
		}
		if len(importantAnnotations) > 0 {
			node.Metadata["annotations"] = importantAnnotations
		}
	}

	// Add provider info if available
	spec, _, _ := unstructured.NestedMap(resource.Object, "spec")
	if providerRef, found, _ := unstructured.NestedMap(spec, "providerConfigRef"); found {
		node.Metadata["providerConfigRef"] = providerRef
	}
}
