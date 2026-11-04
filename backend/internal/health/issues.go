package health

import (
	"fmt"
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/kanivet/backend/internal/incidents"
)

// Signal is one rule hit on one object, before grouping.
type Signal struct {
	Rule     string
	Severity Severity
	Title    string
	Reason   string
	Message  string
	Hint     string
	// Object shows the problem; Group is what it is reported under (the
	// owning workload for pods, the object itself otherwise).
	Object ResourceRef
	Group  ResourceRef
	Since  time.Time
}

// Rule is one issue detector. Rules are pure functions of the snapshot.
type Rule struct {
	ID string
	// Kinds are the kinds the rule reads; it is skipped when any is
	// unavailable, so a forbidden list does not look like a healthy one.
	Kinds []string
	Eval  func(s *Snapshot, o *owners, emit func(Signal))
}

var rules []Rule

// RegisterRule adds a rule to the engine.
func RegisterRule(r Rule) { rules = append(rules, r) }

// Rules lists the registered rule ids.
func Rules() []string {
	out := make([]string, len(rules))
	for i, r := range rules {
		out[i] = r.ID
	}
	return out
}

func init() {
	RegisterRule(Rule{ID: "pod.container-waiting", Kinds: []string{"Pod"}, Eval: podWaitingRule})
	RegisterRule(Rule{ID: "pod.oomkilled", Kinds: []string{"Pod"}, Eval: oomRule})
	RegisterRule(Rule{ID: "pod.pending", Kinds: []string{"Pod"}, Eval: pendingRule})
	RegisterRule(Rule{ID: "pod.not-ready", Kinds: []string{"Pod"}, Eval: notReadyRule})
	RegisterRule(Rule{ID: "pod.failed", Kinds: []string{"Pod"}, Eval: podFailedRule})
	RegisterRule(Rule{ID: "workload.rollout", Kinds: []string{"Deployment", "StatefulSet", "DaemonSet"}, Eval: rolloutRule})
	RegisterRule(Rule{ID: "service.endpoints", Kinds: []string{"Service", "Pod"}, Eval: serviceRule})
	RegisterRule(Rule{ID: "ingress.backend", Kinds: []string{"Ingress", "Service"}, Eval: ingressRule})
	RegisterRule(Rule{ID: "pvc.pending", Kinds: []string{"PersistentVolumeClaim"}, Eval: pvcRule})
	RegisterRule(Rule{ID: "node.condition", Kinds: []string{"Node"}, Eval: nodeRule})
	RegisterRule(Rule{ID: "hpa.unable", Kinds: []string{"HorizontalPodAutoscaler"}, Eval: hpaRule})
	RegisterRule(Rule{ID: "job.failed", Kinds: []string{"Job"}, Eval: jobRule})
	RegisterRule(Rule{ID: "cronjob.missed", Kinds: []string{"CronJob"}, Eval: cronJobRule})
}

// EvaluateIssues runs every rule, folds in the incident classifier's recent
// warnings and groups the result.
func EvaluateIssues(s *Snapshot) IssuesReport {
	o := newOwners(s)
	unavailable := map[string]bool{}
	for _, k := range s.Unavailable {
		unavailable[k] = true
	}
	var signals []Signal
	emit := func(sig Signal) { signals = append(signals, sig) }
	for _, r := range rules {
		skip := false
		for _, k := range r.Kinds {
			if unavailable[k] {
				skip = true
			}
		}
		if !skip {
			r.Eval(s, o, emit)
		}
	}
	signals = append(signals, eventSignals(s, o, signals)...)
	issues := group(signals)
	rep := IssuesReport{Issues: issues, GeneratedAt: s.Now, Unavailable: s.Unavailable}
	for _, is := range issues {
		rep.Summary.Total++
		switch is.Severity {
		case SeverityCritical:
			rep.Summary.Critical++
		case SeverityWarning:
			rep.Summary.Warning++
		default:
			rep.Summary.Info++
		}
	}
	return rep
}

type groupKey struct{ rule, reason, group string }

func group(signals []Signal) []Issue {
	byKey := map[groupKey]*Issue{}
	seen := map[groupKey]map[string]bool{}
	var order []groupKey
	for _, sig := range signals {
		k := groupKey{sig.Rule, sig.Reason, sig.Group.Key()}
		is := byKey[k]
		if is == nil {
			is = &Issue{
				ID:       sig.Rule + ":" + sig.Reason + ":" + sig.Group.Key(),
				Rule:     sig.Rule,
				Severity: sig.Severity,
				Title:    sig.Title,
				Reason:   sig.Reason,
				Message:  sig.Message,
				Hint:     sig.Hint,
				Resource: sig.Group,
				Since:    sig.Since,
				Source:   "state",
			}
			if strings.HasPrefix(sig.Rule, "event.") {
				is.Source = "event"
			}
			byKey[k] = is
			seen[k] = map[string]bool{}
			order = append(order, k)
		}
		if seen[k][sig.Object.Key()] {
			continue
		}
		seen[k][sig.Object.Key()] = true
		is.Count++
		if sig.Object != sig.Group {
			is.Affected = append(is.Affected, sig.Object)
		}
		if Rank(sig.Severity) > Rank(is.Severity) {
			is.Severity = sig.Severity
			is.Message = sig.Message
		}
		if !sig.Since.IsZero() && (is.Since.IsZero() || sig.Since.Before(is.Since)) {
			is.Since = sig.Since
		}
	}
	out := make([]Issue, 0, len(order))
	for _, k := range order {
		is := byKey[k]
		if is.Count > 1 && len(is.Affected) > 0 {
			is.Title = fmt.Sprintf("%s (%d %ss)", is.Title, is.Count, strings.ToLower(is.Affected[0].Kind))
		}
		out = append(out, *is)
	}
	SortIssues(out)
	return out
}

// SortIssues ranks by severity, then by how many objects are affected, then
// oldest first: a long-running outage outranks a fresh blip.
func SortIssues(xs []Issue) {
	sort.SliceStable(xs, func(i, j int) bool {
		a, b := xs[i], xs[j]
		if Rank(a.Severity) != Rank(b.Severity) {
			return Rank(a.Severity) > Rank(b.Severity)
		}
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		if !a.Since.Equal(b.Since) {
			if a.Since.IsZero() {
				return false
			}
			if b.Since.IsZero() {
				return true
			}
			return a.Since.Before(b.Since)
		}
		return a.ID < b.ID
	})
}

// ---- pods -------------------------------------------------------------------

func podStart(p *corev1.Pod) time.Time {
	if p.Status.StartTime != nil {
		return p.Status.StartTime.Time
	}
	return p.CreationTimestamp.Time
}

func allStatuses(p *corev1.Pod) []corev1.ContainerStatus {
	return append(append([]corev1.ContainerStatus(nil), p.Status.InitContainerStatuses...), p.Status.ContainerStatuses...)
}

func conditionSince(p *corev1.Pod, t corev1.PodConditionType) time.Time {
	for _, c := range p.Status.Conditions {
		if c.Type == t {
			return c.LastTransitionTime.Time
		}
	}
	return time.Time{}
}

var waitingReasons = map[string]struct {
	sev   Severity
	title string
	hint  string
}{
	"CrashLoopBackOff":           {SeverityCritical, "Container crash-looping", "Open the previous container logs to see why it exits."},
	"ImagePullBackOff":           {SeverityCritical, "Image cannot be pulled", "Check the image name and tag, and the pull secret for private registries."},
	"ErrImagePull":               {SeverityCritical, "Image cannot be pulled", "Check the image name and tag, and the pull secret for private registries."},
	"InvalidImageName":           {SeverityCritical, "Invalid image name", "Fix the image reference in the pod template."},
	"CreateContainerConfigError": {SeverityCritical, "Container config error", "A referenced ConfigMap, Secret or key is missing."},
	"CreateContainerError":       {SeverityCritical, "Container cannot be created", "Read the pod events for the runtime error."},
	"RunContainerError":          {SeverityCritical, "Container cannot start", "Read the pod events for the runtime error."},
}

func podWaitingRule(s *Snapshot, o *owners, emit func(Signal)) {
	for _, p := range s.Pods {
		if p.DeletionTimestamp != nil {
			continue
		}
		for _, cs := range allStatuses(p) {
			w := cs.State.Waiting
			if w == nil {
				continue
			}
			info, ok := waitingReasons[w.Reason]
			if !ok {
				continue
			}
			reason := w.Reason
			if reason == "ErrImagePull" {
				reason = "ImagePullBackOff"
			}
			msg := fmt.Sprintf("Container %s: %s", cs.Name, w.Reason)
			if w.Message != "" {
				msg += ": " + w.Message
			}
			if cs.RestartCount > 0 {
				msg += fmt.Sprintf(" (%d restarts)", cs.RestartCount)
			}
			since := podStart(p)
			emit(Signal{Rule: "pod." + strings.ToLower(reason), Severity: info.sev, Title: info.title, Reason: reason, Message: msg, Hint: info.hint, Object: podRef(p), Group: o.ownerOfPod(p), Since: since})
		}
	}
}

// oomRule reports containers killed for memory in the last hour.
func oomRule(s *Snapshot, o *owners, emit func(Signal)) {
	for _, p := range s.Pods {
		for _, cs := range allStatuses(p) {
			var t *corev1.ContainerStateTerminated
			if cs.State.Terminated != nil && cs.State.Terminated.Reason == "OOMKilled" {
				t = cs.State.Terminated
			} else if lt := cs.LastTerminationState.Terminated; lt != nil && lt.Reason == "OOMKilled" {
				t = lt
			}
			if t == nil || (!t.FinishedAt.IsZero() && s.Now.Sub(t.FinishedAt.Time) > time.Hour) {
				continue
			}
			emit(Signal{Rule: "pod.oomkilled", Severity: SeverityCritical, Title: "Container OOMKilled", Reason: "OOMKilled",
				Message: fmt.Sprintf("Container %s was killed for exceeding its memory limit (%d restarts)", cs.Name, cs.RestartCount),
				Hint:    "Raise the memory limit or find the leak; Rightsizing shows the observed peak.",
				Object:  podRef(p), Group: o.ownerOfPod(p), Since: t.FinishedAt.Time})
		}
	}
}

const pendingGrace = 2 * time.Minute

func pendingRule(s *Snapshot, o *owners, emit func(Signal)) {
	for _, p := range s.Pods {
		if p.Status.Phase != corev1.PodPending || p.DeletionTimestamp != nil {
			continue
		}
		age := s.Now.Sub(p.CreationTimestamp.Time)
		var sched *corev1.PodCondition
		for i := range p.Status.Conditions {
			if p.Status.Conditions[i].Type == corev1.PodScheduled {
				sched = &p.Status.Conditions[i]
			}
		}
		if sched != nil && sched.Status == corev1.ConditionFalse {
			reason := sched.Reason
			if reason == "" {
				reason = "Unschedulable"
			}
			sev := SeverityWarning
			if age > 5*time.Minute {
				sev = SeverityCritical
			}
			msg := sched.Message
			if msg == "" {
				msg = "The scheduler has not placed the pod"
			}
			emit(Signal{Rule: "pod.unschedulable", Severity: sev, Title: "Pod cannot be scheduled", Reason: reason, Message: msg,
				Hint:   "Free capacity, relax node selectors/affinity or tolerate the taints named in the message.",
				Object: podRef(p), Group: o.ownerOfPod(p), Since: p.CreationTimestamp.Time})
			continue
		}
		if age < pendingGrace {
			continue
		}
		// Scheduled but stuck: waiting containers are covered by
		// podWaitingRule unless their reason is benign-looking.
		stuck := true
		for _, cs := range allStatuses(p) {
			if w := cs.State.Waiting; w != nil {
				if _, covered := waitingReasons[w.Reason]; covered {
					stuck = false
				}
			}
		}
		if !stuck {
			continue
		}
		emit(Signal{Rule: "pod.pending", Severity: SeverityWarning, Title: "Pod stuck pending", Reason: "Pending",
			Message: fmt.Sprintf("Pending for %s on %s", shortDuration(age), nonEmpty(p.Spec.NodeName, "no node")),
			Hint:    "Volumes not attaching or init containers waiting are the usual causes; check the pod events.",
			Object:  podRef(p), Group: o.ownerOfPod(p), Since: p.CreationTimestamp.Time})
	}
}

// notReadyRule reports running containers failing their readiness probe.
func notReadyRule(s *Snapshot, o *owners, emit func(Signal)) {
	for _, p := range s.Pods {
		if p.Status.Phase != corev1.PodRunning || p.DeletionTimestamp != nil {
			continue
		}
		since := conditionSince(p, corev1.ContainersReady)
		if since.IsZero() || s.Now.Sub(since) < time.Minute {
			continue
		}
		for _, cs := range p.Status.ContainerStatuses {
			if cs.Ready || cs.State.Running == nil {
				continue
			}
			if cs.Started != nil && !*cs.Started {
				continue // startup probe still running
			}
			emit(Signal{Rule: "pod.probe-failing", Severity: SeverityWarning, Title: "Readiness probe failing", Reason: "NotReady",
				Message: fmt.Sprintf("Container %s is running but not ready", cs.Name),
				Hint:    "The pod gets no Service traffic; the Unhealthy events carry the probe output.",
				Object:  podRef(p), Group: o.ownerOfPod(p), Since: since})
		}
	}
}

func podFailedRule(s *Snapshot, o *owners, emit func(Signal)) {
	for _, p := range s.Pods {
		if p.Status.Phase != corev1.PodFailed {
			continue
		}
		owner := o.ownerOfPod(p)
		if owner.Kind == "Job" || owner.Kind == "CronJob" {
			continue // jobRule reports these
		}
		reason := nonEmpty(p.Status.Reason, "Failed")
		sev := SeverityWarning
		if reason == "Evicted" {
			sev = SeverityInfo
		}
		emit(Signal{Rule: "pod.failed", Severity: sev, Title: "Pod failed", Reason: reason, Message: nonEmpty(p.Status.Message, "The pod terminated with a failure"),
			Object: podRef(p), Group: owner, Since: podStart(p)})
	}
}

// ---- workloads --------------------------------------------------------------

func deploymentCondition(d *appsv1.Deployment, t appsv1.DeploymentConditionType) *appsv1.DeploymentCondition {
	for i := range d.Status.Conditions {
		if d.Status.Conditions[i].Type == t {
			return &d.Status.Conditions[i]
		}
	}
	return nil
}

func rolloutRule(s *Snapshot, _ *owners, emit func(Signal)) {
	for i := range s.Deployments {
		d := &s.Deployments[i]
		ref := ResourceRef{APIVersion: "apps/v1", Kind: "Deployment", Namespace: d.Namespace, Name: d.Name}
		want := int32(1)
		if d.Spec.Replicas != nil {
			want = *d.Spec.Replicas
		}
		if c := deploymentCondition(d, appsv1.DeploymentProgressing); c != nil && c.Reason == "ProgressDeadlineExceeded" {
			emit(Signal{Rule: "deployment.rollout-stuck", Severity: SeverityCritical, Title: "Rollout stuck", Reason: "ProgressDeadlineExceeded",
				Message: nonEmpty(c.Message, "The rollout exceeded its progress deadline"),
				Hint:    "Look at the new ReplicaSet's pods; roll back if the new version cannot start.",
				Object:  ref, Group: ref, Since: c.LastTransitionTime.Time})
			continue
		}
		if c := deploymentCondition(d, appsv1.DeploymentReplicaFailure); c != nil && c.Status == corev1.ConditionTrue {
			emit(Signal{Rule: "deployment.replica-failure", Severity: SeverityCritical, Title: "Replicas cannot be created", Reason: nonEmpty(c.Reason, "ReplicaFailure"),
				Message: c.Message, Hint: "Quota or admission webhooks usually reject the pods; read the ReplicaSet events.",
				Object: ref, Group: ref, Since: c.LastTransitionTime.Time})
			continue
		}
		if want > 0 && d.Status.AvailableReplicas < want {
			c := deploymentCondition(d, appsv1.DeploymentAvailable)
			since := d.CreationTimestamp.Time
			if c != nil {
				since = c.LastTransitionTime.Time
			}
			if c != nil && c.Status == corev1.ConditionTrue && s.Now.Sub(since) < 10*time.Minute {
				continue
			}
			if s.Now.Sub(since) < 2*time.Minute {
				continue
			}
			sev := SeverityWarning
			if d.Status.AvailableReplicas == 0 {
				sev = SeverityCritical
			}
			emit(Signal{Rule: "deployment.unavailable", Severity: sev, Title: "Deployment below desired replicas", Reason: "Unavailable",
				Message: fmt.Sprintf("%d of %d replicas available", d.Status.AvailableReplicas, want),
				Object:  ref, Group: ref, Since: since})
		}
	}
	for i := range s.StatefulSets {
		st := &s.StatefulSets[i]
		want := int32(1)
		if st.Spec.Replicas != nil {
			want = *st.Spec.Replicas
		}
		if want > 0 && st.Status.ReadyReplicas < want && s.Now.Sub(st.CreationTimestamp.Time) > 2*time.Minute {
			ref := ResourceRef{APIVersion: "apps/v1", Kind: "StatefulSet", Namespace: st.Namespace, Name: st.Name}
			sev := SeverityWarning
			if st.Status.ReadyReplicas == 0 {
				sev = SeverityCritical
			}
			emit(Signal{Rule: "statefulset.unavailable", Severity: sev, Title: "StatefulSet below desired replicas", Reason: "Unavailable",
				Message: fmt.Sprintf("%d of %d replicas ready", st.Status.ReadyReplicas, want), Object: ref, Group: ref})
		}
	}
	for i := range s.DaemonSets {
		ds := &s.DaemonSets[i]
		if ds.Status.NumberUnavailable > 0 && s.Now.Sub(ds.CreationTimestamp.Time) > 2*time.Minute {
			ref := ResourceRef{APIVersion: "apps/v1", Kind: "DaemonSet", Namespace: ds.Namespace, Name: ds.Name}
			emit(Signal{Rule: "daemonset.unavailable", Severity: SeverityWarning, Title: "DaemonSet pods unavailable", Reason: "Unavailable",
				Message: fmt.Sprintf("%d of %d nodes without a ready pod", ds.Status.NumberUnavailable, ds.Status.DesiredNumberScheduled), Object: ref, Group: ref})
		}
	}
}

// ---- networking -------------------------------------------------------------

func podReady(p *corev1.Pod) bool {
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// serviceRule computes endpoints from pods the way the endpoints controller
// does, without listing Endpoints.
func serviceRule(s *Snapshot, _ *owners, emit func(Signal)) {
	podsByNS := map[string][]*corev1.Pod{}
	for _, p := range s.Pods {
		if p.DeletionTimestamp == nil && p.Status.Phase != corev1.PodSucceeded && p.Status.Phase != corev1.PodFailed {
			podsByNS[p.Namespace] = append(podsByNS[p.Namespace], p)
		}
	}
	for _, svc := range s.Services {
		if len(svc.Spec.Selector) == 0 || svc.Spec.Type == corev1.ServiceTypeExternalName {
			continue
		}
		sel := labels.SelectorFromSet(svc.Spec.Selector)
		matched, ready := 0, 0
		for _, p := range podsByNS[svc.Namespace] {
			if sel.Matches(labels.Set(p.Labels)) {
				matched++
				if podReady(p) {
					ready++
				}
			}
		}
		ref := ResourceRef{APIVersion: "v1", Kind: "Service", Namespace: svc.Namespace, Name: svc.Name}
		switch {
		case matched == 0:
			emit(Signal{Rule: "service.no-pods", Severity: SeverityWarning, Title: "Service selects no pods", Reason: "NoMatchingPods",
				Message: "Selector " + sel.String() + " matches no pod in " + svc.Namespace,
				Hint:    "Fix the selector or the pod template labels; traffic to this Service goes nowhere.",
				Object:  ref, Group: ref, Since: svc.CreationTimestamp.Time})
		case ready == 0:
			emit(Signal{Rule: "service.no-ready-endpoints", Severity: SeverityCritical, Title: "Service has no ready endpoints", Reason: "NoReadyEndpoints",
				Message: fmt.Sprintf("%d pods match, none ready", matched),
				Hint:    "Every backing pod fails readiness; fix the pods and the Service recovers.",
				Object:  ref, Group: ref})
		}
	}
}

func ingressRule(s *Snapshot, _ *owners, emit func(Signal)) {
	svcs := map[string]bool{}
	for _, svc := range s.Services {
		svcs[svc.Namespace+"/"+svc.Name] = true
	}
	for _, ing := range s.Ingresses {
		ref := ResourceRef{APIVersion: "networking.k8s.io/v1", Kind: "Ingress", Namespace: ing.Namespace, Name: ing.Name}
		var names []string
		if b := ing.Spec.DefaultBackend; b != nil && b.Service != nil {
			names = append(names, b.Service.Name)
		}
		for _, r := range ing.Spec.Rules {
			if r.HTTP == nil {
				continue
			}
			for _, p := range r.HTTP.Paths {
				if p.Backend.Service != nil {
					names = append(names, p.Backend.Service.Name)
				}
			}
		}
		reported := map[string]bool{}
		for _, n := range names {
			if svcs[ing.Namespace+"/"+n] || reported[n] {
				continue
			}
			reported[n] = true
			emit(Signal{Rule: "ingress.missing-service", Severity: SeverityWarning, Title: "Ingress points at a missing Service", Reason: "ServiceNotFound",
				Message: fmt.Sprintf("Backend Service %s does not exist in %s", n, ing.Namespace),
				Object:  ref, Group: ref, Since: ing.CreationTimestamp.Time})
		}
	}
}

// ---- storage, nodes, autoscaling, batch -----------------------------------------

func pvcRule(s *Snapshot, _ *owners, emit func(Signal)) {
	for _, pvc := range s.PVCs {
		if pvc.Status.Phase == corev1.ClaimBound {
			continue
		}
		age := s.Now.Sub(pvc.CreationTimestamp.Time)
		if age < time.Minute {
			continue
		}
		sev := SeverityWarning
		if pvc.Status.Phase == corev1.ClaimLost || age > 10*time.Minute {
			sev = SeverityCritical
		}
		ref := ResourceRef{APIVersion: "v1", Kind: "PersistentVolumeClaim", Namespace: pvc.Namespace, Name: pvc.Name}
		sc := "default"
		if pvc.Spec.StorageClassName != nil {
			sc = *pvc.Spec.StorageClassName
		}
		emit(Signal{Rule: "pvc.pending", Severity: sev, Title: "Volume claim not bound", Reason: string(pvc.Status.Phase),
			Message: fmt.Sprintf("%s for %s (storage class %s)", pvc.Status.Phase, shortDuration(age), sc),
			Hint:    "WaitForFirstConsumer classes bind only once a pod uses the claim; otherwise check the provisioner.",
			Object:  ref, Group: ref, Since: pvc.CreationTimestamp.Time})
	}
}

func nodeRule(s *Snapshot, _ *owners, emit func(Signal)) {
	for _, n := range s.Nodes {
		ref := ResourceRef{APIVersion: "v1", Kind: "Node", Name: n.Name}
		for _, c := range n.Status.Conditions {
			switch c.Type {
			case corev1.NodeReady:
				if c.Status != corev1.ConditionTrue {
					emit(Signal{Rule: "node.not-ready", Severity: SeverityCritical, Title: "Node not ready", Reason: nonEmpty(c.Reason, "NotReady"),
						Message: nonEmpty(c.Message, "The kubelet stopped reporting"), Object: ref, Group: ref, Since: c.LastTransitionTime.Time})
				}
			case corev1.NodeMemoryPressure, corev1.NodeDiskPressure, corev1.NodePIDPressure, corev1.NodeNetworkUnavailable:
				if c.Status == corev1.ConditionTrue {
					emit(Signal{Rule: "node.pressure", Severity: SeverityWarning, Title: "Node under " + strings.TrimPrefix(strings.TrimSuffix(string(c.Type), "Pressure"), "Node") + " pressure", Reason: string(c.Type),
						Message: nonEmpty(c.Message, string(c.Type)), Hint: "The kubelet evicts pods while the pressure lasts.", Object: ref, Group: ref, Since: c.LastTransitionTime.Time})
				}
			}
		}
		if n.Spec.Unschedulable {
			emit(Signal{Rule: "node.cordoned", Severity: SeverityInfo, Title: "Node cordoned", Reason: "Unschedulable", Message: "New pods are not scheduled here", Object: ref, Group: ref})
		}
	}
}

func hpaRule(s *Snapshot, _ *owners, emit func(Signal)) {
	for _, h := range s.HPAs {
		ref := ResourceRef{APIVersion: "autoscaling/v2", Kind: "HorizontalPodAutoscaler", Namespace: h.Namespace, Name: h.Name}
		for _, c := range h.Status.Conditions {
			switch {
			case (c.Type == autoscalingv2.AbleToScale || c.Type == autoscalingv2.ScalingActive) && c.Status == corev1.ConditionFalse:
				emit(Signal{Rule: "hpa.unable", Severity: SeverityWarning, Title: "Autoscaler cannot scale", Reason: nonEmpty(c.Reason, string(c.Type)),
					Message: c.Message, Hint: "Usually metrics are missing (metrics-server down or no requests set on the target).",
					Object: ref, Group: ref, Since: c.LastTransitionTime.Time})
			case c.Type == autoscalingv2.ScalingLimited && c.Status == corev1.ConditionTrue && c.Reason == "TooManyReplicas" && h.Status.CurrentReplicas >= h.Spec.MaxReplicas:
				emit(Signal{Rule: "hpa.at-max", Severity: SeverityInfo, Title: "Autoscaler at max replicas", Reason: c.Reason,
					Message: fmt.Sprintf("Pinned at %d replicas; demand may exceed capacity", h.Spec.MaxReplicas), Object: ref, Group: ref, Since: c.LastTransitionTime.Time})
			}
		}
	}
}

func jobCondition(j *batchv1.Job, t batchv1.JobConditionType) *batchv1.JobCondition {
	for i := range j.Status.Conditions {
		if j.Status.Conditions[i].Type == t && j.Status.Conditions[i].Status == corev1.ConditionTrue {
			return &j.Status.Conditions[i]
		}
	}
	return nil
}

// jobRule reports failed Jobs. A CronJob's failed run is reported on the
// CronJob, and only while it is the CronJob's newest finished run.
func jobRule(s *Snapshot, o *owners, emit func(Signal)) {
	newest := map[string]*batchv1.Job{}
	for i := range s.Jobs {
		j := &s.Jobs[i]
		cj, ok := o.jobToCron[j.Namespace+"/"+j.Name]
		if !ok || (jobCondition(j, batchv1.JobComplete) == nil && jobCondition(j, batchv1.JobFailed) == nil) {
			continue
		}
		k := j.Namespace + "/" + cj
		if cur := newest[k]; cur == nil || j.CreationTimestamp.After(cur.CreationTimestamp.Time) {
			newest[k] = j
		}
	}
	for i := range s.Jobs {
		j := &s.Jobs[i]
		c := jobCondition(j, batchv1.JobFailed)
		if c == nil {
			continue
		}
		ref := ResourceRef{APIVersion: "batch/v1", Kind: "Job", Namespace: j.Namespace, Name: j.Name}
		group := ref
		if cj, ok := o.jobToCron[j.Namespace+"/"+j.Name]; ok {
			if newest[j.Namespace+"/"+cj] != j {
				continue
			}
			group = ResourceRef{APIVersion: "batch/v1", Kind: "CronJob", Namespace: j.Namespace, Name: cj}
		}
		emit(Signal{Rule: "job.failed", Severity: SeverityWarning, Title: "Job failed", Reason: nonEmpty(c.Reason, "Failed"),
			Message: nonEmpty(c.Message, "The job reached its failure limit"), Hint: "Open the logs of its last pod.",
			Object: ref, Group: group, Since: c.LastTransitionTime.Time})
	}
}

func cronJobRule(s *Snapshot, _ *owners, emit func(Signal)) {
	for _, cj := range s.CronJobs {
		if cj.Spec.Suspend != nil && *cj.Spec.Suspend {
			continue
		}
		period, ok := CronPeriod(cj.Spec.Schedule)
		if !ok {
			continue
		}
		last := cj.CreationTimestamp.Time
		ever := false
		if cj.Status.LastScheduleTime != nil {
			last = cj.Status.LastScheduleTime.Time
			ever = true
		}
		overdue := s.Now.Sub(last) - period
		if overdue < period+5*time.Minute {
			continue
		}
		ref := ResourceRef{APIVersion: "batch/v1", Kind: "CronJob", Namespace: cj.Namespace, Name: cj.Name}
		msg := fmt.Sprintf("Schedule %q has not fired for %s", cj.Spec.Schedule, shortDuration(s.Now.Sub(last)))
		if !ever {
			msg = fmt.Sprintf("Schedule %q has never fired (created %s ago)", cj.Spec.Schedule, shortDuration(s.Now.Sub(last)))
		}
		emit(Signal{Rule: "cronjob.missed", Severity: SeverityWarning, Title: "CronJob has not run", Reason: "MissedSchedule", Message: msg,
			Hint:   "A startingDeadlineSeconds too short, or the controller missing >100 schedules, stops it firing.",
			Object: ref, Group: ref, Since: last})
	}
}

// ---- events -----------------------------------------------------------------

// eventWindow is how far back a warning event counts as live.
const eventWindow = 15 * time.Minute

// eventSignals turns the incident classifier's recent critical and warning
// entries into issues, skipping objects a state rule already reports: the
// state rules say what is wrong now, events fill the gaps (probe output,
// mount failures, evictions).
func eventSignals(s *Snapshot, o *owners, state []Signal) []Signal {
	if len(s.Events) == 0 {
		return nil
	}
	covered := map[string]bool{}
	for _, sig := range state {
		covered[sig.Object.Key()] = true
		covered[sig.Group.Key()] = true
	}
	pods := map[string]*corev1.Pod{}
	for _, p := range s.Pods {
		pods[p.Namespace+"/"+p.Name] = p
	}
	tl := incidents.BuildTimeline(s.Events, incidents.TimelineFilters{
		Since:      s.Now.Add(-eventWindow),
		Severities: []incidents.Severity{incidents.SeverityCritical, incidents.SeverityWarning},
	})
	var out []Signal
	for _, e := range tl.Entries {
		obj := ResourceRef{APIVersion: e.APIVersion, Kind: e.Kind, Namespace: e.Namespace, Name: e.Name}
		if covered[obj.Key()] || ignoredEventReason[e.Reason] {
			continue
		}
		group := obj
		if e.Kind == "Pod" {
			p := pods[e.Namespace+"/"+e.Name]
			if p == nil {
				continue // the pod is gone; its warning is history, not a live issue
			}
			group = o.ownerOfPod(p)
			if covered[group.Key()] {
				continue
			}
		}
		title := "Warning: " + e.Reason
		hint := ""
		if e.Reason == "Unhealthy" {
			title = "Probe failing"
			hint = "The kubelet restarts the container when a liveness probe keeps failing."
		}
		count := int(e.Count)
		msg := e.Message
		if count > 1 {
			msg = fmt.Sprintf("%s (x%d in %s)", msg, count, shortDuration(eventWindow))
		}
		out = append(out, Signal{Rule: "event." + strings.ToLower(e.Reason), Severity: e.Severity, Title: title, Reason: e.Reason,
			Message: msg, Hint: hint, Object: obj, Group: group, Since: e.FirstTimestamp})
	}
	return out
}

// ignoredEventReason are warnings that are noise as live issues.
var ignoredEventReason = map[string]bool{
	"FailedGetResourceMetric":       true, // hpaRule covers it
	"FailedComputeMetricsReplicas":  true,
	"DNSConfigForming":              true,
	"FailedToUpdateEndpointSlices":  true,
	"FailedToUpdateEndpoint":        true,
	"NetworkNotReady":               true,
	"FailedPreStopHook":             true,
	"ExceededGracePeriod":           true,
	"InvalidDiskCapacity":           true,
	"FailedNodeAllocatableEnforcem": true,
}

func shortDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

func nonEmpty(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
