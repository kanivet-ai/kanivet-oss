package topology

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func i64(obj map[string]interface{}, path ...string) int64 {
	v, _, _ := unstructured.NestedInt64(obj, path...)
	return v
}

func str(obj map[string]interface{}, path ...string) string {
	v, _, _ := unstructured.NestedString(obj, path...)
	return v
}

func replicaHealth(ready, desired int64) (Health, string) {
	s := fmt.Sprintf("%d/%d ready", ready, desired)
	switch {
	case desired == 0:
		return HealthUnknown, "scaled to 0"
	case ready >= desired:
		return HealthHealthy, s
	case ready == 0:
		return HealthError, s
	default:
		return HealthWarning, s
	}
}

func conditionTrue(u *unstructured.Unstructured, condType string) (found, isTrue bool) {
	conds, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	for _, c := range conds {
		m, ok := c.(map[string]interface{})
		if !ok || m["type"] != condType {
			continue
		}
		return true, m["status"] == "True"
	}
	return false, false
}

// healthOf derives a coarse health and short status for the kinds the engine
// knows; anything else is HealthUnknown with no status.
func healthOf(u *unstructured.Unstructured) (Health, string) {
	o := u.Object
	switch u.GetKind() {
	case "Pod":
		return podHealth(u)
	case "Deployment", "StatefulSet", "ReplicaSet":
		desired := i64(o, "spec", "replicas")
		if _, ok, _ := unstructured.NestedInt64(o, "spec", "replicas"); !ok {
			desired = 1
		}
		return replicaHealth(i64(o, "status", "readyReplicas"), desired)
	case "DaemonSet":
		return replicaHealth(i64(o, "status", "numberReady"), i64(o, "status", "desiredNumberScheduled"))
	case "Job":
		if f, t := conditionTrue(u, "Complete"); f && t {
			return HealthHealthy, "Complete"
		}
		if f, t := conditionTrue(u, "Failed"); f && t {
			return HealthError, "Failed"
		}
		if i64(o, "status", "active") > 0 {
			return HealthHealthy, "Running"
		}
		return HealthUnknown, "Pending"
	case "CronJob":
		if b, _, _ := unstructured.NestedBool(o, "spec", "suspend"); b {
			return HealthUnknown, "Suspended"
		}
		return HealthHealthy, str(o, "spec", "schedule")
	case "PersistentVolumeClaim", "PersistentVolume":
		switch p := str(o, "status", "phase"); p {
		case "Bound", "Available":
			return HealthHealthy, p
		case "Pending", "Released":
			return HealthWarning, p
		case "Lost", "Failed":
			return HealthError, p
		default:
			return HealthUnknown, p
		}
	case "Node":
		if f, t := conditionTrue(u, "Ready"); f {
			if t {
				if b, _, _ := unstructured.NestedBool(o, "spec", "unschedulable"); b {
					return HealthWarning, "Cordoned"
				}
				return HealthHealthy, "Ready"
			}
			return HealthError, "NotReady"
		}
		return HealthUnknown, ""
	case "HorizontalPodAutoscaler":
		cur, desired := i64(o, "status", "currentReplicas"), i64(o, "status", "desiredReplicas")
		max := i64(o, "spec", "maxReplicas")
		st := fmt.Sprintf("%d replicas (max %d)", cur, max)
		if f, t := conditionTrue(u, "ScalingActive"); f && !t {
			return HealthWarning, st
		}
		if max > 0 && desired >= max {
			return HealthWarning, "at max " + fmt.Sprint(max)
		}
		return HealthHealthy, st
	case "PodDisruptionBudget":
		allowed := i64(o, "status", "disruptionsAllowed")
		if allowed == 0 && i64(o, "status", "expectedPods") > 0 {
			return HealthWarning, "0 disruptions allowed"
		}
		return HealthHealthy, fmt.Sprintf("%d disruptions allowed", allowed)
	case "Service":
		return HealthHealthy, str(o, "spec", "type")
	case "Gateway":
		if f, t := conditionTrue(u, "Programmed"); f {
			if t {
				return HealthHealthy, "Programmed"
			}
			return HealthError, "Not programmed"
		}
	case "HTTPRoute":
		parents, _, _ := unstructured.NestedSlice(o, "status", "parents")
		for _, p := range parents {
			pm, _ := p.(map[string]interface{})
			conds, _, _ := unstructured.NestedSlice(pm, "conditions")
			for _, c := range conds {
				cm, _ := c.(map[string]interface{})
				if cm["type"] == "Accepted" && cm["status"] == "False" {
					return HealthError, "Not accepted"
				}
			}
		}
		if len(parents) > 0 {
			return HealthHealthy, "Accepted"
		}
	}
	return HealthUnknown, ""
}

func podHealth(u *unstructured.Unstructured) (Health, string) {
	o := u.Object
	phase := str(o, "status", "phase")
	if u.GetDeletionTimestamp() != nil {
		return HealthWarning, "Terminating"
	}
	statuses, _, _ := unstructured.NestedSlice(o, "status", "containerStatuses")
	ready, total := 0, len(statuses)
	for _, s := range statuses {
		m, _ := s.(map[string]interface{})
		if r, _ := m["ready"].(bool); r {
			ready++
		}
		if reason := str(m, "state", "waiting", "reason"); reason != "" && reason != "ContainerCreating" && reason != "PodInitializing" {
			return HealthError, reason
		}
		if reason := str(m, "state", "terminated", "reason"); reason == "Error" || reason == "OOMKilled" {
			if phase != "Succeeded" {
				return HealthError, reason
			}
		}
	}
	switch phase {
	case "Running":
		if total > 0 && ready < total {
			return HealthWarning, fmt.Sprintf("Running %d/%d", ready, total)
		}
		return HealthHealthy, "Running"
	case "Succeeded":
		return HealthHealthy, "Completed"
	case "Pending":
		return HealthWarning, "Pending"
	case "Failed":
		if r := str(o, "status", "reason"); r != "" {
			return HealthError, r
		}
		return HealthError, "Failed"
	}
	return HealthUnknown, phase
}
