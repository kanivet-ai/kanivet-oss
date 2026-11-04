package network

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	netv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
)

func ns(name string, lbl map[string]string) *corev1.Namespace {
	l := map[string]string{"kubernetes.io/metadata.name": name}
	for k, v := range lbl {
		l[k] = v
	}
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: l}}
}

func pod(namespace, name, ip string, lbl map[string]string, ready bool, ports ...corev1.ContainerPort) *corev1.Pod {
	cond := corev1.ConditionFalse
	if ready {
		cond = corev1.ConditionTrue
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name, Labels: lbl},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "main", Ports: ports}}},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning, PodIP: ip, Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: cond}}},
	}
}

func deployPod(namespace, deploy, suffix, ip string, lbl map[string]string, ports ...corev1.ContainerPort) *corev1.Pod {
	l := map[string]string{"pod-template-hash": "abc"}
	for k, v := range lbl {
		l[k] = v
	}
	p := pod(namespace, deploy+"-abc-"+suffix, ip, l, true, ports...)
	p.OwnerReferences = []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: deploy + "-abc", Controller: ptr.To(true)}}
	return p
}

func svc(namespace, name string, sel map[string]string, ports ...corev1.ServicePort) *corev1.Service {
	return &corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name}, Spec: corev1.ServiceSpec{Selector: sel, Ports: ports, ClusterIP: "10.96.0.10"}}
}

func slice(s *corev1.Service, portName string, port int32, pods ...*corev1.Pod) *discoveryv1.EndpointSlice {
	sl := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{Namespace: s.Namespace, Name: s.Name + "-x", Labels: map[string]string{discoveryv1.LabelServiceName: s.Name}},
		Ports:      []discoveryv1.EndpointPort{{Name: ptr.To(portName), Port: ptr.To(port)}},
	}
	for _, p := range pods {
		sl.Endpoints = append(sl.Endpoints, discoveryv1.Endpoint{
			Addresses:  []string{p.Status.PodIP},
			Conditions: discoveryv1.EndpointConditions{Ready: ptr.To(isReady(p))},
			TargetRef:  &corev1.ObjectReference{Kind: "Pod", Namespace: p.Namespace, Name: p.Name},
		})
	}
	return sl
}

func np(namespace, name string, sel map[string]string, types []netv1.PolicyType, ingress []netv1.NetworkPolicyIngressRule, egress []netv1.NetworkPolicyEgressRule) *netv1.NetworkPolicy {
	return &netv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name}, Spec: netv1.NetworkPolicySpec{PodSelector: metav1.LabelSelector{MatchLabels: sel}, PolicyTypes: types, Ingress: ingress, Egress: egress}}
}

func tcp(port intstr.IntOrString) netv1.NetworkPolicyPort {
	return netv1.NetworkPolicyPort{Protocol: ptr.To(corev1.ProtocolTCP), Port: &port}
}

type fixture struct {
	nss      []*corev1.Namespace
	pods     []*corev1.Pod
	svcs     []*corev1.Service
	slices   []*discoveryv1.EndpointSlice
	policies []*netv1.NetworkPolicy
}

func base() *fixture {
	web := deployPod("web", "frontend", "1", "10.0.1.5", map[string]string{"app": "web"})
	api1 := deployPod("prod", "api", "1", "10.0.2.5", map[string]string{"app": "api"}, corev1.ContainerPort{Name: "http", ContainerPort: 8080})
	api2 := deployPod("prod", "api", "2", "10.0.2.6", map[string]string{"app": "api"}, corev1.ContainerPort{Name: "http", ContainerPort: 8080})
	apiSvc := svc("prod", "api", map[string]string{"app": "api"}, corev1.ServicePort{Name: "web", Port: 80, TargetPort: intstr.FromString("http")})
	return &fixture{
		nss:    []*corev1.Namespace{ns("web", map[string]string{"team": "web"}), ns("prod", nil)},
		pods:   []*corev1.Pod{web, api1, api2, pod("kube-system", "cilium-xyz", "10.0.0.2", nil, true)},
		svcs:   []*corev1.Service{apiSvc},
		slices: []*discoveryv1.EndpointSlice{slice(apiSvc, "web", 8080, api1, api2)},
	}
}

func (f *fixture) snap() *Snapshot {
	return NewSnapshot(f.nss, f.pods, f.svcs, f.slices, f.policies)
}

var toAPI = PathRequest{Source: Ref{Kind: "deployment", Namespace: "web", Name: "frontend"}, Destination: Ref{Kind: "svc", Namespace: "prod", Name: "api"}, Port: 80}

func hop(t *testing.T, r *PathResult, id string) Hop {
	t.Helper()
	for _, h := range r.Hops {
		if h.ID == id {
			return h
		}
	}
	t.Fatalf("no hop %q in %+v", id, r.Hops)
	return Hop{}
}

func TestOpenPathThroughService(t *testing.T) {
	r := base().snap().CheckPath(toAPI)
	if r.Verdict != VerdictAllowed {
		t.Fatalf("verdict %s: %s", r.Verdict, r.Summary)
	}
	ids := []string{}
	for _, h := range r.Hops {
		ids = append(ids, h.ID)
	}
	if got := strings.Join(ids, ","); got != "source,egress,service,endpoints,ingress,port,destination" {
		t.Fatalf("hops %s", got)
	}
	if d := hop(t, r, "service").Detail; !strings.Contains(d, "port 80 → targetPort http") {
		t.Fatalf("service detail %q", d)
	}
	if d := hop(t, r, "port").Detail; !strings.Contains(d, "8080") {
		t.Fatalf("port detail %q", d)
	}
	if r.Pairs != 2 || r.PairsAllowed != 2 || r.Enforcer != "Cilium" {
		t.Fatalf("pairs %d/%d enforcer %q", r.PairsAllowed, r.Pairs, r.Enforcer)
	}
}

func TestDefaultDenyIngressBlocks(t *testing.T) {
	f := base()
	f.policies = []*netv1.NetworkPolicy{np("prod", "default-deny", nil, []netv1.PolicyType{netv1.PolicyTypeIngress}, nil, nil)}
	r := f.snap().CheckPath(toAPI)
	if r.Verdict != VerdictBlocked {
		t.Fatalf("verdict %s", r.Verdict)
	}
	h := hop(t, r, "ingress")
	if h.Status != StatusFail || !strings.Contains(h.Detail, "default-deny ingress in prod") {
		t.Fatalf("ingress hop %+v", h)
	}
	if hop(t, r, "egress").Status != StatusPass {
		t.Fatal("egress should be open")
	}
}

func TestAllowRuleByNamespaceAndPodSelectorWithNamedPort(t *testing.T) {
	f := base()
	f.policies = []*netv1.NetworkPolicy{
		np("prod", "default-deny", nil, []netv1.PolicyType{netv1.PolicyTypeIngress}, nil, nil),
		np("prod", "allow-web", map[string]string{"app": "api"}, nil, []netv1.NetworkPolicyIngressRule{
			{From: []netv1.NetworkPolicyPeer{{IPBlock: &netv1.IPBlock{CIDR: "192.168.0.0/16"}}}},
			{
				From:  []netv1.NetworkPolicyPeer{{NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"team": "web"}}, PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}}}},
				Ports: []netv1.NetworkPolicyPort{tcp(intstr.FromString("http"))},
			},
		}, nil),
	}
	r := f.snap().CheckPath(toAPI)
	if r.Verdict != VerdictAllowed {
		t.Fatalf("verdict %s: %s", r.Verdict, r.Summary)
	}
	h := hop(t, r, "ingress")
	if !strings.Contains(h.Detail, "allow-web rule 2") || len(h.AllowedBy) != 1 || h.AllowedBy[0].Index != 2 {
		t.Fatalf("ingress hop %+v", h)
	}
}

func TestWrongPortExplained(t *testing.T) {
	f := base()
	f.policies = []*netv1.NetworkPolicy{
		np("prod", "allow-9090", map[string]string{"app": "api"}, nil, []netv1.NetworkPolicyIngressRule{
			{Ports: []netv1.NetworkPolicyPort{tcp(intstr.FromInt32(9000))}},
		}, nil),
	}
	f.policies[0].Spec.Ingress[0].Ports[0].EndPort = ptr.To[int32](9100)
	r := f.snap().CheckPath(toAPI)
	h := hop(t, r, "ingress")
	if h.Status != StatusFail || len(h.Reasons) == 0 || !strings.Contains(h.Reasons[0], "TCP/8080 is not in TCP/9000-9100") {
		t.Fatalf("ingress hop %+v", h)
	}
}

func TestEgressDenyAndDNS(t *testing.T) {
	f := base()
	f.policies = []*netv1.NetworkPolicy{np("web", "lockdown", nil, []netv1.PolicyType{netv1.PolicyTypeEgress}, nil, nil)}
	r := f.snap().CheckPath(toAPI)
	if h := hop(t, r, "egress"); h.Status != StatusFail || !strings.Contains(h.Detail, "default-deny egress in web") {
		t.Fatalf("egress hop %+v", h)
	}
}

func TestServicePortMissingAndNoEndpoints(t *testing.T) {
	f := base()
	req := toAPI
	req.Port = 81
	r := f.snap().CheckPath(req)
	if h := hop(t, r, "service"); h.Status != StatusFail || !strings.Contains(h.Detail, "no TCP port 81") {
		t.Fatalf("service hop %+v", h)
	}
	for _, p := range f.pods[1:3] {
		p.Status.Conditions[0].Status = corev1.ConditionFalse
	}
	f.slices = []*discoveryv1.EndpointSlice{slice(f.svcs[0], "web", 8080, f.pods[1], f.pods[2])}
	r = f.snap().CheckPath(toAPI)
	if h := hop(t, r, "endpoints"); h.Status != StatusFail || !strings.Contains(h.Detail, "No ready endpoints") {
		t.Fatalf("endpoints hop %+v", h)
	}
}

func TestTargetPortNotExposed(t *testing.T) {
	f := base()
	f.svcs[0].Spec.Ports[0].TargetPort = intstr.FromInt32(9999)
	f.slices = nil // fall back to the selector
	r := f.snap().CheckPath(toAPI)
	if r.Verdict != VerdictPartial {
		t.Fatalf("verdict %s", r.Verdict)
	}
	if h := hop(t, r, "port"); h.Status != StatusWarn || !strings.Contains(h.Detail, "9999 is not exposed by any container") {
		t.Fatalf("port hop %+v", h)
	}
}

func TestEgressToExternalIPBlock(t *testing.T) {
	f := base()
	f.policies = []*netv1.NetworkPolicy{np("web", "egress-internet", nil, []netv1.PolicyType{netv1.PolicyTypeEgress}, nil, []netv1.NetworkPolicyEgressRule{
		{To: []netv1.NetworkPolicyPeer{{IPBlock: &netv1.IPBlock{CIDR: "0.0.0.0/0", Except: []string{"10.0.0.0/8"}}}}, Ports: []netv1.NetworkPolicyPort{tcp(intstr.FromInt32(443))}},
	})}
	req := PathRequest{Source: toAPI.Source, Destination: Ref{Kind: "IP", IP: "8.8.8.8"}, Port: 443}
	if r := f.snap().CheckPath(req); r.Verdict != VerdictAllowed {
		t.Fatalf("external: %s %s", r.Verdict, r.Summary)
	}
	// A pod IP inside the except block is blocked, and is resolved to the pod.
	req.Destination.IP = "10.0.2.5"
	r := f.snap().CheckPath(req)
	if r.Verdict != VerdictBlocked || len(r.DestPods) != 1 || r.DestPods[0].Name != "api-abc-1" {
		t.Fatalf("pod ip: %s %+v", r.Verdict, r.DestPods)
	}
}

func TestBadSource(t *testing.T) {
	r := base().snap().CheckPath(PathRequest{Source: Ref{Kind: "Service", Namespace: "prod", Name: "api"}, Destination: toAPI.Destination, Port: 80})
	if r.Verdict != VerdictBlocked || r.Hops[0].Status != StatusFail {
		t.Fatalf("%+v", r)
	}
}

func TestCoverageAndPolicyDetail(t *testing.T) {
	f := base()
	f.policies = []*netv1.NetworkPolicy{
		np("prod", "default-deny", nil, []netv1.PolicyType{netv1.PolicyTypeIngress, netv1.PolicyTypeEgress}, nil, nil),
		np("prod", "api-out", map[string]string{"app": "api"}, []netv1.PolicyType{netv1.PolicyTypeEgress}, nil, []netv1.NetworkPolicyEgressRule{{Ports: []netv1.NetworkPolicyPort{tcp(intstr.FromInt32(5432))}}}),
	}
	s := f.snap()
	c := s.Coverage("prod")
	if c.Pods != 2 || c.IngressIsolated != 2 || len(c.Workloads) != 1 || c.Workloads[0].Status != "isolated" || c.Workloads[0].Workload.Kind != "Deployment" {
		t.Fatalf("coverage %+v", c)
	}
	if !c.Policies[1].DefaultDenyIngress || !c.Policies[1].DefaultDenyEgress {
		t.Fatalf("default-deny not flagged: %+v", c.Policies)
	}
	all := s.Coverage("")
	open := 0
	for _, w := range all.Workloads {
		if w.Status == "open" {
			open++
		}
	}
	if open != 2 { // frontend and the cilium pod
		t.Fatalf("open workloads %d", open)
	}
	d, err := s.PolicyDetail("prod", "api-out")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.SelectedPods) != 2 || len(d.SelectedWorkloads) != 1 || len(d.Egress) != 1 || d.Egress[0].Matches != -1 {
		t.Fatalf("detail %+v", d)
	}
	if len(d.Warnings) == 0 || !strings.Contains(d.Warnings[0], "DNS") {
		t.Fatalf("expected DNS warning: %+v", d.Warnings)
	}
}

func TestResourceNetworkPeers(t *testing.T) {
	f := base()
	f.policies = []*netv1.NetworkPolicy{np("prod", "allow-web", map[string]string{"app": "api"}, nil, []netv1.NetworkPolicyIngressRule{
		{From: []netv1.NetworkPolicyPeer{{NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"team": "web"}}}}, Ports: []netv1.NetworkPolicyPort{tcp(intstr.FromInt32(8080))}},
	}, nil)}
	v, err := f.snap().ResourceNetwork(Ref{Kind: "Service", Namespace: "prod", Name: "api"})
	if err != nil {
		t.Fatal(err)
	}
	if !v.Ingress.Isolated || len(v.Ingress.Peers) != 1 || v.Ingress.Peers[0].Workload.Name != "frontend" || v.Ingress.Peers[0].Ports[0] != "TCP/8080" {
		t.Fatalf("ingress %+v", v.Ingress)
	}
	if v.Egress.Isolated || len(v.Services) != 1 || !v.Services[0].Ports[0].Exposed || v.Services[0].Ports[0].ResolvedPort != 8080 {
		t.Fatalf("egress/services %+v %+v", v.Egress, v.Services)
	}
}

func TestNoEnforcerWarns(t *testing.T) {
	f := base()
	f.pods = f.pods[:3]
	f.policies = []*netv1.NetworkPolicy{np("prod", "x", nil, nil, nil, nil)}
	r := f.snap().CheckPath(toAPI)
	if len(r.Warnings) == 0 || !strings.Contains(r.Warnings[0], "enforcer") {
		t.Fatalf("warnings %+v", r.Warnings)
	}
}

func TestProbePodIsContained(t *testing.T) {
	f := base()
	s := f.snap()
	src, host, port, err := s.probeTarget(ProbeRequest{Source: toAPI.Source, Destination: toAPI.Destination})
	if err != nil || host != "api.prod.svc" || port != 80 {
		t.Fatalf("%v %s %d", err, host, port)
	}
	p := probePod(src, "id", DefaultProbeImage, host, port, true)
	if p.Labels["app"] != "web" || p.Labels["pod-template-hash"] != "" || p.Labels[ProbeLabel] != "id" {
		t.Fatalf("labels %v", p.Labels)
	}
	if len(p.OwnerReferences) != 1 || !*p.OwnerReferences[0].Controller || p.Spec.Containers[0].ReadinessProbe == nil {
		t.Fatal("mimicking probe must be controller-owned and never ready")
	}
	if *p.Spec.ActiveDeadlineSeconds > 120 || !*p.Spec.SecurityContext.RunAsNonRoot {
		t.Fatal("probe must be short-lived and unprivileged")
	}
	plain := probePod(src, "id", DefaultProbeImage, host, port, false)
	if plain.Labels["app"] != "" || len(plain.OwnerReferences) != 0 {
		t.Fatalf("plain probe %+v", plain.ObjectMeta)
	}
}
