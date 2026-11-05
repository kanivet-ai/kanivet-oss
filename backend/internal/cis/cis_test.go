package cis

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/kanivet/backend/internal/clusterinfo"
	"github.com/kanivet/backend/internal/health"
	"github.com/kanivet/backend/internal/rbac"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func byID(rep Report) map[string]ControlResult {
	m := map[string]ControlResult{}
	for _, c := range rep.Controls {
		m[c.ID] = c
	}
	return m
}

func TestParseConfigzKind(t *testing.T) {
	cfg, err := ParseConfigz(readFixture(t, "configz-kind.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"kubelet.anonymous-auth":          true,
		"kubelet.authz-mode":              true,
		"kubelet.client-ca":               true,
		"kubelet.read-only-port":          true, // omitted -> 0
		"kubelet.streaming-timeout":       true, // 4h
		"kubelet.protect-kernel-defaults": false,
		"kubelet.iptables-chains":         true,
		"kubelet.event-qps":               true,
		"kubelet.tls-cert":                true,
		"kubelet.rotate-certs":            true,
		"kubelet.rotate-server-cert":      true,
		"kubelet.tls-ciphers":             false,
		"kubelet.seccomp-default":         false,
	}
	for key, ok := range want {
		v, got := kubeletRules[key](cfg)
		if got != ok {
			t.Errorf("%s: got %v (%s), want %v", key, got, v, ok)
		}
	}
	if _, err := ParseConfigz([]byte(`{"other":{}}`)); err == nil {
		t.Error("expected an error without kubeletconfig")
	}
}

func TestKubeletRulesEdgeCases(t *testing.T) {
	f := false
	cfg := &KubeletConfig{ReadOnlyPort: 10255, StreamingConnectionIdleTimeout: "0s", MakeIPTablesUtilChains: &f,
		FeatureGates: map[string]bool{"RotateKubeletServerCertificate": false}, TLSCipherSuites: []string{"TLS_RSA_WITH_RC4_128_SHA"}}
	cfg.Authorization.Mode = "AlwaysAllow"
	for _, key := range []string{"kubelet.anonymous-auth", "kubelet.authz-mode", "kubelet.client-ca", "kubelet.read-only-port", "kubelet.streaming-timeout", "kubelet.iptables-chains", "kubelet.rotate-server-cert", "kubelet.tls-ciphers", "kubelet.tls-cert"} {
		if v, ok := kubeletRules[key](cfg); ok {
			t.Errorf("%s should fail, got pass (%s)", key, v)
		}
	}
}

func nodeIn(name, pool string) corev1.Node {
	return corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{"eks.amazonaws.com/nodegroup": pool}}}
}

func TestReadKubeletsGroupsAndSamples(t *testing.T) {
	good := readFixture(t, "configz-kind.json")
	var nodes []corev1.Node
	for i := 0; i < 5; i++ {
		nodes = append(nodes, nodeIn("a-"+itoa(i), "pool-a"))
	}
	nodes = append(nodes, nodeIn("b-0", "pool-b"), nodeIn("b-1", "pool-b"))
	nodes = append(nodes, corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "fargate-1", Labels: map[string]string{"eks.amazonaws.com/compute-type": "fargate"}}})
	reads := 0
	st := ReadKubelets(context.Background(), nodes, func(_ context.Context, n string) ([]byte, error) {
		reads++
		if n == "b-1" {
			return nil, errors.New("timeout")
		}
		return good, nil
	})
	if st.Sampled != 5 || reads != 5 || st.Total != 8 {
		t.Fatalf("sampled %d reads %d total %d", st.Sampled, reads, st.Total)
	}
	// pool-a (3 nodes, one config), pool-b ok, pool-b error, fargate.
	if len(st.Groups) != 4 {
		t.Fatalf("groups %+v", st.Groups)
	}
	if g := st.Groups[0]; g.Pool != "pool-a" || len(g.Nodes) != 3 || g.TotalNodes != 5 {
		t.Errorf("pool-a %+v", g)
	}
	o := kubeletCheck("kubelet.protect-kernel-defaults")(&Inputs{Kubelet: st})
	if o.Status != StatusFail || len(o.NodeGroups) != 4 || !strings.Contains(o.Message, "could not be read") {
		t.Errorf("outcome %+v", o)
	}
	o = kubeletCheck("kubelet.anonymous-auth")(&Inputs{Kubelet: st})
	if o.Status != StatusPass {
		t.Errorf("anonymous %+v", o)
	}
	denied := kubeletCheck("kubelet.anonymous-auth")(&Inputs{Kubelet: KubeletState{Reason: "Your identity may not get nodes/proxy"}})
	if denied.Status != StatusNotVisible || !strings.Contains(denied.Message, "nodes/proxy") {
		t.Errorf("denied %+v", denied)
	}
}

var kindAPIServer = []string{"kube-apiserver", "--advertise-address=172.18.0.2", "--allow-privileged=true", "--authorization-mode=Node,RBAC",
	"--client-ca-file=/etc/kubernetes/pki/ca.crt", "--enable-admission-plugins=NodeRestriction", "--enable-bootstrap-token-auth=true",
	"--etcd-cafile=/etc/kubernetes/pki/etcd/ca.crt", "--etcd-certfile=/etc/kubernetes/pki/apiserver-etcd-client.crt",
	"--etcd-keyfile=/etc/kubernetes/pki/apiserver-etcd-client.key", "--kubelet-client-certificate=/etc/kubernetes/pki/apiserver-kubelet-client.crt",
	"--kubelet-client-key=/etc/kubernetes/pki/apiserver-kubelet-client.key", "--runtime-config=", "--secure-port=6443",
	"--service-account-key-file=/etc/kubernetes/pki/sa.pub", "--tls-cert-file=/etc/kubernetes/pki/apiserver.crt",
	"--tls-private-key-file=/etc/kubernetes/pki/apiserver.key"}

func TestParseFlags(t *testing.T) {
	f := ParseFlags([]string{"etcd", "--name", "n1", "--client-cert-auth=true", "--debug", "--x=", "positional"})
	if f["name"] != "n1" || f["client-cert-auth"] != "true" || f["debug"] != "true" || f["x"] != "" {
		t.Fatalf("%v", f)
	}
	if _, ok := f["positional"]; ok {
		t.Fatal("positional args are not flags")
	}
}

func staticPod(component, node string, cmd []string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "kube-system", Name: component + "-" + node, Labels: map[string]string{"component": component, "tier": "control-plane"}},
		Spec:       corev1.PodSpec{NodeName: node, Containers: []corev1.Container{{Name: component, Command: cmd}}},
	}
}

func TestControlPlaneFlags(t *testing.T) {
	cp := ReadControlPlane([]*corev1.Pod{
		staticPod(APIServer, "cp", kindAPIServer),
		staticPod(Etcd, "cp", []string{"etcd", "--cert-file=/a", "--key-file=/b", "--client-cert-auth=true", "--peer-client-cert-auth=true", "--trusted-ca-file=/etc/kubernetes/pki/etcd/ca.crt"}),
		{ObjectMeta: metav1.ObjectMeta{Namespace: "kube-system", Name: "kube-apiserver-proxy-xyz"}},
	})
	if !cp.Visible || len(cp.Components[APIServer]) != 1 {
		t.Fatalf("%+v", cp)
	}
	in := &Inputs{ControlPlane: cp}
	cases := map[string]Status{
		"api.anonymous-auth":         StatusFail, // unset defaults to true
		"api.token-auth-file":        StatusPass,
		"api.authz-node":             StatusPass,
		"api.authz-rbac":             StatusPass,
		"api.authz-not-always-allow": StatusPass,
		"api.node-restriction":       StatusPass,
		"api.service-account":        StatusPass, // on by default
		"api.always-pull":            StatusFail,
		"api.always-admit":           StatusPass,
		"api.profiling":              StatusFail,
		"api.audit-log-path":         StatusFail,
		"api.sa-lookup":              StatusPass,
		"api.kubelet-client-cert":    StatusPass,
		"api.kubelet-ca":             StatusFail,
		"api.encryption-config":      StatusFail,
		"api.request-timeout":        StatusPass,
		"api.tls-cert":               StatusPass,
		"etcd.client-cert-auth":      StatusPass,
		"etcd.auto-tls":              StatusPass,
		"etcd.peer-cert":             StatusFail,
		"etcd.unique-ca":             StatusManual,
		"cm.profiling":               StatusNotVisible, // no controller-manager pod
	}
	for key, want := range cases {
		if got := checks[key].eval(in); got.Status != want {
			t.Errorf("%s: %s (%s), want %s", key, got.Status, got.Message, want)
		}
	}
	got := checks["api.profiling"].eval(in)
	if len(got.Values) != 1 || got.Values[0].Source != "kube-apiserver-cp" || got.Values[0].Value != "unset (default true)" {
		t.Errorf("values %+v", got.Values)
	}
	if o := checks["api.profiling"].eval(&Inputs{}); o.Status != StatusNotVisible {
		t.Errorf("hidden plane: %s", o.Status)
	}
}

func ns(name, enforce string) corev1.Namespace {
	n := corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{}}}
	if enforce != "" {
		n.Labels["pod-security.kubernetes.io/enforce"] = enforce
	}
	return n
}

func TestPSACoverage(t *testing.T) {
	nss := []corev1.Namespace{ns("a", "restricted"), ns("b", "baseline"), ns("c", "privileged"), ns("d", ""), ns("e", "bogus")}
	cov, unc := PSACoverage(nss, "baseline")
	if strings.Join(cov, ",") != "a,b" || strings.Join(unc, ",") != "c,d,e" {
		t.Fatalf("baseline %v %v", cov, unc)
	}
	cov, _ = PSACoverage(nss, "restricted")
	if strings.Join(cov, ",") != "a" {
		t.Fatalf("restricted %v", cov)
	}
}

func boolp(b bool) *bool { return &b }

func dep(namespace, name string, spec corev1.PodSpec) appsv1.Deployment {
	return appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name}, Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: spec}}}
}

func TestPodSecurityControls(t *testing.T) {
	snap := &health.Snapshot{Deployments: []appsv1.Deployment{
		dep("web", "priv", corev1.PodSpec{HostNetwork: true, Containers: []corev1.Container{{Name: "c", SecurityContext: &corev1.SecurityContext{Privileged: boolp(true)}}}}),
		dep("api", "ok", corev1.PodSpec{Containers: []corev1.Container{{Name: "c"}}}),
	}}
	in := &Inputs{Snap: snap, Namespaces: []corev1.Namespace{ns("web", "baseline"), ns("api", ""), ns("kube-system", "")}}
	o := checks["psa.privileged"].eval(in)
	if o.Status != StatusFail || len(o.Resources) != 2 || o.Resources[0].Name != "api" {
		t.Fatalf("privileged %+v", o)
	}
	in.Namespaces[1] = ns("api", "restricted")
	if o := checks["psa.host-network"].eval(in); o.Status != StatusPass || len(o.Resources) != 1 {
		t.Fatalf("all enforced %+v", o)
	}
	in.Namespaces[0] = ns("web", "")
	in.AdmissionWebhooks = []string{"kyverno-resource-validating-webhook-cfg"}
	if o := checks["psa.privileged"].eval(in); o.Status != StatusManual || !strings.Contains(o.ManualCheck, "kyverno") {
		t.Fatalf("engine %+v", o)
	}
	if o := checks["psa.mechanism"].eval(in); o.Status != StatusPass {
		t.Fatalf("mechanism %+v", o)
	}
	if o := checks["psa.mechanism"].eval(&Inputs{Namespaces: []corev1.Namespace{ns("x", "")}}); o.Status != StatusFail {
		t.Fatalf("no mechanism %+v", o)
	}
	if o := checks["psa.root"].eval(&Inputs{}); o.Status != StatusNotVisible {
		t.Fatalf("no namespaces %+v", o)
	}
}

func TestWorkloadPolicyChecks(t *testing.T) {
	snap := &health.Snapshot{
		Deployments: []appsv1.Deployment{
			dep("app", "env", corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Env: []corev1.EnvVar{{Name: "P", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{Key: "k"}}}}}}}),
			dep("app", "seccomp", corev1.PodSpec{AutomountServiceAccountToken: boolp(false), SecurityContext: &corev1.PodSecurityContext{SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}}, Containers: []corev1.Container{{Name: "c"}}}),
			dep("default", "stray", corev1.PodSpec{ServiceAccountName: "quiet", Containers: []corev1.Container{{Name: "c"}}}),
		},
		Services:        []corev1.Service{{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "kubernetes"}}},
		NetworkPolicies: []networkingv1.NetworkPolicy{{ObjectMeta: metav1.ObjectMeta{Namespace: "app", Name: "deny"}}},
		DaemonSets:      []appsv1.DaemonSet{{ObjectMeta: metav1.ObjectMeta{Namespace: "kube-system", Name: "cilium"}}},
	}
	in := &Inputs{Snap: snap, Namespaces: []corev1.Namespace{ns("app", ""), ns("default", "")},
		ServiceAccounts: []corev1.ServiceAccount{
			{ObjectMeta: metav1.ObjectMeta{Namespace: "app", Name: "default"}},
			{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "default"}, AutomountServiceAccountToken: boolp(false)},
			{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "quiet"}, AutomountServiceAccountToken: boolp(false)},
		}}
	expect := func(key string, st Status, n int) {
		t.Helper()
		o := checks[key].eval(in)
		if o.Status != st || len(o.Resources) != n {
			t.Errorf("%s: %s %d resources (%s), want %s %d", key, o.Status, len(o.Resources), o.Message, st, n)
		}
	}
	expect("secrets.env", StatusFail, 1)
	expect("general.seccomp", StatusFail, 2)
	expect("rbac.token-mount", StatusFail, 1) // only "env": seccomp opts out, stray's SA opts out
	expect("rbac.default-sa", StatusFail, 1)  // app/default still auto-mounts
	expect("general.default-ns", StatusFail, 1)
	expect("net.namespaces", StatusFail, 1)
	expect("net.cni", StatusPass, 0)
}

func TestRBACChecks(t *testing.T) {
	g := rbac.BuildGraph(&rbac.Snapshot{
		ClusterRoles: []rbacv1.ClusterRole{
			{ObjectMeta: metav1.ObjectMeta{Name: "cluster-admin"}, Rules: []rbacv1.PolicyRule{{Verbs: []string{"*"}, APIGroups: []string{"*"}, Resources: []string{"*"}}}},
			{ObjectMeta: metav1.ObjectMeta{Name: "reader"}, Rules: []rbacv1.PolicyRule{{Verbs: []string{"get", "list"}, APIGroups: []string{""}, Resources: []string{"secrets", "pods"}}}},
			{ObjectMeta: metav1.ObjectMeta{Name: "deployer"}, Rules: []rbacv1.PolicyRule{{Verbs: []string{"create"}, APIGroups: []string{""}, Resources: []string{"pods"}}}},
		},
		ClusterRoleBindings: []rbacv1.ClusterRoleBinding{
			{ObjectMeta: metav1.ObjectMeta{Name: "ops-admin"}, RoleRef: rbacv1.RoleRef{Kind: "ClusterRole", Name: "cluster-admin"}, Subjects: []rbacv1.Subject{{Kind: "User", Name: "alice"}}},
			{ObjectMeta: metav1.ObjectMeta{Name: "system:masters-admin"}, RoleRef: rbacv1.RoleRef{Kind: "ClusterRole", Name: "cluster-admin"}, Subjects: []rbacv1.Subject{{Kind: "Group", Name: "system:masters"}}},
			{ObjectMeta: metav1.ObjectMeta{Name: "readers"}, RoleRef: rbacv1.RoleRef{Kind: "ClusterRole", Name: "reader"}, Subjects: []rbacv1.Subject{{Kind: "Group", Name: "devs"}}},
			{ObjectMeta: metav1.ObjectMeta{Name: "ci"}, RoleRef: rbacv1.RoleRef{Kind: "ClusterRole", Name: "deployer"}, Subjects: []rbacv1.Subject{{Kind: "ServiceAccount", Namespace: "ci", Name: "runner"}}},
		},
	})
	in := &Inputs{RBAC: g}
	o := checks["rbac.cluster-admin"].eval(in)
	if o.Status != StatusFail || len(o.Resources) != 1 || o.Resources[0].Name != "ops-admin" {
		t.Fatalf("cluster-admin %+v", o)
	}
	if o := checks["rbac.secrets"].eval(in); o.Status != StatusFail || o.Resources[0].Name != "readers" {
		t.Fatalf("secrets %+v", o)
	}
	o = checks["rbac.create-pods"].eval(in)
	if o.Status != StatusFail || !strings.Contains(o.Message, "ServiceAccount:ci/runner") || !strings.Contains(o.Message, "User:alice") {
		t.Fatalf("create pods %+v", o)
	}
	if o := checks["rbac.csr-approve"].eval(in); o.Status != StatusFail { // alice via cluster-admin
		t.Fatalf("csr %+v", o)
	}
	if o := checks["rbac.wildcard"].eval(&Inputs{}); o.Status != StatusNotVisible {
		t.Fatalf("no rbac %+v", o)
	}
}

func TestAWSAuth(t *testing.T) {
	in := &Inputs{AWSAuthFound: true, AWSAuth: map[string]string{
		"mapRoles": "- rolearn: arn:aws:iam::1:role/node\n  username: system:node:{{EC2PrivateDNSName}}\n  groups:\n    - system:bootstrappers\n    - system:nodes\n- rolearn: arn:aws:iam::1:role/admin\n  username: admin\n  groups:\n    - system:masters\n",
	}}
	o := awsAuthCheck(in)
	if o.Status != StatusFail || len(o.Values) != 1 || !strings.Contains(o.Values[0].Value, "role/admin") {
		t.Fatalf("%+v", o)
	}
	if o := awsAuthCheck(&Inputs{}); o.Status != StatusPass {
		t.Fatalf("absent %+v", o)
	}
}

func loadUnstructured(t *testing.T, name string) *unstructured.Unstructured {
	t.Helper()
	u := &unstructured.Unstructured{}
	if err := json.Unmarshal(readFixture(t, name), &u.Object); err != nil {
		t.Fatal(err)
	}
	return u
}

func TestParseTrivy(t *testing.T) {
	rep, ok := ParseTrivyCompliance(loadUnstructured(t, "trivy-k8s-cis.json"))
	if !ok || rep.Profile != ProfileK8s || len(rep.Controls) != 4 {
		t.Fatalf("%+v", rep)
	}
	if rep.Controls["1.1.2"].Status != StatusFail || rep.Controls["1.1.1"].Status != StatusPass {
		t.Fatalf("%+v", rep.Controls)
	}
	if _, has := rep.Controls["5.1.1"]; has {
		t.Fatal("a control without totalFail is manual in Trivy and must be skipped")
	}
	if rep.ScannedAt.IsZero() {
		t.Fatal("timestamp")
	}
	eks, ok := ParseTrivyCompliance(loadUnstructured(t, "trivy-eks-detail.json"))
	if !ok || eks.Profile != ProfileEKS || eks.Controls["3.1.2"].Status != StatusFail || eks.Controls["3.1.1"].Status != StatusPass || len(eks.Controls) != 2 {
		t.Fatalf("detail %+v", eks)
	}
	nsa := &unstructured.Unstructured{Object: map[string]any{"metadata": map[string]any{"name": "nsa-1.0"}, "spec": map[string]any{"compliance": map[string]any{"id": "nsa"}}}}
	if _, ok := ParseTrivyCompliance(nsa); ok {
		t.Fatal("NSA reports are not CIS")
	}
}

func TestParseKubeBench(t *testing.T) {
	logs := "[INFO] 1 Control Plane Security Configuration\n[INFO] 1.1 Control Plane Node Configuration Files\n[PASS] 1.1.1 Ensure that the API server pod specification file permissions are set (Automated)\n[FAIL] 1.1.12 Ensure that the etcd data directory ownership is set (Automated)\n[WARN] 1.1.9 CNI files (Manual)\n[PASS] 2.1 etcd TLS\n== Summary ==\n"
	got := ParseKubeBench(logs)
	if len(got) != 4 || got["1.1.1"].Status != StatusPass || got["1.1.12"].Status != StatusFail || got["1.1.9"].Status != StatusManual || got["2.1"].Status != StatusPass {
		t.Fatalf("%+v", got)
	}
}

func TestParseKubescape(t *testing.T) {
	mk := func(status string) unstructured.Unstructured {
		return unstructured.Unstructured{Object: map[string]any{"metadata": map[string]any{"name": "x", "creationTimestamp": "2026-11-05T07:00:00Z"},
			"spec": map[string]any{"controls": map[string]any{
				"CIS-5.7.4": map[string]any{"controlID": "CIS-5.7.4", "status": map[string]any{"status": status}},
				"C-0057":    map[string]any{"controlID": "C-0057", "status": map[string]any{"status": "failed"}},
			}}}}
	}
	rep, ok := ParseKubescapeScans([]unstructured.Unstructured{mk("passed"), mk("failed")})
	if !ok || len(rep.Controls) != 1 || rep.Controls["5.7.4"].Status != StatusFail {
		t.Fatalf("%+v", rep)
	}
}

func TestScoreExcludesNonAutomated(t *testing.T) {
	cs := []ControlResult{{Status: StatusPass}, {Status: StatusPass}, {Status: StatusPass}, {Status: StatusFail},
		{Status: StatusManual}, {Status: StatusProvider}, {Status: StatusNotVisible}, {Status: StatusNotVisible}, {Status: StatusError}}
	s, counts := Score(cs)
	if s == nil || *s != 75 {
		t.Fatalf("score %v", s)
	}
	if counts[StatusManual] != 1 || counts[StatusNotVisible] != 2 || counts[StatusProvider] != 1 || counts[StatusError] != 1 {
		t.Fatalf("counts %v", counts)
	}
	if s, _ := Score([]ControlResult{{Status: StatusManual}}); s != nil {
		t.Fatal("no automated controls means no score, not 0 or 100")
	}
}

func kindInputs(t *testing.T) *Inputs {
	cfg, _ := ParseConfigz(readFixture(t, "configz-kind.json"))
	pods := []*corev1.Pod{staticPod(APIServer, "demo-control-plane", kindAPIServer)}
	return &Inputs{
		Provider: clusterinfo.Detect(clusterinfo.Signals{
			Nodes:          []corev1.Node{{ObjectMeta: metav1.ObjectMeta{Name: "demo-control-plane"}, Spec: corev1.NodeSpec{ProviderID: "kind://docker/demo/demo-control-plane"}}},
			GitVersion:     "v1.31.6",
			KubeSystemPods: []string{"kube-apiserver-demo-control-plane"},
		}),
		Snap:         &health.Snapshot{Pods: pods},
		Namespaces:   []corev1.Namespace{ns("default", ""), ns("app", "")},
		ControlPlane: ReadControlPlane(pods),
		Kubelet:      KubeletState{Allowed: true, Sampled: 1, Total: 1, Groups: []KubeletGroup{{Pool: "control-plane", Nodes: []string{"demo-control-plane"}, TotalNodes: 1, Config: cfg}}},
	}
}

func TestEvaluateKindAndMerge(t *testing.T) {
	in := kindInputs(t)
	trivy, _ := ParseTrivyCompliance(loadUnstructured(t, "trivy-k8s-cis.json"))
	in.Scanners = []ScannerReport{trivy}
	rep := Evaluate(in, "", time.Unix(0, 0))
	if rep.Profile.ID != ProfileK8s || rep.AutoProfile != ProfileK8s {
		t.Fatalf("profile %s", rep.Profile.ID)
	}
	m := byID(rep)
	if m["1.2.15"].Status != StatusFail || m["1.2.8"].Status != StatusPass {
		t.Errorf("control plane: %s %s", m["1.2.15"].Status, m["1.2.8"].Status)
	}
	if m["4.2.6"].Status != StatusFail || len(m["4.2.6"].NodeGroups) != 1 {
		t.Errorf("kubelet protect kernel defaults %+v", m["4.2.6"])
	}
	// Trivy fills the file-permission controls we cannot see.
	if c := m["1.1.2"]; c.Status != StatusFail || c.Source != SourceTrivy {
		t.Errorf("1.1.2 %+v", c)
	}
	if c := m["1.1.3"]; c.Status != StatusNotVisible {
		t.Errorf("1.1.3 %+v", c)
	}
	// Trivy says the read-only port fails; we read it as 0: shown, ours kept.
	if c := m["4.2.4"]; c.Status != StatusPass || !c.Disagreement || len(c.Scanners) != 1 {
		t.Errorf("4.2.4 %+v", c)
	}
	if len(rep.Scanners) != 1 || !rep.Scanners[0].Used {
		t.Errorf("scanners %+v", rep.Scanners)
	}
	for _, c := range rep.Controls {
		if c.Status == StatusError {
			t.Errorf("control %s errored: %s", c.ID, c.Message)
		}
		if c.Description == "" || c.Title == "" || c.SectionTitle == "" {
			t.Errorf("control %s lacks text: %+v", c.ID, c)
		}
	}
	if rep.Score == nil || len(rep.Sections) != 5 {
		t.Fatalf("score %v sections %d", rep.Score, len(rep.Sections))
	}
}

func TestEvaluateManagedEKS(t *testing.T) {
	in := &Inputs{
		Provider: clusterinfo.Detect(clusterinfo.Signals{GitVersion: "v1.36.1-eks-1", Nodes: []corev1.Node{nodeIn("n1", "ng")}}),
		Snap:     &health.Snapshot{},
		Kubelet:  KubeletState{Reason: "Your identity may not get nodes/proxy", Total: 3},
	}
	trivy, _ := ParseTrivyCompliance(loadUnstructured(t, "trivy-k8s-cis.json"))
	in.Scanners = []ScannerReport{trivy}
	rep := Evaluate(in, "", time.Unix(0, 0))
	if rep.Profile.ID != ProfileEKS {
		t.Fatalf("profile %s", rep.Profile.ID)
	}
	m := byID(rep)
	for _, id := range []string{"1.1.1", "1.1.2", "1.1.3"} {
		if m[id].Status != StatusProvider {
			t.Errorf("%s %s", id, m[id].Status)
		}
	}
	if m["3.2.1"].Status != StatusNotVisible {
		t.Errorf("kubelet %s", m["3.2.1"].Status)
	}
	if m["2.1.1"].Status != StatusManual || m["2.1.1"].ManualCheck == "" {
		t.Errorf("logging %+v", m["2.1.1"])
	}
	if rep.Scanners[0].Used {
		t.Error("a k8s-cis report must not be merged into the EKS profile")
	}
	found := false
	for _, n := range rep.Notes {
		if n.Kind == "rbac" && strings.Contains(n.Text, "nodes/proxy") {
			found = true
		}
	}
	if !found {
		t.Errorf("notes %+v", rep.Notes)
	}
	// Switching profile keeps managed control-plane controls as the provider's.
	gen := byID(Evaluate(in, ProfileK8s, time.Unix(0, 0)))
	if gen["1.2.1"].Status != StatusProvider || gen["1.1.1"].Status != StatusProvider {
		t.Errorf("generic on EKS: %s %s", gen["1.2.1"].Status, gen["1.1.1"].Status)
	}
}

func TestEveryProfileResolves(t *testing.T) {
	for _, id := range ProfileOrder {
		p := profiles[id]()
		seen := map[string]bool{}
		for _, c := range p.controls {
			if _, ok := checks[c.key]; !ok {
				t.Errorf("%s %s: unknown check %s", id, c.id, c.key)
			}
			if seen[c.id] {
				t.Errorf("%s: duplicate id %s", id, c.id)
			}
			seen[c.id] = true
			sec, sub := sectionOf(c.id)
			if p.sections[sec] == "" || p.sections[sub] == "" {
				t.Errorf("%s %s: no section title for %s/%s", id, c.id, sec, sub)
			}
		}
	}
	if ProfileFor(clusterinfo.Kind) != ProfileK8s || ProfileFor(clusterinfo.GKE) != ProfileGKE {
		t.Fatal("profile choice")
	}
}

func TestOverrideStore(t *testing.T) {
	p := filepath.Join(t.TempDir(), "o.json")
	s := NewOverrideStore(p)
	if err := s.Set("c1", Override{Provider: "kubeadm", Profile: ProfileK8s}); err != nil {
		t.Fatal(err)
	}
	if got := NewOverrideStore(p).Get("c1"); got.Provider != "kubeadm" {
		t.Fatalf("reload %+v", got)
	}
	_ = s.Set("c1", Override{})
	if got := NewOverrideStore(p).Get("c1"); got.Provider != "" {
		t.Fatalf("clear %+v", got)
	}
	svc := NewService(nil, nil, nil, s)
	if err := svc.SetOverride("c1", Override{Provider: "nope"}); err == nil {
		t.Fatal("unknown provider accepted")
	}
}
