package health

import corev1 "k8s.io/api/core/v1"

// Workload is a pod template and the object that owns it, for other
// packages (the CIS benchmark) that evaluate the same templates the audit
// does. Jobs owned by a CronJob are folded into the CronJob.
type Workload struct {
	Ref  ResourceRef
	Spec *corev1.PodSpec
}

// Workloads lists the snapshot's pod templates.
func Workloads(s *Snapshot) []Workload {
	ws := workloads(s, newOwners(s))
	out := make([]Workload, len(ws))
	for i, w := range ws {
		out[i] = Workload{Ref: w.Ref, Spec: w.Spec}
	}
	return out
}

// IsSystemNamespace reports whether ns belongs to the platform.
func IsSystemNamespace(ns string) bool { return isSystemNamespace(ns) }
