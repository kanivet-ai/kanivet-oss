package network

import (
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// maxPeers bounds the workloads listed as allowed peers.
const maxPeers = 200

// workloadGroup is a workload and its pods.
type workloadGroup struct {
	ref  ObjectRef
	pods []*corev1.Pod
}

func (s *Snapshot) workloads(ns string) []workloadGroup {
	idx := map[ObjectRef]int{}
	var out []workloadGroup
	for _, p := range s.Pods {
		if ns != "" && p.Namespace != ns {
			continue
		}
		k := workloadKey(p)
		i, ok := idx[k]
		if !ok {
			i = len(out)
			idx[k] = i
			out = append(out, workloadGroup{ref: k})
		}
		out[i].pods = append(out[i].pods, p)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].ref, out[j].ref
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Kind < b.Kind
	})
	return out
}

func isolation(s *Snapshot, p *corev1.Pod) (in, eg []*netv1.NetworkPolicy) {
	for _, np := range s.policiesIn(p.Namespace) {
		if !selects(np, p) {
			continue
		}
		i, e := policyTypes(np)
		if i {
			in = append(in, np)
		}
		if e {
			eg = append(eg, np)
		}
	}
	return
}

// Coverage reports which pods of ns ("" for all) NetworkPolicies isolate.
func (s *Snapshot) Coverage(ns string) *CoverageReport {
	rep := &CoverageReport{Namespace: ns, Enforcer: s.Enforcer, Warnings: append([]string(nil), s.Warnings...), Workloads: []WorkloadCoverage{}, Policies: []PolicySummary{}}
	if len(s.Policies) > 0 && s.Enforcer == "" {
		rep.Warnings = append(rep.Warnings, "No NetworkPolicy enforcer was recognised; if the CNI does not enforce policies, every pod is open whatever this shows.")
	}
	for _, w := range s.workloads(ns) {
		wc := WorkloadCoverage{Workload: w.ref, Pods: len(w.pods), IngressPolicies: []ObjectRef{}, EgressPolicies: []ObjectRef{}}
		// Replicas share labels; the first pod speaks for the workload.
		in, eg := isolation(s, w.pods[0])
		wc.IngressIsolated, wc.EgressIsolated = len(in) > 0, len(eg) > 0
		wc.IngressPolicies, wc.EgressPolicies = policyRefs(in), policyRefs(eg)
		switch {
		case wc.IngressIsolated && wc.EgressIsolated:
			wc.Status = "isolated"
		case wc.IngressIsolated || wc.EgressIsolated:
			wc.Status = "partial"
		default:
			wc.Status = "open"
		}
		rep.Pods += len(w.pods)
		if wc.IngressIsolated {
			rep.IngressIsolated += len(w.pods)
		}
		if wc.EgressIsolated {
			rep.EgressIsolated += len(w.pods)
		}
		rep.Workloads = append(rep.Workloads, wc)
	}
	for _, np := range s.Policies {
		if ns != "" && np.Namespace != ns {
			continue
		}
		rep.Policies = append(rep.Policies, s.policySummary(np))
	}
	return rep
}

func (s *Snapshot) policySummary(np *netv1.NetworkPolicy) PolicySummary {
	in, eg := policyTypes(np)
	sum := PolicySummary{Policy: policyRef(np), PolicyTypes: policyTypeNames(np), IngressRules: len(np.Spec.Ingress), EgressRules: len(np.Spec.Egress), Selector: selectorText(np.Spec.PodSelector)}
	for _, p := range s.Pods {
		if selects(np, p) {
			sum.SelectedPods++
		}
	}
	all := isEmptySelector(&np.Spec.PodSelector)
	sum.DefaultDenyIngress = all && in && len(np.Spec.Ingress) == 0
	sum.DefaultDenyEgress = all && eg && len(np.Spec.Egress) == 0
	return sum
}

// ResourceNetwork is what can reach the pods of r, and what they can reach.
func (s *Snapshot) ResourceNetwork(r Ref) (*ResourceNetwork, error) {
	r.Kind = normalizeKind(r.Kind)
	out := &ResourceNetwork{Subject: r, Pods: []PodRef{}, Services: []ServiceView{}, Enforcer: s.Enforcer, Warnings: append([]string(nil), s.Warnings...)}
	var pods []*corev1.Pod
	switch r.Kind {
	case "Service":
		svc := s.service(r.Namespace, r.Name)
		if svc == nil {
			return nil, fmt.Errorf("service %s/%s not found", r.Namespace, r.Name)
		}
		out.Services = append(out.Services, s.serviceView(svc))
		pods = s.serviceBackends(svc)
	case "Pod", "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Job":
		pods = runningFirst(s.podsOf(r))
	default:
		return nil, fmt.Errorf("network view is not available for %s", r.Kind)
	}
	for _, p := range capPods(pods) {
		out.Pods = append(out.Pods, podRef(p))
	}
	if len(pods) == 0 {
		out.Ingress = DirectionView{Policies: []ObjectRef{}, Rules: []RuleView{}, Peers: []Peer{}, Summary: "No running pods to evaluate."}
		out.Egress = out.Ingress
		return out, nil
	}
	rep := pods[0]
	if r.Kind != "Service" {
		for _, svc := range s.servicesSelecting(rep) {
			out.Services = append(out.Services, s.serviceView(svc))
		}
	}
	if rep.Spec.HostNetwork {
		out.Warnings = append(out.Warnings, "These pods use the host network: NetworkPolicies usually do not apply to them.")
	}
	out.Ingress = s.directionView(rep, "ingress")
	out.Egress = s.directionView(rep, "egress")
	return out, nil
}

func (s *Snapshot) serviceBackends(svc *corev1.Service) []*corev1.Pod {
	var out []*corev1.Pod
	seen := map[string]bool{}
	for _, sl := range s.slicesFor(svc) {
		for _, ep := range sl.Endpoints {
			if ep.TargetRef == nil || ep.TargetRef.Kind != "Pod" {
				continue
			}
			if p := s.pod(ep.TargetRef.Namespace, ep.TargetRef.Name); p != nil && !seen[p.Name] {
				seen[p.Name] = true
				out = append(out, p)
			}
		}
	}
	if len(out) == 0 && len(svc.Spec.Selector) > 0 {
		for _, p := range s.Pods {
			for _, x := range s.servicesSelecting(p) {
				if x == svc {
					out = append(out, p)
				}
			}
		}
	}
	return runningFirst(out)
}

func (s *Snapshot) serviceView(svc *corev1.Service) ServiceView {
	v := ServiceView{Service: serviceRef(svc), Type: string(svc.Spec.Type), Ports: []PortMapping{}}
	if v.Type == "" {
		v.Type = string(corev1.ServiceTypeClusterIP)
	}
	backends := s.serviceBackends(svc)
	slices := s.slicesFor(svc)
	for _, sp := range svc.Spec.Ports {
		proto := sp.Protocol
		if proto == "" {
			proto = corev1.ProtocolTCP
		}
		target := sp.TargetPort
		if target.Type == intstr.Int && target.IntVal == 0 {
			target = intstr.FromInt32(sp.Port)
		}
		pm := PortMapping{Name: sp.Name, Port: sp.Port, Protocol: string(proto), TargetPort: target.String()}
		for _, sl := range slices {
			for _, ep := range sl.Endpoints {
				pm.TotalEndpoints++
				if ep.Conditions.Ready == nil || *ep.Conditions.Ready {
					pm.ReadyEndpoints++
				}
			}
		}
		if len(slices) == 0 {
			for _, p := range backends {
				pm.TotalEndpoints++
				if isReady(p) {
					pm.ReadyEndpoints++
				}
			}
		}
		switch {
		case svc.Spec.Type == corev1.ServiceTypeExternalName:
			pm.Detail = "ExternalName: " + svc.Spec.ExternalName
		case len(backends) == 0:
			pm.Detail = "No backing pods."
		default:
			b := backends[0]
			pm.ResolvedPort = resolveTarget(b, target, proto)
			if pm.ResolvedPort == 0 {
				pm.Detail = fmt.Sprintf("targetPort %q is not a port name any container declares.", target.StrVal)
				break
			}
			h := portHop([]backend{{pod: b, port: pm.ResolvedPort}}, proto)
			pm.Exposed = h.Status == StatusPass
			pm.Detail = h.Detail
		}
		v.Ports = append(v.Ports, pm)
	}
	return v
}

// directionView describes one direction for a representative pod.
func (s *Snapshot) directionView(p *corev1.Pod, dir string) DirectionView {
	v := DirectionView{Policies: []ObjectRef{}, Rules: []RuleView{}, Peers: []Peer{}}
	in, eg := isolation(s, p)
	pols := in
	if dir == "egress" {
		pols = eg
	}
	if len(pols) == 0 {
		if dir == "ingress" {
			v.Summary = "Open: no NetworkPolicy selects these pods for ingress, so any pod or address can connect."
		} else {
			v.Summary = "Open: no NetworkPolicy selects these pods for egress, so they can connect anywhere."
		}
		return v
	}
	v.Isolated = true
	v.Policies = policyRefs(pols)
	anyPeer := false
	cidrs := map[string]bool{}
	for _, np := range pols {
		for _, rv := range s.ruleViews(np, dir) {
			if rv.Matches == -1 {
				anyPeer = true
			}
			v.Rules = append(v.Rules, rv)
		}
		for _, peers := range rulePeers(np, dir) {
			for _, pr := range peers {
				if pr.IPBlock != nil {
					cidrs[describePeer(pr, np.Namespace)] = true
				}
			}
		}
	}
	for c := range cidrs {
		v.ExternalCIDRs = append(v.ExternalCIDRs, c)
	}
	sort.Strings(v.ExternalCIDRs)
	switch {
	case len(v.Rules) == 0:
		v.Summary = fmt.Sprintf("Default-deny: %s isolate these pods for %s and allow nothing.", policyNames(pols), dir)
	case anyPeer:
		v.Summary = fmt.Sprintf("Isolated by %s, but a rule allows any %s on some ports.", policyNames(pols), map[string]string{"ingress": "source", "egress": "destination"}[dir])
	default:
		v.Summary = fmt.Sprintf("Isolated by %s: only the peers below are allowed.", policyNames(pols))
	}
	// Concrete peers: each workload's first pod, any port.
	for _, w := range s.workloads("") {
		other := w.pods[0]
		var ports []string
		for _, np := range pols {
			for i, peers := range rulePeers(np, dir) {
				ok := len(peers) == 0
				for _, pr := range peers {
					if s.peerMatches(np, pr, peer{pod: other}) {
						ok = true
						break
					}
				}
				if ok {
					ports = append(ports, portStrings(rulePortsOf(np, dir)[i])...)
				}
			}
		}
		if len(ports) == 0 {
			continue
		}
		if len(v.Peers) >= maxPeers {
			v.PeersTruncated = true
			break
		}
		v.Peers = append(v.Peers, Peer{Workload: w.ref, Ports: dedupe(ports)})
	}
	return v
}

func rulePeers(np *netv1.NetworkPolicy, dir string) [][]netv1.NetworkPolicyPeer {
	var out [][]netv1.NetworkPolicyPeer
	if dir == "ingress" {
		for _, r := range np.Spec.Ingress {
			out = append(out, r.From)
		}
	} else {
		for _, r := range np.Spec.Egress {
			out = append(out, r.To)
		}
	}
	return out
}

func rulePortsOf(np *netv1.NetworkPolicy, dir string) [][]netv1.NetworkPolicyPort {
	var out [][]netv1.NetworkPolicyPort
	if dir == "ingress" {
		for _, r := range np.Spec.Ingress {
			out = append(out, r.Ports)
		}
	} else {
		for _, r := range np.Spec.Egress {
			out = append(out, r.Ports)
		}
	}
	return out
}

// ruleViews renders a policy's rules for one direction, counting the
// workloads each rule's peers select.
func (s *Snapshot) ruleViews(np *netv1.NetworkPolicy, dir string) []RuleView {
	in, eg := policyTypes(np)
	if (dir == "ingress" && !in) || (dir == "egress" && !eg) {
		return nil
	}
	ports := rulePortsOf(np, dir)
	var out []RuleView
	for i, peers := range rulePeers(np, dir) {
		rv := RuleView{Rule: RuleRef{Policy: policyRef(np), Direction: dir, Index: i + 1}, Ports: portStrings(ports[i])}
		if len(peers) == 0 {
			rv.Peers = []string{describePeers(nil, np.Namespace, dir)}
			rv.Matches = -1
		} else {
			for _, pr := range peers {
				rv.Peers = append(rv.Peers, describePeer(pr, np.Namespace))
			}
			for _, w := range s.workloads("") {
				for _, pr := range peers {
					if s.peerMatches(np, pr, peer{pod: w.pods[0]}) {
						rv.Matches++
						break
					}
				}
			}
		}
		out = append(out, rv)
	}
	return out
}

// PolicyDetail is what a NetworkPolicy selects and allows.
func (s *Snapshot) PolicyDetail(ns, name string) (*PolicyDetail, error) {
	var np *netv1.NetworkPolicy
	for _, x := range s.policiesIn(ns) {
		if x.Name == name {
			np = x
		}
	}
	if np == nil {
		return nil, fmt.Errorf("NetworkPolicy %s/%s not found", ns, name)
	}
	d := &PolicyDetail{Policy: policyRef(np), PolicyTypes: policyTypeNames(np), Selector: selectorText(np.Spec.PodSelector), SelectedPods: []PodRef{}, SelectedWorkloads: []ObjectRef{}, Ingress: []RuleView{}, Egress: []RuleView{}, Enforcer: s.Enforcer, Warnings: append([]string(nil), s.Warnings...)}
	seen := map[ObjectRef]bool{}
	for _, p := range s.Pods {
		if !selects(np, p) {
			continue
		}
		if len(d.SelectedPods) < maxPairPods {
			d.SelectedPods = append(d.SelectedPods, podRef(p))
		}
		if k := workloadKey(p); !seen[k] {
			seen[k] = true
			d.SelectedWorkloads = append(d.SelectedWorkloads, k)
		}
	}
	d.Ingress = append(d.Ingress, s.ruleViews(np, "ingress")...)
	d.Egress = append(d.Egress, s.ruleViews(np, "egress")...)
	in, eg := policyTypes(np)
	who := fmt.Sprintf("Selects %s in %s (%d pods now)", selectorText(np.Spec.PodSelector), np.Namespace, countSelected(s, np))
	d.Summary = append(d.Summary, who+".")
	if in {
		d.Summary = append(d.Summary, directionSentence("ingress", len(np.Spec.Ingress)))
	}
	if eg {
		d.Summary = append(d.Summary, directionSentence("egress", len(np.Spec.Egress)))
	}
	if !in {
		d.Summary = append(d.Summary, "Does not restrict ingress.")
	}
	if !eg {
		d.Summary = append(d.Summary, "Does not restrict egress.")
	}
	if eg && !allowsDNS(np) && len(np.Spec.Egress) > 0 {
		d.Warnings = append(d.Warnings, "Egress is restricted but no rule allows port 53: selected pods may be unable to resolve DNS names.")
	}
	return d, nil
}

func countSelected(s *Snapshot, np *netv1.NetworkPolicy) int {
	n := 0
	for _, p := range s.Pods {
		if selects(np, p) {
			n++
		}
	}
	return n
}

func directionSentence(dir string, rules int) string {
	if rules == 0 {
		return fmt.Sprintf("Denies all %s to the selected pods (no rules).", dir)
	}
	return fmt.Sprintf("Allows %s only as %d rule(s) below permit; everything else is denied.", dir, rules)
}

// allowsDNS reports whether some egress rule permits port 53.
func allowsDNS(np *netv1.NetworkPolicy) bool {
	for _, r := range np.Spec.Egress {
		if portMatches(r.Ports, 53, corev1.ProtocolUDP, nil) || portMatches(r.Ports, 53, corev1.ProtocolTCP, nil) {
			return true
		}
	}
	return false
}

// maxTargets bounds the picker list.
const maxTargets = 3000

// Targets lists what the path checker can pick, workloads and Services
// first, then bare pods.
func (s *Snapshot) Targets() *Targets {
	t := &Targets{Namespaces: []string{}, Targets: []Target{}}
	nss := map[string]bool{}
	for n := range s.Namespaces {
		nss[n] = true
	}
	for _, w := range s.workloads("") {
		nss[w.ref.Namespace] = true
		ports := declaredPorts(w.pods[0], corev1.ProtocolTCP)
		t.Targets = append(t.Targets, Target{Ref: Ref{Kind: w.ref.Kind, Namespace: w.ref.Namespace, Name: w.ref.Name}, Pods: len(w.pods), Ports: dedupeInt(ports)})
	}
	for _, svc := range s.Services {
		nss[svc.Namespace] = true
		tg := Target{Ref: Ref{Kind: "Service", Namespace: svc.Namespace, Name: svc.Name}}
		for _, p := range svc.Spec.Ports {
			tg.Ports = append(tg.Ports, p.Port)
			tg.PortNames = append(tg.PortNames, p.Name)
		}
		tg.Pods = len(s.serviceBackends(svc))
		t.Targets = append(t.Targets, tg)
	}
	for _, p := range s.Pods {
		if workloadOf(p) == nil {
			continue // a bare pod is its own workload group, listed above
		}
		t.Targets = append(t.Targets, Target{Ref: Ref{Kind: "Pod", Namespace: p.Namespace, Name: p.Name}, Pods: 1, Ports: dedupeInt(declaredPorts(p, corev1.ProtocolTCP))})
	}
	if len(t.Targets) > maxTargets {
		t.Targets = t.Targets[:maxTargets]
		t.Truncated = true
	}
	for n := range nss {
		t.Namespaces = append(t.Namespaces, n)
	}
	sort.Strings(t.Namespaces)
	return t
}

func dedupe(in []string) []string {
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

func dedupeInt(in []int32) []int32 {
	seen := map[int32]bool{}
	var out []int32
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
