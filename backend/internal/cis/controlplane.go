package cis

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
)

// Control-plane components, as kubeadm labels their static pods.
const (
	APIServer         = "kube-apiserver"
	ControllerManager = "kube-controller-manager"
	Scheduler         = "kube-scheduler"
	Etcd              = "etcd"
)

// Component is one static control-plane pod's flags.
type Component struct {
	Pod   string
	Flags map[string]string
}

// ControlPlane holds the parsed static pods, per component.
type ControlPlane struct {
	// Visible is true when at least one kube-apiserver static pod was read.
	Visible    bool
	Components map[string][]Component
}

// ParseFlags turns a container command line into flag values. "--a=b" and
// "--a b" both work; a bare "--a" is "true". Repeated flags keep the last.
func ParseFlags(args []string) map[string]string {
	out := map[string]string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "--") {
			continue
		}
		a = strings.TrimPrefix(a, "--")
		if k, v, ok := strings.Cut(a, "="); ok {
			out[k] = v
			continue
		}
		if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			out[a] = args[i+1]
			i++
			continue
		}
		out[a] = "true"
	}
	return out
}

// componentOf names the control-plane component a kube-system pod runs.
func componentOf(p *corev1.Pod) string {
	if c := p.Labels["component"]; c == APIServer || c == ControllerManager || c == Scheduler || c == Etcd {
		return c
	}
	for _, c := range []string{APIServer, ControllerManager, Scheduler, Etcd} {
		if strings.HasPrefix(p.Name, c+"-") {
			// Static pods are mirror pods named <component>-<node>.
			if _, mirror := p.Annotations["kubernetes.io/config.mirror"]; mirror || p.Spec.NodeName != "" && p.Name == c+"-"+p.Spec.NodeName {
				return c
			}
		}
	}
	return ""
}

// ReadControlPlane parses the control-plane static pods in kube-system.
func ReadControlPlane(pods []*corev1.Pod) ControlPlane {
	cp := ControlPlane{Components: map[string][]Component{}}
	for _, p := range pods {
		if p.Namespace != "kube-system" {
			continue
		}
		c := componentOf(p)
		if c == "" {
			continue
		}
		var args []string
		for _, ctr := range p.Spec.Containers {
			if ctr.Name == c || len(p.Spec.Containers) == 1 {
				args = append(append(args, ctr.Command...), ctr.Args...)
			}
		}
		cp.Components[c] = append(cp.Components[c], Component{Pod: p.Name, Flags: ParseFlags(args)})
		if c == APIServer {
			cp.Visible = true
		}
	}
	for k := range cp.Components {
		sort.Slice(cp.Components[k], func(i, j int) bool { return cp.Components[k][i].Pod < cp.Components[k][j].Pod })
	}
	return cp
}

// flagRule judges one flag: the shown value and whether it complies. set is
// false when the flag is absent.
type flagRule func(v string, set bool) (string, bool)

func listHas(v, item string) bool {
	for _, x := range strings.Split(v, ",") {
		if strings.TrimSpace(x) == item {
			return true
		}
	}
	return false
}

func mustEqual(want, dflt string) flagRule {
	return func(v string, set bool) (string, bool) {
		if !set {
			return "unset (default " + dflt + ")", dflt == want
		}
		return v, v == want
	}
}

func mustBeSet(v string, set bool) (string, bool) {
	if !set || v == "" {
		return "unset", false
	}
	return v, true
}

func mustNotBeSet(v string, set bool) (string, bool) {
	if set {
		return v, false
	}
	return "unset", true
}

func listIncludes(item string) flagRule {
	return func(v string, set bool) (string, bool) {
		if !set {
			return "unset", false
		}
		return v, listHas(v, item)
	}
}

func listExcludes(item string) flagRule {
	return func(v string, set bool) (string, bool) {
		if !set {
			return "unset", true
		}
		return v, !listHas(v, item)
	}
}

func atLeast(n int) flagRule {
	return func(v string, set bool) (string, bool) {
		if !set {
			return "unset", false
		}
		i, err := strconv.Atoi(v)
		return v, err == nil && i >= n
	}
}

func ciphers(v string, set bool) (string, bool) {
	if !set {
		return "unset (Go defaults)", false
	}
	return strongCipherList(strings.Split(v, ","), "unset")
}

// flagCheck evaluates rule on flag of every pod of component. The same flag
// may live on another component's command line too (admission plugins on
// the API server).
func flagCheck(component, flag string, rule flagRule) func(in *Inputs) Outcome {
	return multiFlagCheck(component, []string{flag}, func(vals map[string]string, set map[string]bool) (string, bool) {
		return rule(vals[flag], set[flag])
	})
}

func multiFlagCheck(component string, flags []string, rule func(vals map[string]string, set map[string]bool) (string, bool)) func(in *Inputs) Outcome {
	return func(in *Inputs) Outcome {
		if !in.ControlPlane.Visible {
			return notVisible("The control-plane static pods are not visible from the API, so " + component + " flags cannot be read.")
		}
		pods := in.ControlPlane.Components[component]
		if len(pods) == 0 {
			return notVisible("No " + component + " static pod was found in kube-system.")
		}
		o := Outcome{Status: StatusPass}
		bad := 0
		for _, p := range pods {
			set := map[string]bool{}
			for _, f := range flags {
				_, set[f] = p.Flags[f]
			}
			shown, ok := rule(p.Flags, set)
			o.Values = append(o.Values, Value{Name: "--" + strings.Join(flags, ", --"), Value: shown, Source: p.Pod, OK: ok})
			if !ok {
				bad++
			}
		}
		if bad > 0 {
			o.Status = StatusFail
		}
		if len(pods) == 1 {
			o.Message = fmt.Sprintf("%s: %s", o.Values[0].Name, o.Values[0].Value)
		} else if bad > 0 {
			o.Message = fmt.Sprintf("%d of %d %s pods do not comply (%s: %s on %s).", bad, len(pods), component, o.Values[0].Name, firstBad(o.Values).Value, firstBad(o.Values).Source)
		} else {
			o.Message = fmt.Sprintf("All %d %s pods comply (%s: %s).", len(pods), component, o.Values[0].Name, o.Values[0].Value)
		}
		return o
	}
}

func firstBad(vs []Value) Value {
	for _, v := range vs {
		if !v.OK {
			return v
		}
	}
	return vs[0]
}

func admissionEnabled(plugin string, defaultOn bool) func(vals map[string]string, set map[string]bool) (string, bool) {
	return func(vals map[string]string, set map[string]bool) (string, bool) {
		en, dis := vals["enable-admission-plugins"], vals["disable-admission-plugins"]
		if set["disable-admission-plugins"] && listHas(dis, plugin) {
			return "disabled via --disable-admission-plugins=" + dis, false
		}
		if set["enable-admission-plugins"] && listHas(en, plugin) {
			return "enabled: " + en, true
		}
		if defaultOn {
			return plugin + " on by default", true
		}
		return "not in --enable-admission-plugins", false
	}
}

func durationAtLeast(min time.Duration) flagRule {
	return func(v string, set bool) (string, bool) {
		if !set {
			return "unset (default 1m0s)", true
		}
		d, err := time.ParseDuration(v)
		return v, err == nil && d >= min
	}
}

func featureGateNotFalse(gate string) flagRule {
	return func(v string, set bool) (string, bool) {
		if !set {
			return "unset", true
		}
		return v, !listHas(v, gate+"=false")
	}
}

func init() {
	cp := func(key, desc, remediation, snippet string, eval func(in *Inputs) Outcome) {
		reg(key, check{desc: desc, remediation: remediation, snippet: snippet, plane: true, eval: eval})
	}
	api := func(key, flag, desc, snippet string, rule flagRule) {
		cp(key, desc, "Edit the kube-apiserver static pod manifest (/etc/kubernetes/manifests/kube-apiserver.yaml) on each control-plane node; the kubelet restarts it.", snippet, flagCheck(APIServer, flag, rule))
	}
	adm := func(key, plugin, desc string, on bool, defaultOn bool) {
		eval := multiFlagCheck(APIServer, []string{"enable-admission-plugins", "disable-admission-plugins"}, admissionEnabled(plugin, defaultOn))
		if !on {
			eval = flagCheck(APIServer, "enable-admission-plugins", listExcludes(plugin))
		}
		snippet := "- --enable-admission-plugins=NodeRestriction," + plugin
		if !on {
			snippet = "# remove " + plugin + " from --enable-admission-plugins"
		}
		cp(key, desc, "Adjust the admission plugin list on the kube-apiserver static pod.", snippet, eval)
	}

	api("api.anonymous-auth", "anonymous-auth", "The API server rejects anonymous requests.", "- --anonymous-auth=false", mustEqual("false", "true"))
	api("api.token-auth-file", "token-auth-file", "Static token files are not used for authentication.", "# remove --token-auth-file", mustNotBeSet)
	adm("api.deny-external-ips", "DenyServiceExternalIPs", "Services cannot claim arbitrary external IPs.", true, false)
	cp("api.kubelet-client-cert", "The API server authenticates to kubelets with a client certificate.", "Set both kubelet client flags on the API server.",
		"- --kubelet-client-certificate=/etc/kubernetes/pki/apiserver-kubelet-client.crt\n- --kubelet-client-key=/etc/kubernetes/pki/apiserver-kubelet-client.key",
		multiFlagCheck(APIServer, []string{"kubelet-client-certificate", "kubelet-client-key"}, func(v map[string]string, s map[string]bool) (string, bool) {
			if s["kubelet-client-certificate"] && s["kubelet-client-key"] {
				return v["kubelet-client-certificate"], true
			}
			return "missing certificate or key", false
		}))
	api("api.kubelet-ca", "kubelet-certificate-authority", "The API server verifies kubelet serving certificates.", "- --kubelet-certificate-authority=/etc/kubernetes/pki/ca.crt", mustBeSet)
	api("api.authz-not-always-allow", "authorization-mode", "Authorization is not AlwaysAllow.", "- --authorization-mode=Node,RBAC", listExcludes("AlwaysAllow"))
	api("api.authz-node", "authorization-mode", "The Node authorizer is enabled.", "- --authorization-mode=Node,RBAC", listIncludes("Node"))
	api("api.authz-rbac", "authorization-mode", "The RBAC authorizer is enabled.", "- --authorization-mode=Node,RBAC", listIncludes("RBAC"))
	adm("api.event-rate-limit", "EventRateLimit", "Event floods are rate limited at admission.", true, false)
	adm("api.always-admit", "AlwaysAdmit", "The AlwaysAdmit plugin is not enabled.", false, false)
	adm("api.always-pull", "AlwaysPullImages", "Images are always pulled, so cached private images are not reused by other tenants.", true, false)
	adm("api.service-account", "ServiceAccount", "The ServiceAccount admission plugin is on.", true, true)
	adm("api.namespace-lifecycle", "NamespaceLifecycle", "The NamespaceLifecycle admission plugin is on.", true, true)
	adm("api.node-restriction", "NodeRestriction", "The NodeRestriction admission plugin limits what kubelets can modify.", true, false)
	api("api.profiling", "profiling", "Profiling endpoints are off on the API server.", "- --profiling=false", mustEqual("false", "true"))
	api("api.audit-log-path", "audit-log-path", "API server audit logging is enabled.", "- --audit-log-path=/var/log/kubernetes/audit/audit.log", mustBeSet)
	api("api.audit-log-maxage", "audit-log-maxage", "Audit logs are kept for at least 30 days.", "- --audit-log-maxage=30", atLeast(30))
	api("api.audit-log-maxbackup", "audit-log-maxbackup", "At least 10 rotated audit log files are kept.", "- --audit-log-maxbackup=10", atLeast(10))
	api("api.audit-log-maxsize", "audit-log-maxsize", "Audit log files rotate at 100 MB or more.", "- --audit-log-maxsize=100", atLeast(100))
	api("api.request-timeout", "request-timeout", "The request timeout is not set too low.", "- --request-timeout=300s", durationAtLeast(60*time.Second))
	api("api.sa-lookup", "service-account-lookup", "Service account tokens are checked against existing accounts.", "- --service-account-lookup=true", mustEqual("true", "true"))
	api("api.sa-key-file", "service-account-key-file", "The key used to verify service account tokens is set explicitly.", "- --service-account-key-file=/etc/kubernetes/pki/sa.pub", mustBeSet)
	cp("api.etcd-cert", "The API server authenticates to etcd with a client certificate.", "Set --etcd-certfile and --etcd-keyfile.",
		"- --etcd-certfile=/etc/kubernetes/pki/apiserver-etcd-client.crt\n- --etcd-keyfile=/etc/kubernetes/pki/apiserver-etcd-client.key",
		multiFlagCheck(APIServer, []string{"etcd-certfile", "etcd-keyfile"}, func(v map[string]string, s map[string]bool) (string, bool) {
			if s["etcd-certfile"] && s["etcd-keyfile"] {
				return v["etcd-certfile"], true
			}
			return "missing certificate or key", false
		}))
	cp("api.tls-cert", "The API server serves TLS with an explicit certificate.", "Set --tls-cert-file and --tls-private-key-file.",
		"- --tls-cert-file=/etc/kubernetes/pki/apiserver.crt\n- --tls-private-key-file=/etc/kubernetes/pki/apiserver.key",
		multiFlagCheck(APIServer, []string{"tls-cert-file", "tls-private-key-file"}, func(v map[string]string, s map[string]bool) (string, bool) {
			if s["tls-cert-file"] && s["tls-private-key-file"] {
				return v["tls-cert-file"], true
			}
			return "missing certificate or key", false
		}))
	api("api.client-ca", "client-ca-file", "Client certificates are verified against a CA.", "- --client-ca-file=/etc/kubernetes/pki/ca.crt", mustBeSet)
	api("api.etcd-ca", "etcd-cafile", "The API server verifies etcd's certificate.", "- --etcd-cafile=/etc/kubernetes/pki/etcd/ca.crt", mustBeSet)
	api("api.encryption-config", "encryption-provider-config", "Secrets are encrypted at rest in etcd.", "- --encryption-provider-config=/etc/kubernetes/enc/encryption-config.yaml", mustBeSet)
	reg("api.encryption-providers", check{plane: true, desc: "The encryption-at-rest provider is a strong one (KMS v2, aescbc or secretbox), not identity.",
		manual: "The encryption configuration file lives on the control-plane nodes. Check that its first provider for secrets is kms, aescbc or secretbox."})
	api("api.tls-ciphers", "tls-cipher-suites", "The API server only offers strong TLS cipher suites.", "- --tls-cipher-suites=TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256", ciphers)

	cm := func(key, flag, desc, snippet string, rule flagRule) {
		cp(key, desc, "Edit the kube-controller-manager static pod manifest on each control-plane node.", snippet, flagCheck(ControllerManager, flag, rule))
	}
	cm("cm.terminated-pod-gc", "terminated-pod-gc-threshold", "Terminated pods are garbage-collected at a set threshold.", "- --terminated-pod-gc-threshold=10", mustBeSet)
	cm("cm.profiling", "profiling", "Profiling is off on the controller manager.", "- --profiling=false", mustEqual("false", "true"))
	cm("cm.sa-credentials", "use-service-account-credentials", "Each controller runs with its own service account.", "- --use-service-account-credentials=true", mustEqual("true", "false"))
	cm("cm.sa-private-key", "service-account-private-key-file", "The service account signing key is set.", "- --service-account-private-key-file=/etc/kubernetes/pki/sa.key", mustBeSet)
	cm("cm.root-ca", "root-ca-file", "Pods receive the cluster root CA.", "- --root-ca-file=/etc/kubernetes/pki/ca.crt", mustBeSet)
	cm("cm.rotate-server-cert", "feature-gates", "Kubelet serving certificate rotation is not disabled.", "# do not set RotateKubeletServerCertificate=false", featureGateNotFalse("RotateKubeletServerCertificate"))
	cm("cm.bind-address", "bind-address", "The controller manager listens on localhost only.", "- --bind-address=127.0.0.1", mustEqual("127.0.0.1", "0.0.0.0"))
	sch := func(key, flag, desc, snippet string, rule flagRule) {
		cp(key, desc, "Edit the kube-scheduler static pod manifest on each control-plane node.", snippet, flagCheck(Scheduler, flag, rule))
	}
	sch("sch.profiling", "profiling", "Profiling is off on the scheduler.", "- --profiling=false", mustEqual("false", "true"))
	sch("sch.bind-address", "bind-address", "The scheduler listens on localhost only.", "- --bind-address=127.0.0.1", mustEqual("127.0.0.1", "0.0.0.0"))

	et := func(key, desc, snippet string, eval func(in *Inputs) Outcome) {
		cp(key, desc, "Edit the etcd static pod manifest (/etc/kubernetes/manifests/etcd.yaml) on each control-plane node.", snippet, eval)
	}
	et("etcd.cert", "etcd serves clients over TLS.", "- --cert-file=/etc/kubernetes/pki/etcd/server.crt\n- --key-file=/etc/kubernetes/pki/etcd/server.key",
		multiFlagCheck(Etcd, []string{"cert-file", "key-file"}, func(v map[string]string, s map[string]bool) (string, bool) {
			if s["cert-file"] && s["key-file"] {
				return v["cert-file"], true
			}
			return "missing certificate or key", false
		}))
	et("etcd.client-cert-auth", "etcd requires client certificates.", "- --client-cert-auth=true", flagCheck(Etcd, "client-cert-auth", mustEqual("true", "false")))
	et("etcd.auto-tls", "etcd does not use self-signed client TLS.", "# remove --auto-tls", flagCheck(Etcd, "auto-tls", mustEqual("false", "false")))
	et("etcd.peer-cert", "etcd peers talk over TLS.", "- --peer-cert-file=/etc/kubernetes/pki/etcd/peer.crt\n- --peer-key-file=/etc/kubernetes/pki/etcd/peer.key",
		multiFlagCheck(Etcd, []string{"peer-cert-file", "peer-key-file"}, func(v map[string]string, s map[string]bool) (string, bool) {
			if s["peer-cert-file"] && s["peer-key-file"] {
				return v["peer-cert-file"], true
			}
			return "missing certificate or key", false
		}))
	et("etcd.peer-client-cert-auth", "etcd peers authenticate each other with certificates.", "- --peer-client-cert-auth=true", flagCheck(Etcd, "peer-client-cert-auth", mustEqual("true", "false")))
	et("etcd.peer-auto-tls", "etcd peers do not use self-signed TLS.", "# remove --peer-auto-tls", flagCheck(Etcd, "peer-auto-tls", mustEqual("false", "false")))
	reg("etcd.unique-ca", check{plane: true, desc: "etcd uses its own CA, separate from the cluster CA.",
		manual: "Compare etcd's --trusted-ca-file with the API server's --client-ca-file; they should be different CAs.",
		eval: func(in *Inputs) Outcome {
			if !in.ControlPlane.Visible || len(in.ControlPlane.Components[Etcd]) == 0 || len(in.ControlPlane.Components[APIServer]) == 0 {
				return notVisible("etcd and kube-apiserver static pods are not both visible.")
			}
			e := in.ControlPlane.Components[Etcd][0].Flags["trusted-ca-file"]
			a := in.ControlPlane.Components[APIServer][0].Flags["client-ca-file"]
			o := Outcome{Values: []Value{{Name: "etcd --trusted-ca-file", Value: e, OK: e != a}, {Name: "apiserver --client-ca-file", Value: a, OK: e != a}}}
			if e == "" || a == "" {
				o.Status, o.Message, o.ManualCheck = StatusManual, "One of the CA paths is unset.", "Compare the two CA files on the control-plane node."
				return o
			}
			if e == a {
				o.Status, o.Message = StatusFail, "etcd trusts the same CA file as the API server's clients."
			} else {
				o.Status, o.Message, o.ManualCheck = StatusManual, "The CA paths differ; whether they hold different CAs needs node access.", "Confirm the two files contain different certificates."
			}
			return o
		}})

	cp("audit.policy", "An audit policy file is configured.", "Create an audit policy and pass it to the API server.", "- --audit-policy-file=/etc/kubernetes/audit-policy.yaml", flagCheck(APIServer, "audit-policy-file", mustBeSet))
	reg("audit.coverage", check{plane: true, desc: "The audit policy records the events that matter (secrets, RBAC, exec).",
		manual: "Read the audit policy file on the control-plane nodes and check it logs at least metadata for Secrets, ConfigMaps and TokenReviews and request bodies for RBAC changes."})
	reg("authn.client-certs", check{desc: "People do not authenticate with client certificates.",
		manual: "Client certificates cannot be revoked. Check that humans sign in through OIDC or the provider's IAM integration instead of certificates in kubeconfigs."})
	reg("authn.sa-tokens", check{desc: "Service account tokens are not handed to people.",
		manual: "Check that no user kubeconfig authenticates with a service account token."})
	reg("authn.bootstrap", check{desc: "Bootstrap tokens are not used for user authentication.",
		manual: "Check that bootstrap tokens in kube-system are short-lived and used only to join nodes."})

	reg("files.control-plane", check{plane: true, desc: "Control-plane manifest, kubeconfig, PKI and data files have tight permissions and root ownership.",
		eval: func(in *Inputs) Outcome {
			return notVisible("File permissions and ownership on control-plane nodes need node access (a kube-bench Job or the Trivy Operator node collector).")
		}})
	reg("files.worker", check{desc: "Kubelet service, kubeconfig, config and CA files have tight permissions and root ownership.",
		eval: func(in *Inputs) Outcome {
			return notVisible("File permissions and ownership on worker nodes need node access (a kube-bench Job or the Trivy Operator node collector).")
		}})
}
