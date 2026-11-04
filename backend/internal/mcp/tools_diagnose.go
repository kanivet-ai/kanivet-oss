package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/kanivet/backend/internal/mcp/mcpreg"
)

type diagnoseArgs struct {
	mcpreg.ClusterArg
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
	LogLines  int    `json:"logLines,omitempty" jsonschema:"log lines per failing container (default 40)"`
}

// Ref names an object; agents (and Kanivet's investigation view) can turn
// these into links.
type Ref struct {
	Kind       string `json:"kind"`
	APIVersion string `json:"apiVersion,omitempty"`
	Namespace  string `json:"ns,omitempty"`
	Name       string `json:"name"`
	Note       string `json:"note,omitempty"`
}

// Diagnosis is the diagnose result.
type Diagnosis struct {
	Object   Ref             `json:"object"`
	Health   string          `json:"health"` // healthy | degraded | failing | unknown
	Summary  Row             `json:"summary"`
	Findings []string        `json:"findings"`
	Owners   []Ref           `json:"owners,omitempty"`
	Related  []Ref           `json:"related,omitempty"`
	Events   []EventGroup    `json:"events,omitempty"`
	Logs     []containerLogs `json:"logs,omitempty"`
	Errors   []string        `json:"errors,omitempty"`
}

func (d *Deps) diagnose(ctx context.Context, call *mcpreg.Call, in diagnoseArgs) (any, error) {
	ki, err := d.resolveKind(call.Cluster, in.Kind)
	if err != nil {
		return nil, err
	}
	obj, err := d.fetch(ctx, call.Cluster, ki, in.Namespace, in.Name)
	if err != nil {
		return nil, err
	}
	md, _ := obj["metadata"].(map[string]any)
	ns := str(md, "namespace")
	dg := &Diagnosis{
		Object:  Ref{Kind: ki.Kind, APIVersion: ki.APIVersion(), Namespace: ns, Name: in.Name},
		Summary: Summarize(obj, time.Now()),
	}
	dg.Findings = append(dg.Findings, objectFindings(obj)...)

	// Owners (walk up to 4 levels).
	cur := obj
	for i := 0; i < 4; i++ {
		ors, _ := cur["metadata"].(map[string]any)["ownerReferences"].([]any)
		if len(ors) == 0 {
			break
		}
		or, _ := ors[0].(map[string]any)
		ref := Ref{Kind: str(or, "kind"), APIVersion: str(or, "apiVersion"), Namespace: ns, Name: str(or, "name")}
		dg.Owners = append(dg.Owners, ref)
		oki, err := d.resolveKind(call.Cluster, ref.Kind)
		if err != nil {
			break
		}
		next, err := d.fetch(ctx, call.Cluster, oki, ns, ref.Name)
		if err != nil {
			break
		}
		cur = next
	}

	logLines := in.LogLines
	if logLines <= 0 {
		logLines = 40
	}

	// Related objects and pods to inspect.
	var pods []corev1.Pod
	switch ki.Kind {
	case "Pod":
		if p, err := d.podsFor(ctx, call.Cluster, ns, "", in.Name); err == nil {
			pods = p
		}
	case "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Job":
		p, err := d.podsFor(ctx, call.Cluster, ns, ki.Kind, in.Name)
		if err != nil {
			dg.Errors = append(dg.Errors, "pods: "+err.Error())
		}
		pods = p
		for i, pod := range p {
			if i >= 10 {
				dg.Related = append(dg.Related, Ref{Kind: "Pod", Note: fmt.Sprintf("+%d more pods", len(p)-10)})
				break
			}
			r := Summarize(podMap(pod), time.Now())
			dg.Related = append(dg.Related, Ref{Kind: "Pod", APIVersion: "v1", Namespace: ns, Name: pod.Name, Note: r.Status + " " + r.Ready})
		}
	}
	if len(pods) > 0 {
		dg.Related = append(dg.Related, d.podRelations(ctx, call.Cluster, pods[0], ki.Kind == "Pod")...)
	}

	// Events of the object and of its unhealthy pods.
	evs, err := d.events(ctx, call.Cluster, ns, ki.Kind, in.Name, false, 24*time.Hour)
	if err != nil {
		dg.Errors = append(dg.Errors, "events: "+err.Error())
	}
	for i, p := range pods {
		if i >= 3 || (ki.Kind != "Pod" && podTrouble(&p) == 0) {
			continue
		}
		pe, _ := d.events(ctx, call.Cluster, ns, "Pod", p.Name, false, 24*time.Hour)
		evs = append(evs, pe...)
	}
	dg.Events = DedupeEvents(evs, time.Now())
	if len(dg.Events) > 25 {
		dg.Events = dg.Events[:25]
	}
	for _, e := range dg.Events {
		if e.Type == "Warning" {
			dg.Findings = append(dg.Findings, eventHints(e)...)
		}
	}

	// Pod container findings and failing logs.
	for i, p := range pods {
		if i >= 3 {
			break
		}
		f, failing := podFindings(&p)
		dg.Findings = append(dg.Findings, f...)
		for _, c := range failing {
			previous := c.RestartCount > 0 && c.State.Running == nil
			if c.LastTerminationState.Terminated != nil && c.State.Running != nil {
				previous = true
			}
			dg.Logs = append(dg.Logs, d.readLogs(ctx, call.Cluster, p, c.Name, int64(logLines), 0, previous, nil, false))
		}
	}

	dg.Findings = dedupeStrings(dg.Findings)
	dg.Health = verdict(dg)
	if dg.Findings == nil {
		dg.Findings = []string{}
	}
	return dg, nil
}

func podMap(p corev1.Pod) map[string]any {
	m := map[string]any{"kind": "Pod", "metadata": map[string]any{"name": p.Name, "namespace": p.Namespace, "creationTimestamp": p.CreationTimestamp.UTC().Format(time.RFC3339)}}
	var cs []any
	for _, c := range p.Status.ContainerStatuses {
		st := map[string]any{}
		if c.State.Waiting != nil {
			st["waiting"] = map[string]any{"reason": c.State.Waiting.Reason}
		}
		if c.State.Terminated != nil {
			st["terminated"] = map[string]any{"reason": c.State.Terminated.Reason}
		}
		cs = append(cs, map[string]any{"ready": c.Ready, "restartCount": float64(c.RestartCount), "state": st})
	}
	m["status"] = map[string]any{"phase": string(p.Status.Phase), "reason": p.Status.Reason, "containerStatuses": cs}
	m["spec"] = map[string]any{"nodeName": p.Spec.NodeName}
	return m
}

func (d *Deps) podRelations(ctx context.Context, cluster string, p corev1.Pod, includeNode bool) []Ref {
	var out []Ref
	if p.Spec.NodeName != "" && includeNode {
		out = append(out, Ref{Kind: "Node", APIVersion: "v1", Name: p.Spec.NodeName, Note: "runs on"})
	}
	if p.Spec.ServiceAccountName != "" && p.Spec.ServiceAccountName != "default" {
		out = append(out, Ref{Kind: "ServiceAccount", APIVersion: "v1", Namespace: p.Namespace, Name: p.Spec.ServiceAccountName})
	}
	seen := map[string]bool{}
	add := func(kind, name, note string) {
		if name == "" || seen[kind+name] {
			return
		}
		seen[kind+name] = true
		out = append(out, Ref{Kind: kind, APIVersion: "v1", Namespace: p.Namespace, Name: name, Note: note})
	}
	for _, v := range p.Spec.Volumes {
		if v.ConfigMap != nil {
			add("ConfigMap", v.ConfigMap.Name, "volume")
		}
		if v.Secret != nil {
			add("Secret", v.Secret.SecretName, "volume")
		}
		if v.PersistentVolumeClaim != nil {
			add("PersistentVolumeClaim", v.PersistentVolumeClaim.ClaimName, "volume")
		}
	}
	for _, c := range p.Spec.Containers {
		for _, ef := range c.EnvFrom {
			if ef.ConfigMapRef != nil {
				add("ConfigMap", ef.ConfigMapRef.Name, "envFrom")
			}
			if ef.SecretRef != nil {
				add("Secret", ef.SecretRef.Name, "envFrom")
			}
		}
		for _, e := range c.Env {
			if e.ValueFrom != nil && e.ValueFrom.ConfigMapKeyRef != nil {
				add("ConfigMap", e.ValueFrom.ConfigMapKeyRef.Name, "env")
			}
			if e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil {
				add("Secret", e.ValueFrom.SecretKeyRef.Name, "env")
			}
		}
	}
	// Services selecting the pod.
	if cs, err := d.K8s.GetClientForCluster(cluster); err == nil {
		if svcs, err := cs.CoreV1().Services(p.Namespace).List(ctx, metav1.ListOptions{}); err == nil {
			for _, s := range svcs.Items {
				if len(s.Spec.Selector) > 0 && labels.SelectorFromSet(s.Spec.Selector).Matches(labels.Set(p.Labels)) {
					out = append(out, Ref{Kind: "Service", APIVersion: "v1", Namespace: p.Namespace, Name: s.Name, Note: "selects pod"})
				}
			}
		}
	}
	return out
}

func podFindings(p *corev1.Pod) (findings []string, failing []corev1.ContainerStatus) {
	if p.Status.Phase == corev1.PodPending {
		for _, c := range p.Status.Conditions {
			if c.Type == corev1.PodScheduled && c.Status == corev1.ConditionFalse {
				findings = append(findings, fmt.Sprintf("Pod %s is unschedulable: %s", p.Name, truncateText(c.Message, 200)))
			}
		}
	}
	all := append(append([]corev1.ContainerStatus{}, p.Status.InitContainerStatuses...), p.Status.ContainerStatuses...)
	for _, c := range all {
		bad := false
		if w := c.State.Waiting; w != nil && w.Reason != "" && w.Reason != "ContainerCreating" && w.Reason != "PodInitializing" {
			findings = append(findings, fmt.Sprintf("Container %s/%s waiting: %s %s", p.Name, c.Name, w.Reason, truncateText(ScrubText(w.Message), 200)))
			bad = true
		}
		if t := c.State.Terminated; t != nil && t.ExitCode != 0 {
			findings = append(findings, fmt.Sprintf("Container %s/%s terminated: %s (exit %d)", p.Name, c.Name, t.Reason, t.ExitCode))
			bad = true
		}
		if lt := c.LastTerminationState.Terminated; lt != nil && c.RestartCount > 0 {
			findings = append(findings, fmt.Sprintf("Container %s/%s restarted %d times; last exit: %s (code %d) at %s", p.Name, c.Name, c.RestartCount, lt.Reason, lt.ExitCode, lt.FinishedAt.UTC().Format(time.RFC3339)))
			if lt.Reason == "OOMKilled" {
				findings = append(findings, fmt.Sprintf("Container %s was OOMKilled: raise its memory limit or fix the leak", c.Name))
			}
			bad = true
		}
		if !c.Ready && c.State.Running != nil {
			findings = append(findings, fmt.Sprintf("Container %s/%s is running but not ready (readiness probe failing?)", p.Name, c.Name))
			bad = true
		}
		if bad {
			failing = append(failing, c)
		}
	}
	return findings, failing
}

func objectFindings(obj map[string]any) []string {
	var out []string
	st, _ := obj["status"].(map[string]any)
	conds, _ := st["conditions"].([]any)
	for _, c := range conds {
		cm, _ := c.(map[string]any)
		typ, status := str(cm, "type"), str(cm, "status")
		negative := strings.HasPrefix(typ, "Disk") || strings.HasPrefix(typ, "Memory") || strings.HasPrefix(typ, "PID") || typ == "ReplicaFailure" || typ == "Failed" || typ == "NetworkUnavailable"
		if (negative && status == "True") || (!negative && status == "False") {
			out = append(out, fmt.Sprintf("Condition %s=%s: %s %s", typ, status, str(cm, "reason"), truncateText(str(cm, "message"), 200)))
		}
	}
	spec, _ := obj["spec"].(map[string]any)
	if want := num(spec, "replicas"); want > 0 {
		if ready := num(st, "readyReplicas"); ready < want {
			out = append(out, fmt.Sprintf("%d of %d replicas ready", ready, want))
		}
	}
	return out
}

func eventHints(e EventGroup) []string {
	msg := e.Reason + " " + e.Message
	switch {
	case strings.Contains(msg, "FailedScheduling"):
		return []string{"Scheduling failed: " + e.Message}
	case strings.Contains(msg, "ErrImagePull"), strings.Contains(msg, "ImagePullBackOff"), strings.Contains(e.Reason, "Failed") && strings.Contains(msg, "pull"):
		return []string{"Image pull failing: " + e.Message}
	case strings.Contains(msg, "FailedMount"), strings.Contains(msg, "FailedAttachVolume"):
		return []string{"Volume problem: " + e.Message}
	case e.Reason == "Unhealthy":
		return []string{fmt.Sprintf("Probe failing (%dx): %s", e.Count, e.Message)}
	case e.Reason == "BackOff":
		return []string{"Crash loop back-off: " + e.Message}
	case e.Reason == "Evicted":
		return []string{"Evicted: " + e.Message}
	}
	return nil
}

func verdict(dg *Diagnosis) string {
	s := dg.Summary.Status
	switch {
	case s == "CrashLoopBackOff" || s == "ImagePullBackOff" || s == "ErrImagePull" || s == "Failed" || s == "NotReady" || strings.HasPrefix(s, "Not") || s == "OOMKilled" || s == "Error" || s == "CreateContainerConfigError":
		return "failing"
	case len(dg.Findings) > 0:
		return "degraded"
	case s == "":
		return "unknown"
	}
	return "healthy"
}

func dedupeStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
