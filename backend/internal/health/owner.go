package health

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// owners resolves a pod to the top-level workload that runs it without
// listing ReplicaSets: a Deployment's ReplicaSet is named after the
// Deployment plus the pod-template-hash the pod carries.
type owners struct {
	deployments map[string]bool // ns/name
	jobToCron   map[string]string
}

func newOwners(s *Snapshot) *owners {
	o := &owners{deployments: map[string]bool{}, jobToCron: map[string]string{}}
	for _, d := range s.Deployments {
		o.deployments[d.Namespace+"/"+d.Name] = true
	}
	for _, j := range s.Jobs {
		for _, ref := range j.OwnerReferences {
			if ref.Kind == "CronJob" {
				o.jobToCron[j.Namespace+"/"+j.Name] = ref.Name
			}
		}
	}
	return o
}

func controllerOf(refs []metav1.OwnerReference) *metav1.OwnerReference {
	for i := range refs {
		if refs[i].Controller != nil && *refs[i].Controller {
			return &refs[i]
		}
	}
	if len(refs) > 0 {
		return &refs[0]
	}
	return nil
}

func podRef(p *corev1.Pod) ResourceRef {
	return ResourceRef{APIVersion: "v1", Kind: "Pod", Namespace: p.Namespace, Name: p.Name}
}

// ownerOfPod returns the workload that owns the pod, or the pod itself.
func (o *owners) ownerOfPod(p *corev1.Pod) ResourceRef {
	ref := controllerOf(p.OwnerReferences)
	if ref == nil {
		return podRef(p)
	}
	switch ref.Kind {
	case "ReplicaSet":
		if hash := p.Labels["pod-template-hash"]; hash != "" && strings.HasSuffix(ref.Name, "-"+hash) {
			dep := strings.TrimSuffix(ref.Name, "-"+hash)
			if o.deployments == nil || len(o.deployments) == 0 || o.deployments[p.Namespace+"/"+dep] {
				return ResourceRef{APIVersion: "apps/v1", Kind: "Deployment", Namespace: p.Namespace, Name: dep}
			}
		}
		return ResourceRef{APIVersion: "apps/v1", Kind: "ReplicaSet", Namespace: p.Namespace, Name: ref.Name}
	case "Job":
		if cj, ok := o.jobToCron[p.Namespace+"/"+ref.Name]; ok {
			return ResourceRef{APIVersion: "batch/v1", Kind: "CronJob", Namespace: p.Namespace, Name: cj}
		}
		return ResourceRef{APIVersion: "batch/v1", Kind: "Job", Namespace: p.Namespace, Name: ref.Name}
	case "Node":
		// Static pods are their own owner.
		return podRef(p)
	}
	return ResourceRef{APIVersion: ref.APIVersion, Kind: ref.Kind, Namespace: p.Namespace, Name: ref.Name}
}
