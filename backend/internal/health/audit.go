package health

import (
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

// workload is a pod template and what owns it.
type workload struct {
	Ref      ResourceRef
	Spec     *corev1.PodSpec
	Labels   map[string]string
	Replicas int32 // -1 when not replicated (DaemonSet, Job, CronJob)
	Batch    bool
}

func workloads(s *Snapshot, o *owners) []workload {
	var out []workload
	rep := func(r *int32) int32 {
		if r == nil {
			return 1
		}
		return *r
	}
	for i := range s.Deployments {
		d := &s.Deployments[i]
		out = append(out, workload{Ref: ResourceRef{"apps/v1", "Deployment", d.Namespace, d.Name}, Spec: &d.Spec.Template.Spec, Labels: d.Spec.Template.Labels, Replicas: rep(d.Spec.Replicas)})
	}
	for i := range s.StatefulSets {
		d := &s.StatefulSets[i]
		out = append(out, workload{Ref: ResourceRef{"apps/v1", "StatefulSet", d.Namespace, d.Name}, Spec: &d.Spec.Template.Spec, Labels: d.Spec.Template.Labels, Replicas: rep(d.Spec.Replicas)})
	}
	for i := range s.DaemonSets {
		d := &s.DaemonSets[i]
		out = append(out, workload{Ref: ResourceRef{"apps/v1", "DaemonSet", d.Namespace, d.Name}, Spec: &d.Spec.Template.Spec, Labels: d.Spec.Template.Labels, Replicas: -1})
	}
	for i := range s.CronJobs {
		d := &s.CronJobs[i]
		out = append(out, workload{Ref: ResourceRef{"batch/v1", "CronJob", d.Namespace, d.Name}, Spec: &d.Spec.JobTemplate.Spec.Template.Spec, Labels: d.Spec.JobTemplate.Spec.Template.Labels, Replicas: -1, Batch: true})
	}
	for i := range s.Jobs {
		d := &s.Jobs[i]
		if _, owned := o.jobToCron[d.Namespace+"/"+d.Name]; owned {
			continue
		}
		out = append(out, workload{Ref: ResourceRef{"batch/v1", "Job", d.Namespace, d.Name}, Spec: &d.Spec.Template.Spec, Labels: d.Spec.Template.Labels, Replicas: -1, Batch: true})
	}
	return out
}

// auditContext is what checks see besides the workload.
type auditContext struct {
	pdbs       map[string][]labels.Selector // namespace -> selectors
	netpolNS   map[string]bool
	hpaTargets map[string]bool // ns/Kind/name
	namespaces map[string]bool
}

// CheckFunc evaluates one check on one workload, returning one message per
// failure (container-level checks name the container).
type CheckFunc func(w workload, ac *auditContext) []failure

type failure struct{ container, message string }

type checkDef struct {
	Check
	eval CheckFunc
}

var checks []checkDef

// RegisterCheck adds an audit check.
func RegisterCheck(c Check, eval CheckFunc) { checks = append(checks, checkDef{c, eval}) }

// Checks returns the registered checks.
func Checks() []Check {
	out := make([]Check, len(checks))
	for i, c := range checks {
		out[i] = c.Check
	}
	return out
}

// CheckByID returns a check's definition.
func CheckByID(id string) (Check, bool) {
	for _, c := range checks {
		if c.ID == id {
			return c.Check, true
		}
	}
	return Check{}, false
}

func containers(spec *corev1.PodSpec) []corev1.Container { return spec.Containers }

func perContainer(spec *corev1.PodSpec, bad func(c corev1.Container) string) []failure {
	var out []failure
	for _, c := range containers(spec) {
		if m := bad(c); m != "" {
			out = append(out, failure{c.Name, m})
		}
	}
	return out
}

func init() {
	RegisterCheck(Check{ID: "resources-requests", Title: "Resource requests set", Category: CategoryEfficiency, Severity: SeverityWarning,
		Explanation: "Without CPU and memory requests the scheduler packs pods blindly and the pod gets BestEffort QoS: it is the first evicted under pressure, and HPAs on utilization cannot work.",
		FixPath:     "spec.template.spec.containers[*].resources",
		Fix:         "resources:\n  requests:\n    cpu: 100m\n    memory: 128Mi"},
		func(w workload, _ *auditContext) []failure {
			return perContainer(w.Spec, func(c corev1.Container) string {
				var miss []string
				if _, ok := c.Resources.Requests[corev1.ResourceCPU]; !ok {
					miss = append(miss, "cpu")
				}
				if _, ok := c.Resources.Requests[corev1.ResourceMemory]; !ok {
					miss = append(miss, "memory")
				}
				if len(miss) == 0 {
					return ""
				}
				return "No " + strings.Join(miss, " or ") + " request"
			})
		})
	RegisterCheck(Check{ID: "resources-memory-limit", Title: "Memory limit set", Category: CategoryReliability, Severity: SeverityWarning,
		Explanation: "A container without a memory limit can take all of a node's memory and push the kubelet into evicting its neighbours.",
		FixPath:     "spec.template.spec.containers[*].resources",
		Fix:         "resources:\n  limits:\n    memory: 256Mi"},
		func(w workload, _ *auditContext) []failure {
			return perContainer(w.Spec, func(c corev1.Container) string {
				if _, ok := c.Resources.Limits[corev1.ResourceMemory]; ok {
					return ""
				}
				return "No memory limit"
			})
		})
	RegisterCheck(Check{ID: "probe-liveness", Title: "Liveness probe configured", Category: CategoryReliability, Severity: SeverityInfo,
		Explanation: "Without a liveness probe a deadlocked process keeps running and never gets restarted.",
		FixPath:     "spec.template.spec.containers[*]",
		Fix:         "livenessProbe:\n  httpGet:\n    path: /healthz\n    port: 8080\n  periodSeconds: 10\n  failureThreshold: 3"},
		func(w workload, _ *auditContext) []failure {
			if w.Batch {
				return nil
			}
			return perContainer(w.Spec, func(c corev1.Container) string {
				if c.LivenessProbe == nil {
					return "No liveness probe"
				}
				return ""
			})
		})
	RegisterCheck(Check{ID: "probe-readiness", Title: "Readiness probe configured", Category: CategoryReliability, Severity: SeverityWarning,
		Explanation: "Without a readiness probe a pod receives Service traffic as soon as it starts, before it can answer, and rollouts cannot tell a broken version from a working one.",
		FixPath:     "spec.template.spec.containers[*]",
		Fix:         "readinessProbe:\n  httpGet:\n    path: /ready\n    port: 8080\n  periodSeconds: 5"},
		func(w workload, _ *auditContext) []failure {
			if w.Batch {
				return nil
			}
			return perContainer(w.Spec, func(c corev1.Container) string {
				if c.ReadinessProbe == nil {
					return "No readiness probe"
				}
				return ""
			})
		})
	RegisterCheck(Check{ID: "run-as-non-root", Title: "Runs as non-root", Category: CategorySecurity, Severity: SeverityWarning,
		Explanation: "A process running as UID 0 that escapes its container is root on the node. Setting runAsNonRoot makes the kubelet refuse images that would run as root.",
		FixPath:     "spec.template.spec.securityContext",
		Fix:         "securityContext:\n  runAsNonRoot: true\n  runAsUser: 10001"},
		func(w workload, _ *auditContext) []failure {
			podNonRoot := w.Spec.SecurityContext != nil && ((w.Spec.SecurityContext.RunAsNonRoot != nil && *w.Spec.SecurityContext.RunAsNonRoot) ||
				(w.Spec.SecurityContext.RunAsUser != nil && *w.Spec.SecurityContext.RunAsUser > 0))
			return perContainer(w.Spec, func(c corev1.Container) string {
				sc := c.SecurityContext
				if sc != nil && sc.RunAsUser != nil && *sc.RunAsUser == 0 {
					return "Runs as UID 0"
				}
				if sc != nil && ((sc.RunAsNonRoot != nil && *sc.RunAsNonRoot) || (sc.RunAsUser != nil && *sc.RunAsUser > 0)) {
					return ""
				}
				if sc != nil && sc.RunAsNonRoot != nil && !*sc.RunAsNonRoot {
					return "runAsNonRoot is false"
				}
				if podNonRoot {
					return ""
				}
				return "May run as root (runAsNonRoot not set)"
			})
		})
	RegisterCheck(Check{ID: "privileged", Title: "Not privileged", Category: CategorySecurity, Severity: SeverityCritical,
		Explanation: "A privileged container has every capability and the host's devices: it is equivalent to root on the node.",
		FixPath:     "spec.template.spec.containers[*].securityContext",
		Fix:         "securityContext:\n  privileged: false"},
		func(w workload, _ *auditContext) []failure {
			return perContainer(w.Spec, func(c corev1.Container) string {
				if c.SecurityContext != nil && c.SecurityContext.Privileged != nil && *c.SecurityContext.Privileged {
					return "Privileged container"
				}
				return ""
			})
		})
	RegisterCheck(Check{ID: "privilege-escalation", Title: "Privilege escalation disabled", Category: CategorySecurity, Severity: SeverityInfo,
		Explanation: "allowPrivilegeEscalation defaults to true, letting setuid binaries gain more privileges than the process started with.",
		FixPath:     "spec.template.spec.containers[*].securityContext",
		Fix:         "securityContext:\n  allowPrivilegeEscalation: false\n  capabilities:\n    drop: [\"ALL\"]"},
		func(w workload, _ *auditContext) []failure {
			return perContainer(w.Spec, func(c corev1.Container) string {
				if c.SecurityContext != nil && c.SecurityContext.AllowPrivilegeEscalation != nil && !*c.SecurityContext.AllowPrivilegeEscalation {
					return ""
				}
				return "allowPrivilegeEscalation not set to false"
			})
		})
	RegisterCheck(Check{ID: "dangerous-capabilities", Title: "No dangerous capabilities", Category: CategorySecurity, Severity: SeverityWarning,
		Explanation: "SYS_ADMIN, NET_ADMIN, SYS_PTRACE and ALL give a container powers close to root on the node.",
		FixPath:     "spec.template.spec.containers[*].securityContext",
		Fix:         "securityContext:\n  capabilities:\n    drop: [\"ALL\"]\n    add: [\"NET_BIND_SERVICE\"]  # only what is needed"},
		func(w workload, _ *auditContext) []failure {
			return perContainer(w.Spec, func(c corev1.Container) string {
				if c.SecurityContext == nil || c.SecurityContext.Capabilities == nil {
					return ""
				}
				var bad []string
				for _, cap := range c.SecurityContext.Capabilities.Add {
					switch strings.ToUpper(string(cap)) {
					case "ALL", "SYS_ADMIN", "NET_ADMIN", "SYS_PTRACE", "SYS_MODULE", "DAC_READ_SEARCH", "NET_RAW":
						bad = append(bad, string(cap))
					}
				}
				if len(bad) == 0 {
					return ""
				}
				return "Adds " + strings.Join(bad, ", ")
			})
		})
	RegisterCheck(Check{ID: "host-namespaces", Title: "No host network, PID or IPC", Category: CategorySecurity, Severity: SeverityWarning,
		Explanation: "hostNetwork, hostPID and hostIPC put the pod in the node's namespaces: it sees every process or every connection on the node and bypasses NetworkPolicy.",
		FixPath:     "spec.template.spec",
		Fix:         "hostNetwork: false\nhostPID: false\nhostIPC: false"},
		func(w workload, _ *auditContext) []failure {
			var on []string
			if w.Spec.HostNetwork {
				on = append(on, "hostNetwork")
			}
			if w.Spec.HostPID {
				on = append(on, "hostPID")
			}
			if w.Spec.HostIPC {
				on = append(on, "hostIPC")
			}
			if len(on) == 0 {
				return nil
			}
			return []failure{{"", "Uses " + strings.Join(on, ", ")}}
		})
	RegisterCheck(Check{ID: "host-path", Title: "No hostPath volumes", Category: CategorySecurity, Severity: SeverityWarning,
		Explanation: "A hostPath volume mounts the node's filesystem into the pod; a writable one lets the pod tamper with the node.",
		FixPath:     "spec.template.spec.volumes",
		Fix:         "volumes:\n  - name: data\n    emptyDir: {}   # or a PersistentVolumeClaim"},
		func(w workload, _ *auditContext) []failure {
			var out []failure
			for _, v := range w.Spec.Volumes {
				if v.HostPath != nil {
					out = append(out, failure{"", fmt.Sprintf("Volume %s mounts host path %s", v.Name, v.HostPath.Path)})
				}
			}
			return out
		})
	RegisterCheck(Check{ID: "image-tag", Title: "Image pinned to a tag", Category: CategoryReliability, Severity: SeverityWarning,
		Explanation: "An image without a tag or with :latest changes under you: two pods of the same ReplicaSet can run different code and a rollback does not roll back.",
		FixPath:     "spec.template.spec.containers[*]",
		Fix:         "image: registry.example.com/app:1.4.2   # or @sha256:<digest>"},
		func(w workload, _ *auditContext) []failure {
			return perContainer(w.Spec, func(c corev1.Container) string {
				img := c.Image
				if strings.Contains(img, "@sha256:") {
					return ""
				}
				last := img[strings.LastIndex(img, "/")+1:]
				if !strings.Contains(last, ":") {
					return "Image " + img + " has no tag"
				}
				if strings.HasSuffix(last, ":latest") {
					return "Image " + img + " uses :latest"
				}
				return ""
			})
		})
	RegisterCheck(Check{ID: "single-replica", Title: "More than one replica", Category: CategoryReliability, Severity: SeverityWarning,
		Explanation: "A single replica goes down on every node drain, rollout or crash. Two or more replicas spread across nodes keep it serving.",
		FixPath:     "spec",
		Fix:         "replicas: 2\n# and spread them:\ntemplate:\n  spec:\n    topologySpreadConstraints:\n      - maxSkew: 1\n        topologyKey: kubernetes.io/hostname\n        whenUnsatisfiable: ScheduleAnyway\n        labelSelector:\n          matchLabels:\n            app: <app>"},
		func(w workload, ac *auditContext) []failure {
			if w.Replicas != 1 || ac.hpaTargets[w.Ref.Namespace+"/"+w.Ref.Kind+"/"+w.Ref.Name] {
				return nil
			}
			return []failure{{"", "Runs a single replica"}}
		})
	RegisterCheck(Check{ID: "pdb", Title: "PodDisruptionBudget covers it", Category: CategoryReliability, Severity: SeverityInfo,
		Explanation: "Without a PodDisruptionBudget a node drain or cluster upgrade can evict every replica at once.",
		FixPath:     "new object",
		Fix:         "apiVersion: policy/v1\nkind: PodDisruptionBudget\nmetadata:\n  name: <app>\nspec:\n  maxUnavailable: 1\n  selector:\n    matchLabels:\n      app: <app>"},
		func(w workload, ac *auditContext) []failure {
			if w.Replicas < 2 && !ac.hpaTargets[w.Ref.Namespace+"/"+w.Ref.Kind+"/"+w.Ref.Name] {
				return nil
			}
			for _, sel := range ac.pdbs[w.Ref.Namespace] {
				if sel.Matches(labels.Set(w.Labels)) {
					return nil
				}
			}
			return []failure{{"", "No PodDisruptionBudget selects its pods"}}
		})
	RegisterCheck(Check{ID: "network-policy", Title: "Namespace has a NetworkPolicy", Category: CategorySecurity, Severity: SeverityInfo,
		Explanation: "With no NetworkPolicy in its namespace every pod in the cluster can reach this workload, and it can reach everything.",
		FixPath:     "new object",
		Fix:         "apiVersion: networking.k8s.io/v1\nkind: NetworkPolicy\nmetadata:\n  name: default-deny-ingress\nspec:\n  podSelector: {}\n  policyTypes: [\"Ingress\"]"},
		func(w workload, ac *auditContext) []failure {
			if ac.netpolNS == nil || ac.netpolNS[w.Ref.Namespace] {
				return nil
			}
			return []failure{{"", "No NetworkPolicy in namespace " + w.Ref.Namespace}}
		})
	RegisterCheck(Check{ID: "automount-token", Title: "Service account token not auto-mounted", Category: CategorySecurity, Severity: SeverityInfo,
		Explanation: "Most workloads never call the Kubernetes API, yet get a token mounted that anyone who compromises the pod can use.",
		FixPath:     "spec.template.spec",
		Fix:         "automountServiceAccountToken: false"},
		func(w workload, _ *auditContext) []failure {
			if w.Spec.AutomountServiceAccountToken != nil && !*w.Spec.AutomountServiceAccountToken {
				return nil
			}
			return []failure{{"", "Mounts the service account token"}}
		})
}

// systemNamespaces are skipped unless asked for: their workloads come from
// the platform and their findings are not the user's to fix.
var systemNamespaces = map[string]bool{"kube-system": true, "kube-public": true, "kube-node-lease": true}

func isSystemNamespace(ns string) bool {
	return systemNamespaces[ns] || ns == "gke-managed-system"
}

func newAuditContext(s *Snapshot) *auditContext {
	ac := &auditContext{pdbs: map[string][]labels.Selector{}, hpaTargets: map[string]bool{}}
	for _, p := range s.PDBs {
		if p.Spec.Selector == nil {
			continue
		}
		if sel, err := metav1.LabelSelectorAsSelector(p.Spec.Selector); err == nil {
			ac.pdbs[p.Namespace] = append(ac.pdbs[p.Namespace], sel)
		}
	}
	netpolKnown := true
	for _, k := range s.Unavailable {
		if k == "NetworkPolicy" {
			netpolKnown = false
		}
	}
	if netpolKnown {
		ac.netpolNS = map[string]bool{}
		for _, n := range s.NetworkPolicies {
			ac.netpolNS[n.Namespace] = true
		}
	}
	for _, h := range s.HPAs {
		ac.hpaTargets[h.Namespace+"/"+h.Spec.ScaleTargetRef.Kind+"/"+h.Spec.ScaleTargetRef.Name] = true
	}
	return ac
}

var severityWeight = map[Severity]int{SeverityCritical: 10, SeverityWarning: 3, SeverityInfo: 1}

// EvaluateAudit runs every check on every workload in scope and scores the
// result. A workload's score is the share of check weight it passes; a
// namespace's and the cluster's are the same over all their workloads, so a
// critical failure costs ten times an informational one. Ignored findings
// count as passed.
func EvaluateAudit(s *Snapshot, scope AuditScope, ignored func(Finding) bool) AuditReport {
	o := newOwners(s)
	ac := newAuditContext(s)
	nsFilter := map[string]bool{}
	for _, n := range scope.Namespaces {
		nsFilter[n] = true
	}
	rep := AuditReport{GeneratedAt: s.Now, Unavailable: s.Unavailable, Categories: map[Category]int{}}
	results := make([]CheckResult, len(checks))
	for i, c := range checks {
		results[i] = CheckResult{Check: c.Check}
	}
	type acc struct{ total, lost, res, crit, warn, info int }
	byNS := map[string]*acc{}
	catTotal, catLost := map[Category]int{}, map[Category]int{}
	cluster := &acc{}
	for _, w := range workloads(s, o) {
		if len(nsFilter) > 0 && !nsFilter[w.Ref.Namespace] {
			continue
		}
		if !scope.IncludeSystem && len(nsFilter) == 0 && isSystemNamespace(w.Ref.Namespace) {
			continue
		}
		if scope.Resource != nil && (scope.Resource.Kind != w.Ref.Kind || scope.Resource.Name != w.Ref.Name || scope.Resource.Namespace != w.Ref.Namespace) {
			continue
		}
		a := byNS[w.Ref.Namespace]
		if a == nil {
			a = &acc{}
			byNS[w.Ref.Namespace] = a
		}
		a.res++
		cluster.res++
		for i, c := range checks {
			wt := severityWeight[c.Severity]
			a.total += wt
			cluster.total += wt
			catTotal[c.Category] += wt
			fails := c.eval(w, ac)
			lost := false
			for _, f := range fails {
				fd := Finding{CheckID: c.ID, Severity: c.Severity, Category: c.Category, Resource: w.Ref, Container: f.container, Message: f.message}
				if ignored != nil && ignored(fd) {
					fd.Ignored = true
				} else {
					lost = true
				}
				rep.Findings = append(rep.Findings, fd)
			}
			switch {
			case len(fails) == 0:
				results[i].Passed++
			case lost:
				results[i].Failed++
				a.lost += wt
				cluster.lost += wt
				catLost[c.Category] += wt
				switch c.Severity {
				case SeverityCritical:
					a.crit++
				case SeverityWarning:
					a.warn++
				default:
					a.info++
				}
			default:
				results[i].Ignored++
				results[i].Passed++
			}
		}
	}
	score := func(total, lost int) int {
		if total == 0 {
			return 100
		}
		return int(float64(total-lost)/float64(total)*100 + 0.5)
	}
	rep.Score = score(cluster.total, cluster.lost)
	rep.Resources = cluster.res
	for ns, a := range byNS {
		rep.Namespaces = append(rep.Namespaces, NamespaceScore{Namespace: ns, Score: score(a.total, a.lost), Resources: a.res, Critical: a.crit, Warning: a.warn, Info: a.info})
	}
	sort.Slice(rep.Namespaces, func(i, j int) bool {
		if rep.Namespaces[i].Score != rep.Namespaces[j].Score {
			return rep.Namespaces[i].Score < rep.Namespaces[j].Score
		}
		return rep.Namespaces[i].Namespace < rep.Namespaces[j].Namespace
	})
	for cat, t := range catTotal {
		rep.Categories[cat] = score(t, catLost[cat])
	}
	sort.SliceStable(results, func(i, j int) bool {
		if Rank(results[i].Severity) != Rank(results[j].Severity) {
			return Rank(results[i].Severity) > Rank(results[j].Severity)
		}
		return results[i].Failed > results[j].Failed
	})
	rep.Checks = results
	sort.SliceStable(rep.Findings, func(i, j int) bool {
		a, b := rep.Findings[i], rep.Findings[j]
		if Rank(a.Severity) != Rank(b.Severity) {
			return Rank(a.Severity) > Rank(b.Severity)
		}
		return a.Resource.Key() < b.Resource.Key()
	})
	if rep.Findings == nil {
		rep.Findings = []Finding{}
	}
	if rep.Namespaces == nil {
		rep.Namespaces = []NamespaceScore{}
	}
	return rep
}
