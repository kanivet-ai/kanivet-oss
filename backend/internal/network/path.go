package network

import (
	"fmt"
	"net"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// maxPairPods bounds the pods taken from each end: policy verdicts depend on
// labels, and replicas share them.
const maxPairPods = 50

// backend is one destination pod and the port it is reached on.
type backend struct {
	pod  *corev1.Pod
	port int32
}

// CheckPath evaluates req against the snapshot. It never errors on a bad
// request: an unknown source or destination is a failed hop.
func (s *Snapshot) CheckPath(req PathRequest) *PathResult {
	proto := corev1.Protocol(strings.ToUpper(req.Protocol))
	if proto == "" {
		proto = corev1.ProtocolTCP
	}
	res := &PathResult{Port: req.Port, Protocol: string(proto), Enforcer: s.Enforcer, Warnings: append([]string(nil), s.Warnings...)}
	if len(s.Policies) > 0 && s.Enforcer == "" {
		res.Warnings = append(res.Warnings, "No NetworkPolicy enforcer (Cilium, Calico, Antrea, ...) was recognised: if the CNI does not enforce policies, they have no effect and everything is allowed.")
	}

	// Source.
	srcKind := normalizeKind(req.Source.Kind)
	req.Source.Kind = srcKind
	var srcPods []*corev1.Pod
	switch srcKind {
	case "Service", "IP", "NetworkPolicy":
		res.Hops = append(res.Hops, Hop{ID: "source", Kind: "source", Title: describeRef(req.Source), Status: StatusFail, Detail: "The source must be a pod or a workload."})
		return finish(res)
	default:
		srcPods = runningFirst(s.podsOf(req.Source))
	}
	if len(srcPods) == 0 {
		res.Hops = append(res.Hops, Hop{ID: "source", Kind: "source", Title: describeRef(req.Source), Status: StatusFail, Detail: "No running pods found for the source.", Objects: []ObjectRef{refObject(req.Source)}})
		return finish(res)
	}
	srcPods = capPods(srcPods)
	for _, p := range srcPods {
		res.SourcePods = append(res.SourcePods, podRef(p))
	}
	srcHop := Hop{ID: "source", Kind: "source", Title: describeRef(req.Source), Status: StatusPass, Detail: podCountText(srcPods), Objects: []ObjectRef{refObject(req.Source)}}
	if srcPods[0].Spec.HostNetwork {
		srcHop.Status = StatusWarn
		srcHop.Detail += "; uses the host network, so NetworkPolicies may not apply to its traffic"
	}
	res.Hops = append(res.Hops, srcHop)

	// Destination.
	dstKind := normalizeKind(req.Destination.Kind)
	req.Destination.Kind = dstKind
	var backends []backend
	var external string
	switch dstKind {
	case "IP":
		ip := strings.TrimSpace(req.Destination.IP)
		if ip == "" {
			ip = req.Destination.Name
		}
		if net.ParseIP(ip) == nil {
			res.Hops = append(res.Hops, Hop{ID: "destination", Kind: "destination", Title: ip, Status: StatusFail, Detail: "Not a valid IP address."})
			return finish(res)
		}
		// An address that is a pod's is that pod.
		for _, p := range s.Pods {
			if p.Status.PodIP == ip && !p.Spec.HostNetwork {
				backends = []backend{{pod: p, port: req.Port}}
				break
			}
		}
		if backends == nil {
			external = ip
		}
	case "Service":
		svc := s.service(req.Destination.Namespace, req.Destination.Name)
		if svc == nil {
			res.Hops = append(res.Hops, Hop{ID: "service", Kind: "service", Title: describeRef(req.Destination), Status: StatusFail, Detail: "Service not found."})
			return finish(res)
		}
		var ok bool
		backends, ok = s.serviceHops(res, svc, req.Port, proto)
		if !ok {
			return finish(res)
		}
	default:
		pods := runningFirst(s.podsOf(req.Destination))
		if len(pods) == 0 {
			res.Hops = append(res.Hops, Hop{ID: "destination", Kind: "destination", Title: describeRef(req.Destination), Status: StatusFail, Detail: "No running pods found for the destination.", Objects: []ObjectRef{refObject(req.Destination)}})
			return finish(res)
		}
		port := req.Port
		if port == 0 {
			if ports := declaredPorts(pods[0], proto); len(ports) == 1 {
				port = ports[0]
				res.Port = port
			}
		}
		for _, p := range capPods(pods) {
			backends = append(backends, backend{pod: p, port: port})
		}
		if !anyReady(backends) {
			res.Hops = append(res.Hops, Hop{ID: "endpoints", Kind: "endpoints", Title: "Destination pods", Status: StatusWarn, Detail: "None of the destination pods is ready; connections may be refused."})
		}
	}
	for _, b := range backends {
		res.DestPods = append(res.DestPods, podRef(b.pod))
	}

	// Egress from the source's namespace, ingress into the destination's.
	egressHop := Hop{ID: "egress", Kind: "egress", Title: "Egress from " + srcPods[0].Namespace}
	ingressHop := Hop{ID: "ingress", Kind: "ingress"}
	if external == "" {
		ingressHop.Title = "Ingress into " + backends[0].pod.Namespace
	}
	var egressAllowed, ingressAllowed, pairsAllowed, pairs int
	var egRep, inRep *decision
	var egFail, inFail *decision
	egPolicies := map[string]ObjectRef{}
	inPolicies := map[string]ObjectRef{}
	allowedBy := map[string]map[RuleRef]bool{"egress": {}, "ingress": {}}
	for _, sp := range srcPods {
		targets := backends
		if external != "" {
			targets = []backend{{port: req.Port}}
		}
		for _, b := range targets {
			pairs++
			dst := peer{pod: b.pod, ip: external}
			eg := s.evalEgress(sp, dst, b.port, proto)
			for _, np := range eg.selecting {
				egPolicies[np.Namespace+"/"+np.Name] = policyRef(np)
			}
			for _, r := range eg.allowedBy {
				allowedBy["egress"][r] = true
			}
			if eg.allowed {
				egressAllowed++
				if egRep == nil {
					e := eg
					egRep = &e
				}
			} else if egFail == nil {
				e := eg
				egFail = &e
			}
			inOK := true
			if b.pod != nil {
				in := s.evalIngress(b.pod, peer{pod: sp}, b.port, proto)
				for _, np := range in.selecting {
					inPolicies[np.Namespace+"/"+np.Name] = policyRef(np)
				}
				for _, r := range in.allowedBy {
					allowedBy["ingress"][r] = true
				}
				inOK = in.allowed
				if in.allowed {
					ingressAllowed++
					if inRep == nil {
						i := in
						inRep = &i
					}
				} else if inFail == nil {
					i := in
					inFail = &i
				}
			}
			if eg.allowed && inOK {
				pairsAllowed++
			}
		}
	}
	res.Pairs, res.PairsAllowed = pairs, pairsAllowed
	fillPolicyHop(&egressHop, "egress", egressAllowed, pairs, egRep, egFail, egPolicies, allowedBy["egress"])
	res.Hops = insertBeforeService(res.Hops, egressHop)
	if external == "" {
		fillPolicyHop(&ingressHop, "ingress", ingressAllowed, pairs, inRep, inFail, inPolicies, allowedBy["ingress"])
		res.Hops = append(res.Hops, ingressHop)
		res.Hops = append(res.Hops, portHop(backends, proto))
		res.Hops = append(res.Hops, Hop{ID: "destination", Kind: "destination", Title: describeRef(req.Destination), Status: StatusPass, Detail: podCountText(podsOfBackends(backends)), Objects: []ObjectRef{refObject(req.Destination)}})
	} else {
		res.Hops = append(res.Hops, Hop{ID: "destination", Kind: "destination", Title: external, Status: StatusPass, Detail: "Outside the cluster: only the source's egress policies apply here; firewalls and security groups beyond the cluster are not checked."})
	}
	return finish(res)
}

// insertBeforeService puts the egress hop right after the source.
func insertBeforeService(hops []Hop, h Hop) []Hop {
	out := make([]Hop, 0, len(hops)+1)
	out = append(out, hops[0], h)
	return append(out, hops[1:]...)
}

func fillPolicyHop(h *Hop, dir string, allowed, pairs int, rep, fail *decision, policies map[string]ObjectRef, by map[RuleRef]bool) {
	for _, r := range sortedRefs(policies) {
		h.Policies = append(h.Policies, r)
	}
	for r := range by {
		h.AllowedBy = append(h.AllowedBy, r)
	}
	sort.Slice(h.AllowedBy, func(i, j int) bool {
		a, b := h.AllowedBy[i], h.AllowedBy[j]
		if a.Policy.Name != b.Policy.Name {
			return a.Policy.Name < b.Policy.Name
		}
		return a.Index < b.Index
	})
	switch {
	case allowed == pairs:
		h.Status = StatusPass
		if rep == nil || !rep.isolated {
			h.Detail = fmt.Sprintf("No NetworkPolicy selects the %s pods for %s: %s is open.", sideName(dir), dir, dir)
		} else {
			h.Detail = "Allowed by " + ruleList(h.AllowedBy) + "."
			h.Reasons = rep.reasons
		}
	case allowed == 0:
		h.Status = StatusFail
		h.Detail = blockedText(dir, fail)
		h.Reasons = fail.reasons
	default:
		h.Status = StatusWarn
		h.Detail = fmt.Sprintf("Allowed for %d of %d pod pairs. %s", allowed, pairs, blockedText(dir, fail))
		h.Reasons = fail.reasons
	}
}

func sideName(dir string) string {
	if dir == "egress" {
		return "source"
	}
	return "destination"
}

func blockedText(dir string, d *decision) string {
	if d == nil {
		return "Blocked."
	}
	if len(d.defaultDeny) == len(d.selecting) {
		return fmt.Sprintf("Blocked: default-deny %s in %s (%s).", dir, d.selecting[0].Namespace, policyNames(d.defaultDeny))
	}
	return fmt.Sprintf("Blocked: isolated for %s by %s and no rule matches.", dir, policyNames(d.selecting))
}

func ruleList(rs []RuleRef) string {
	parts := make([]string, 0, len(rs))
	for _, r := range rs {
		parts = append(parts, fmt.Sprintf("%s rule %d", r.Policy.Name, r.Index))
	}
	if len(parts) > 3 {
		return strings.Join(parts[:3], ", ") + fmt.Sprintf(" and %d more", len(parts)-3)
	}
	return strings.Join(parts, ", ")
}

// serviceHops adds the Service and endpoints hops and returns the backends.
func (s *Snapshot) serviceHops(res *PathResult, svc *corev1.Service, port int32, proto corev1.Protocol) ([]backend, bool) {
	ref := serviceRef(svc)
	h := Hop{ID: "service", Kind: "service", Title: "Service " + svc.Namespace + "/" + svc.Name, Objects: []ObjectRef{ref}}
	if svc.Spec.Type == corev1.ServiceTypeExternalName {
		h.Status = StatusWarn
		h.Detail = "ExternalName Service: resolves to " + svc.Spec.ExternalName + " outside the cluster; only egress policies with ipBlocks can apply."
		res.Hops = append(res.Hops, h)
		return nil, false
	}
	var sp *corev1.ServicePort
	for i := range svc.Spec.Ports {
		p := &svc.Spec.Ports[i]
		pp := p.Protocol
		if pp == "" {
			pp = corev1.ProtocolTCP
		}
		if pp == proto && (p.Port == port || (port == 0 && len(svc.Spec.Ports) == 1)) {
			sp = p
			break
		}
	}
	if sp == nil {
		h.Status = StatusFail
		h.Detail = fmt.Sprintf("The Service has no %s port %d (ports: %s).", proto, port, servicePortList(svc))
		res.Hops = append(res.Hops, h)
		return nil, false
	}
	res.Port = sp.Port
	target := sp.TargetPort
	if target.Type == intstr.Int && target.IntVal == 0 {
		target = intstr.FromInt32(sp.Port)
	}
	h.Status = StatusPass
	h.Detail = fmt.Sprintf("Service port %d → targetPort %s", sp.Port, target.String())
	if svc.Spec.ClusterIP == corev1.ClusterIPNone {
		h.Detail += " (headless: clients connect to pod IPs directly)"
	}
	res.Hops = append(res.Hops, h)

	// Endpoints: EndpointSlices when there are any, the selector otherwise.
	var backends []backend
	ready, total := 0, 0
	unresolved := 0
	slices := s.slicesFor(svc)
	if len(slices) > 0 {
		for _, sl := range slices {
			slicePort := int32(0)
			for _, p := range sl.Ports {
				if p.Name != nil && *p.Name == sp.Name && p.Port != nil {
					slicePort = *p.Port
				}
			}
			for _, ep := range sl.Endpoints {
				total++
				if ep.Conditions.Ready != nil && !*ep.Conditions.Ready {
					continue
				}
				ready++
				if ep.TargetRef == nil || ep.TargetRef.Kind != "Pod" {
					continue
				}
				p := s.pod(ep.TargetRef.Namespace, ep.TargetRef.Name)
				if p == nil {
					continue
				}
				port := slicePort
				if port == 0 {
					port = resolveTarget(p, target, proto)
				}
				if port == 0 {
					unresolved++
					continue
				}
				backends = append(backends, backend{pod: p, port: port})
			}
		}
	} else if len(svc.Spec.Selector) > 0 {
		sel := labels.SelectorFromSet(svc.Spec.Selector)
		for _, p := range s.Pods {
			if p.Namespace != svc.Namespace || !sel.Matches(labels.Set(p.Labels)) {
				continue
			}
			total++
			if !isReady(p) {
				continue
			}
			ready++
			port := resolveTarget(p, target, proto)
			if port == 0 {
				unresolved++
				continue
			}
			backends = append(backends, backend{pod: p, port: port})
		}
	}
	eh := Hop{ID: "endpoints", Kind: "endpoints", Title: "Endpoints"}
	switch {
	case total == 0 && len(svc.Spec.Selector) == 0:
		eh.Status = StatusFail
		eh.Detail = "The Service has no selector and no EndpointSlices: nothing receives its traffic."
	case total == 0:
		eh.Status = StatusFail
		eh.Detail = "No pods match the selector " + labels.SelectorFromSet(svc.Spec.Selector).String() + "."
	case ready == 0:
		eh.Status = StatusFail
		eh.Detail = fmt.Sprintf("No ready endpoints (0 of %d ready).", total)
	case len(backends) == 0 && unresolved > 0:
		eh.Status = StatusFail
		eh.Detail = fmt.Sprintf("targetPort %s is a port name no container of the backing pods declares.", target.String())
	case len(backends) == 0:
		eh.Status = StatusWarn
		eh.Detail = fmt.Sprintf("%d of %d endpoints ready, but none is a pod in the cache; policies cannot be evaluated.", ready, total)
	case ready < total:
		eh.Status = StatusWarn
		eh.Detail = fmt.Sprintf("%d of %d endpoints ready.", ready, total)
	default:
		eh.Status = StatusPass
		eh.Detail = fmt.Sprintf("%d of %d endpoints ready.", ready, total)
	}
	res.Hops = append(res.Hops, eh)
	if len(backends) == 0 {
		return nil, false
	}
	return capBackends(backends), true
}

func (s *Snapshot) slicesFor(svc *corev1.Service) []*discoveryv1.EndpointSlice {
	var out []*discoveryv1.EndpointSlice
	for _, sl := range s.Slices {
		if sl.Namespace == svc.Namespace && sl.Labels[discoveryv1.LabelServiceName] == svc.Name {
			out = append(out, sl)
		}
	}
	return out
}

// resolveTarget turns a targetPort into the pod's port number, 0 when a name
// does not resolve.
func resolveTarget(p *corev1.Pod, target intstr.IntOrString, proto corev1.Protocol) int32 {
	if target.Type == intstr.Int {
		return target.IntVal
	}
	return namedPorts(p, proto)[target.StrVal]
}

func declaredPorts(p *corev1.Pod, proto corev1.Protocol) []int32 {
	var out []int32
	for _, c := range p.Spec.Containers {
		for _, cp := range c.Ports {
			pp := cp.Protocol
			if pp == "" {
				pp = corev1.ProtocolTCP
			}
			if pp == proto {
				out = append(out, cp.ContainerPort)
			}
		}
	}
	return out
}

// portHop checks that a container declares the port traffic lands on.
// Declaring is informational in Kubernetes, so a miss is a warning: the
// process may still listen.
func portHop(backends []backend, proto corev1.Protocol) Hop {
	h := Hop{ID: "port", Kind: "port", Title: "Container port"}
	b := backends[0]
	if b.port == 0 {
		h.Status = StatusSkip
		h.Detail = "No port given; policies were evaluated for any port."
		return h
	}
	for _, c := range b.pod.Spec.Containers {
		for _, cp := range c.Ports {
			pp := cp.Protocol
			if pp == "" {
				pp = corev1.ProtocolTCP
			}
			if cp.ContainerPort == b.port && pp == proto {
				h.Status = StatusPass
				name := ""
				if cp.Name != "" {
					name = " (" + cp.Name + ")"
				}
				h.Detail = fmt.Sprintf("Container %s exposes %d/%s%s.", c.Name, b.port, proto, name)
				return h
			}
		}
	}
	declared := declaredPorts(b.pod, proto)
	if len(declared) == 0 {
		h.Status = StatusSkip
		h.Detail = fmt.Sprintf("Port %d: the containers declare no ports, so this cannot be verified.", b.port)
		return h
	}
	h.Status = StatusWarn
	h.Detail = fmt.Sprintf("Port %d is not exposed by any container (declared: %s); unless the process listens on it anyway, connections will be refused.", b.port, int32List(declared))
	return h
}

func finish(res *PathResult) *PathResult {
	failed, warned := 0, 0
	var firstFail *Hop
	for i := range res.Hops {
		switch res.Hops[i].Status {
		case StatusFail:
			failed++
			if firstFail == nil {
				firstFail = &res.Hops[i]
			}
		case StatusWarn:
			warned++
		}
	}
	switch {
	case failed > 0:
		res.Verdict = VerdictBlocked
		res.Summary = firstFail.Title + ": " + firstFail.Detail
	case warned > 0:
		res.Verdict = VerdictPartial
		res.Summary = "Reachable with caveats: see the highlighted steps."
	default:
		res.Verdict = VerdictAllowed
		res.Summary = "Reachable: every step allows the connection."
	}
	if res.SourcePods == nil {
		res.SourcePods = []PodRef{}
	}
	if res.DestPods == nil {
		res.DestPods = []PodRef{}
	}
	return res
}

func describeRef(r Ref) string {
	if r.Kind == "IP" {
		return r.IP
	}
	return fmt.Sprintf("%s %s/%s", r.Kind, r.Namespace, r.Name)
}

func refObject(r Ref) ObjectRef {
	api := "v1"
	switch r.Kind {
	case "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet":
		api = "apps/v1"
	case "Job":
		api = "batch/v1"
	}
	return ObjectRef{Kind: r.Kind, APIVersion: api, Namespace: r.Namespace, Name: r.Name}
}

func runningFirst(pods []*corev1.Pod) []*corev1.Pod {
	sort.SliceStable(pods, func(i, j int) bool { return isReady(pods[i]) && !isReady(pods[j]) })
	return pods
}

func capPods(pods []*corev1.Pod) []*corev1.Pod {
	if len(pods) > maxPairPods {
		return pods[:maxPairPods]
	}
	return pods
}

func capBackends(b []backend) []backend {
	if len(b) > maxPairPods {
		return b[:maxPairPods]
	}
	return b
}

func anyReady(b []backend) bool {
	for _, x := range b {
		if isReady(x.pod) {
			return true
		}
	}
	return false
}

func podsOfBackends(b []backend) []*corev1.Pod {
	out := make([]*corev1.Pod, len(b))
	for i, x := range b {
		out[i] = x.pod
	}
	return out
}

func podCountText(pods []*corev1.Pod) string {
	ready := 0
	for _, p := range pods {
		if isReady(p) {
			ready++
		}
	}
	if len(pods) == 1 {
		state := "not ready"
		if ready == 1 {
			state = "ready"
		}
		ip := pods[0].Status.PodIP
		if ip != "" {
			return fmt.Sprintf("Pod %s (%s), %s", pods[0].Name, ip, state)
		}
		return fmt.Sprintf("Pod %s, %s", pods[0].Name, state)
	}
	return fmt.Sprintf("%d pods, %d ready", len(pods), ready)
}

func servicePortList(svc *corev1.Service) string {
	parts := make([]string, 0, len(svc.Spec.Ports))
	for _, p := range svc.Spec.Ports {
		pp := p.Protocol
		if pp == "" {
			pp = corev1.ProtocolTCP
		}
		parts = append(parts, fmt.Sprintf("%d/%s", p.Port, pp))
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}

func int32List(v []int32) string {
	parts := make([]string, len(v))
	for i, x := range v {
		parts[i] = fmt.Sprint(x)
	}
	return strings.Join(parts, ", ")
}

func sortedRefs(m map[string]ObjectRef) []ObjectRef {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]ObjectRef, len(keys))
	for i, k := range keys {
		out[i] = m[k]
	}
	return out
}
