package network

import (
	"fmt"
	"net"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// peer is the other end of a connection as a policy sees it: a pod, or an
// address outside the cluster.
type peer struct {
	pod *corev1.Pod
	ip  string
}

func (p peer) addr() string {
	if p.pod != nil {
		return p.pod.Status.PodIP
	}
	return p.ip
}

func (p peer) String() string {
	if p.pod != nil {
		return p.pod.Namespace + "/" + p.pod.Name
	}
	return p.ip
}

// decision is one direction's verdict for one pod pair.
type decision struct {
	isolated  bool
	allowed   bool
	selecting []*netv1.NetworkPolicy
	allowedBy []RuleRef
	// reasons explain why each rule of each selecting policy did or did not
	// match.
	reasons []string
	// defaultDeny lists the selecting policies that have no rules at all in
	// this direction.
	defaultDeny []*netv1.NetworkPolicy
}

// policyTypes reports which directions a policy isolates, with the API's
// default: Ingress always, Egress when it has egress rules.
func policyTypes(np *netv1.NetworkPolicy) (ingress, egress bool) {
	if len(np.Spec.PolicyTypes) == 0 {
		return true, len(np.Spec.Egress) > 0
	}
	for _, t := range np.Spec.PolicyTypes {
		switch t {
		case netv1.PolicyTypeIngress:
			ingress = true
		case netv1.PolicyTypeEgress:
			egress = true
		}
	}
	return
}

func policyTypeNames(np *netv1.NetworkPolicy) []string {
	in, eg := policyTypes(np)
	var out []string
	if in {
		out = append(out, "Ingress")
	}
	if eg {
		out = append(out, "Egress")
	}
	return out
}

func selects(np *netv1.NetworkPolicy, p *corev1.Pod) bool {
	if np.Namespace != p.Namespace {
		return false
	}
	sel, err := metav1.LabelSelectorAsSelector(&np.Spec.PodSelector)
	if err != nil {
		return false
	}
	return sel.Matches(labels.Set(p.Labels))
}

// peerMatches reports whether a rule's peer selects t, from the point of view
// of a policy in np's namespace.
func (s *Snapshot) peerMatches(np *netv1.NetworkPolicy, pr netv1.NetworkPolicyPeer, t peer) bool {
	if pr.IPBlock != nil {
		return ipBlockMatches(pr.IPBlock, t.addr())
	}
	if t.pod == nil {
		// Pod and namespace selectors only ever pick pods.
		return false
	}
	if pr.NamespaceSelector == nil {
		if t.pod.Namespace != np.Namespace {
			return false
		}
	} else {
		sel, err := metav1.LabelSelectorAsSelector(pr.NamespaceSelector)
		if err != nil || !sel.Matches(s.namespaceLabels(t.pod.Namespace)) {
			return false
		}
	}
	if pr.PodSelector == nil {
		return true
	}
	sel, err := metav1.LabelSelectorAsSelector(pr.PodSelector)
	return err == nil && sel.Matches(labels.Set(t.pod.Labels))
}

func ipBlockMatches(b *netv1.IPBlock, addr string) bool {
	ip := net.ParseIP(addr)
	if ip == nil {
		return false
	}
	_, cidr, err := net.ParseCIDR(b.CIDR)
	if err != nil || !cidr.Contains(ip) {
		return false
	}
	for _, ex := range b.Except {
		if _, e, err := net.ParseCIDR(ex); err == nil && e.Contains(ip) {
			return false
		}
	}
	return true
}

// namedPorts maps the destination pod's container port names to numbers for
// a protocol: policies' named ports always refer to the destination.
func namedPorts(p *corev1.Pod, proto corev1.Protocol) map[string]int32 {
	if p == nil {
		return nil
	}
	m := map[string]int32{}
	for _, c := range p.Spec.Containers {
		for _, cp := range c.Ports {
			pp := cp.Protocol
			if pp == "" {
				pp = corev1.ProtocolTCP
			}
			if cp.Name != "" && pp == proto {
				m[cp.Name] = cp.ContainerPort
			}
		}
	}
	return m
}

// portMatches reports whether a rule's ports allow port/proto; port 0 means
// "any port" and matches any rule that names the protocol.
func portMatches(ports []netv1.NetworkPolicyPort, port int32, proto corev1.Protocol, named map[string]int32) bool {
	if len(ports) == 0 {
		return true
	}
	for _, p := range ports {
		pp := corev1.ProtocolTCP
		if p.Protocol != nil {
			pp = *p.Protocol
		}
		if pp != proto {
			continue
		}
		if p.Port == nil || port == 0 {
			return true
		}
		if p.Port.Type == intstr.Int {
			lo, hi := p.Port.IntVal, p.Port.IntVal
			if p.EndPort != nil {
				hi = *p.EndPort
			}
			if port >= lo && port <= hi {
				return true
			}
			continue
		}
		if v, ok := named[p.Port.StrVal]; ok && v == port {
			return true
		}
	}
	return false
}

// evalIngress decides whether src may connect to dst on port.
func (s *Snapshot) evalIngress(dst *corev1.Pod, src peer, port int32, proto corev1.Protocol) decision {
	return s.eval("ingress", dst, src, port, proto, namedPorts(dst, proto))
}

// evalEgress decides whether src may connect out to dst on port. dstPod is
// the destination pod when there is one, for named ports.
func (s *Snapshot) evalEgress(src *corev1.Pod, dst peer, port int32, proto corev1.Protocol) decision {
	return s.eval("egress", src, dst, port, proto, namedPorts(dst.pod, proto))
}

func (s *Snapshot) eval(dir string, subject *corev1.Pod, other peer, port int32, proto corev1.Protocol, named map[string]int32) decision {
	var d decision
	for _, np := range s.policiesIn(subject.Namespace) {
		if !selects(np, subject) {
			continue
		}
		in, eg := policyTypes(np)
		var rules [][]netv1.NetworkPolicyPeer
		var rulePorts [][]netv1.NetworkPolicyPort
		if dir == "ingress" {
			if !in {
				continue
			}
			for _, r := range np.Spec.Ingress {
				rules = append(rules, r.From)
				rulePorts = append(rulePorts, r.Ports)
			}
		} else {
			if !eg {
				continue
			}
			for _, r := range np.Spec.Egress {
				rules = append(rules, r.To)
				rulePorts = append(rulePorts, r.Ports)
			}
		}
		d.isolated = true
		d.selecting = append(d.selecting, np)
		if len(rules) == 0 {
			d.defaultDeny = append(d.defaultDeny, np)
			d.reasons = append(d.reasons, fmt.Sprintf("%s selects %s and allows no %s", np.Name, subject.Name, dir))
			continue
		}
		for i := range rules {
			peersOK := len(rules[i]) == 0
			for _, pr := range rules[i] {
				if s.peerMatches(np, pr, other) {
					peersOK = true
					break
				}
			}
			portsOK := portMatches(rulePorts[i], port, proto, named)
			ref := RuleRef{Policy: policyRef(np), Direction: dir, Index: i + 1}
			switch {
			case peersOK && portsOK:
				d.allowed = true
				d.allowedBy = append(d.allowedBy, ref)
				d.reasons = append(d.reasons, fmt.Sprintf("%s %s rule %d allows %s on %s", np.Name, dir, i+1, other, portText(port, proto)))
			case !peersOK && !portsOK:
				d.reasons = append(d.reasons, fmt.Sprintf("%s %s rule %d: %s does not match %s, and the port is not in %s", np.Name, dir, i+1, other, describePeers(rules[i], np.Namespace, dir), describePorts(rulePorts[i])))
			case !peersOK:
				d.reasons = append(d.reasons, fmt.Sprintf("%s %s rule %d: %s does not match %s", np.Name, dir, i+1, other, describePeers(rules[i], np.Namespace, dir)))
			default:
				d.reasons = append(d.reasons, fmt.Sprintf("%s %s rule %d: peer matches but %s is not in %s", np.Name, dir, i+1, portText(port, proto), describePorts(rulePorts[i])))
			}
		}
	}
	if !d.isolated {
		d.allowed = true
	}
	return d
}

func portText(port int32, proto corev1.Protocol) string {
	if port == 0 {
		return "any " + string(proto) + " port"
	}
	return fmt.Sprintf("%s/%d", proto, port)
}

// describePeers renders a rule's peers in words.
func describePeers(peers []netv1.NetworkPolicyPeer, ns, dir string) string {
	if len(peers) == 0 {
		if dir == "ingress" {
			return "any source"
		}
		return "any destination"
	}
	parts := make([]string, 0, len(peers))
	for _, p := range peers {
		parts = append(parts, describePeer(p, ns))
	}
	return strings.Join(parts, " or ")
}

func describePeer(p netv1.NetworkPolicyPeer, ns string) string {
	if p.IPBlock != nil {
		s := p.IPBlock.CIDR
		if len(p.IPBlock.Except) > 0 {
			s += " except " + strings.Join(p.IPBlock.Except, ", ")
		}
		return s
	}
	pods := "all pods"
	if p.PodSelector != nil && !isEmptySelector(p.PodSelector) {
		pods = "pods " + metav1.FormatLabelSelector(p.PodSelector)
	}
	switch {
	case p.NamespaceSelector == nil:
		return pods + " in " + ns
	case isEmptySelector(p.NamespaceSelector):
		return pods + " in all namespaces"
	default:
		return pods + " in namespaces " + metav1.FormatLabelSelector(p.NamespaceSelector)
	}
}

func isEmptySelector(s *metav1.LabelSelector) bool {
	return s == nil || (len(s.MatchLabels) == 0 && len(s.MatchExpressions) == 0)
}

func describePorts(ports []netv1.NetworkPolicyPort) string {
	return strings.Join(portStrings(ports), ", ")
}

func portStrings(ports []netv1.NetworkPolicyPort) []string {
	if len(ports) == 0 {
		return []string{"all ports"}
	}
	out := make([]string, 0, len(ports))
	for _, p := range ports {
		proto := "TCP"
		if p.Protocol != nil {
			proto = string(*p.Protocol)
		}
		switch {
		case p.Port == nil:
			out = append(out, "all "+proto)
		case p.EndPort != nil:
			out = append(out, fmt.Sprintf("%s/%s-%d", proto, p.Port.String(), *p.EndPort))
		default:
			out = append(out, proto+"/"+p.Port.String())
		}
	}
	return out
}

func selectorText(s metav1.LabelSelector) string {
	if isEmptySelector(&s) {
		return "all pods"
	}
	return metav1.FormatLabelSelector(&s)
}

func policyNames(nps []*netv1.NetworkPolicy) string {
	names := make([]string, len(nps))
	for i, np := range nps {
		names[i] = np.Name
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func policyRefs(nps []*netv1.NetworkPolicy) []ObjectRef {
	out := make([]ObjectRef, len(nps))
	for i, np := range nps {
		out[i] = policyRef(np)
	}
	return out
}
