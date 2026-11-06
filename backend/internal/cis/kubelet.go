package cis

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	authv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// KubeletConfig is the part of a kubelet's live configuration (configz) the
// benchmark reads. configz omits zero values, so an absent field is its
// zero value, except where noted.
type KubeletConfig struct {
	Authentication struct {
		Anonymous struct {
			Enabled *bool `json:"enabled"`
		} `json:"anonymous"`
		Webhook struct {
			Enabled *bool `json:"enabled"`
		} `json:"webhook"`
		X509 struct {
			ClientCAFile string `json:"clientCAFile"`
		} `json:"x509"`
	} `json:"authentication"`
	Authorization struct {
		Mode string `json:"mode"`
	} `json:"authorization"`
	ReadOnlyPort                   int32           `json:"readOnlyPort"`
	StreamingConnectionIdleTimeout string          `json:"streamingConnectionIdleTimeout"`
	ProtectKernelDefaults          bool            `json:"protectKernelDefaults"`
	MakeIPTablesUtilChains         *bool           `json:"makeIPTablesUtilChains"`
	EventRecordQPS                 *int32          `json:"eventRecordQPS"`
	TLSCertFile                    string          `json:"tlsCertFile"`
	TLSPrivateKeyFile              string          `json:"tlsPrivateKeyFile"`
	ServerTLSBootstrap             bool            `json:"serverTLSBootstrap"`
	RotateCertificates             bool            `json:"rotateCertificates"`
	FeatureGates                   map[string]bool `json:"featureGates"`
	TLSCipherSuites                []string        `json:"tlsCipherSuites"`
	SeccompDefault                 bool            `json:"seccompDefault"`
}

// ParseConfigz reads GET /api/v1/nodes/<n>/proxy/configz.
func ParseConfigz(b []byte) (*KubeletConfig, error) {
	var wrap struct {
		KubeletConfig *KubeletConfig `json:"kubeletconfig"`
	}
	if err := json.Unmarshal(b, &wrap); err != nil {
		return nil, err
	}
	if wrap.KubeletConfig == nil {
		return nil, fmt.Errorf("configz has no kubeletconfig")
	}
	return wrap.KubeletConfig, nil
}

// KubeletGroup is the configuration shared by sampled nodes of one pool.
type KubeletGroup struct {
	Pool       string
	Nodes      []string
	TotalNodes int
	Config     *KubeletConfig
	Err        string
}

// KubeletState is the kubelet half of Inputs.
type KubeletState struct {
	// Allowed is the nodes/proxy access review result.
	Allowed bool
	Reason  string
	Groups  []KubeletGroup
	Sampled int
	Total   int
}

// strongCiphers are the TLS suites the benchmark accepts.
var strongCiphers = map[string]bool{
	"TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256": true, "TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256": true,
	"TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305": true, "TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384": true,
	"TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305": true, "TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384": true,
	"TLS_RSA_WITH_AES_256_GCM_SHA384": true, "TLS_RSA_WITH_AES_128_GCM_SHA256": true,
	"TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256": true, "TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256": true,
	"TLS_AES_128_GCM_SHA256": true, "TLS_AES_256_GCM_SHA384": true, "TLS_CHACHA20_POLY1305_SHA256": true,
}

func strongCipherList(xs []string, empty string) (string, bool) {
	if len(xs) == 0 {
		return empty, false
	}
	for _, c := range xs {
		if !strongCiphers[c] {
			return "weak suite " + c, false
		}
	}
	return strings.Join(xs, ","), true
}

// kubeletRules maps a check key to what it reads from the config: the
// displayed value and whether it meets the benchmark.
var kubeletRules = map[string]func(c *KubeletConfig) (string, bool){
	"kubelet.anonymous-auth": func(c *KubeletConfig) (string, bool) {
		// The kubelet's own default is enabled; configz always renders it.
		if c.Authentication.Anonymous.Enabled == nil {
			return "authentication.anonymous.enabled unset (defaults to true)", false
		}
		v := *c.Authentication.Anonymous.Enabled
		return "authentication.anonymous.enabled=" + strconv.FormatBool(v), !v
	},
	"kubelet.authz-mode": func(c *KubeletConfig) (string, bool) {
		m := c.Authorization.Mode
		if m == "" {
			m = "unset"
		}
		return "authorization.mode=" + m, c.Authorization.Mode == "Webhook"
	},
	"kubelet.client-ca": func(c *KubeletConfig) (string, bool) {
		if c.Authentication.X509.ClientCAFile == "" {
			return "authentication.x509.clientCAFile unset", false
		}
		return "authentication.x509.clientCAFile=" + c.Authentication.X509.ClientCAFile, true
	},
	"kubelet.read-only-port": func(c *KubeletConfig) (string, bool) {
		return fmt.Sprintf("readOnlyPort=%d", c.ReadOnlyPort), c.ReadOnlyPort == 0
	},
	"kubelet.streaming-timeout": func(c *KubeletConfig) (string, bool) {
		v := c.StreamingConnectionIdleTimeout
		if v == "" {
			return "streamingConnectionIdleTimeout unset (0, no timeout)", false
		}
		d, err := time.ParseDuration(v)
		return "streamingConnectionIdleTimeout=" + v, err == nil && d > 0
	},
	"kubelet.protect-kernel-defaults": func(c *KubeletConfig) (string, bool) {
		return "protectKernelDefaults=" + strconv.FormatBool(c.ProtectKernelDefaults), c.ProtectKernelDefaults
	},
	"kubelet.iptables-chains": func(c *KubeletConfig) (string, bool) {
		if c.MakeIPTablesUtilChains == nil {
			return "makeIPTablesUtilChains unset (defaults to true)", true
		}
		return "makeIPTablesUtilChains=" + strconv.FormatBool(*c.MakeIPTablesUtilChains), *c.MakeIPTablesUtilChains
	},
	"kubelet.event-qps": func(c *KubeletConfig) (string, bool) {
		if c.EventRecordQPS == nil {
			return "eventRecordQPS=0 (no limit)", true
		}
		return fmt.Sprintf("eventRecordQPS=%d", *c.EventRecordQPS), *c.EventRecordQPS >= 0
	},
	"kubelet.tls-cert": func(c *KubeletConfig) (string, bool) {
		if c.ServerTLSBootstrap {
			return "serverTLSBootstrap=true", true
		}
		if c.TLSCertFile != "" && c.TLSPrivateKeyFile != "" {
			return "tlsCertFile=" + c.TLSCertFile, true
		}
		return "tlsCertFile/tlsPrivateKeyFile unset (self-signed serving cert)", false
	},
	"kubelet.rotate-certs": func(c *KubeletConfig) (string, bool) {
		return "rotateCertificates=" + strconv.FormatBool(c.RotateCertificates), c.RotateCertificates
	},
	"kubelet.rotate-server-cert": func(c *KubeletConfig) (string, bool) {
		if v, ok := c.FeatureGates["RotateKubeletServerCertificate"]; ok && !v {
			return "featureGates.RotateKubeletServerCertificate=false", false
		}
		if c.ServerTLSBootstrap {
			return "serverTLSBootstrap=true", true
		}
		return "RotateKubeletServerCertificate not disabled", true
	},
	"kubelet.tls-ciphers": func(c *KubeletConfig) (string, bool) {
		return strongCipherList(c.TLSCipherSuites, "tlsCipherSuites unset (Go defaults)")
	},
	"kubelet.seccomp-default": func(c *KubeletConfig) (string, bool) {
		return "seccompDefault=" + strconv.FormatBool(c.SeccompDefault), c.SeccompDefault
	},
}

// kubeletCheck evaluates one kubelet rule over every node group.
func kubeletCheck(key string) func(in *Inputs) Outcome {
	rule := kubeletRules[key]
	return func(in *Inputs) Outcome {
		k := in.Kubelet
		if !k.Allowed {
			msg := "Kubelet configuration needs get on nodes/proxy, which was denied"
			if k.Reason != "" {
				msg = k.Reason
			}
			return notVisible(msg + ". Grant nodes/proxy (get) to see kubelet checks.")
		}
		var groups []NodeGroup
		bad, read := 0, 0
		for _, g := range k.Groups {
			ng := NodeGroup{Pool: g.Pool, Nodes: g.Nodes, TotalNodes: g.TotalNodes}
			if g.Config == nil {
				ng.Error = g.Err
				groups = append(groups, ng)
				continue
			}
			read++
			ng.Value, ng.OK = rule(g.Config)
			if !ng.OK {
				bad++
			}
			groups = append(groups, ng)
		}
		if read == 0 {
			o := notVisible("No kubelet configuration could be read" + firstErr(k.Groups) + ".")
			o.NodeGroups = groups
			return o
		}
		o := Outcome{NodeGroups: groups}
		sample := ""
		if k.Sampled < k.Total {
			sample = fmt.Sprintf(" (sampled %d of %d nodes)", k.Sampled, k.Total)
		}
		if bad == 0 {
			o.Status = StatusPass
			o.Message = fmt.Sprintf("All %d node configuration group(s) comply%s.", read, sample)
		} else {
			o.Status = StatusFail
			o.Message = fmt.Sprintf("%d of %d node configuration group(s) do not comply%s.", bad, read, sample)
		}
		if read < len(k.Groups) {
			o.Message += fmt.Sprintf(" %d group(s) could not be read.", len(k.Groups)-read)
		}
		return o
	}
}

func firstErr(gs []KubeletGroup) string {
	for _, g := range gs {
		if g.Err != "" {
			return ": " + g.Err
		}
	}
	return ""
}

// PoolOf names a node's pool from the labels the providers set.
func PoolOf(n corev1.Node) string {
	for _, k := range []string{"eks.amazonaws.com/nodegroup", "karpenter.sh/nodepool", "cloud.google.com/gke-nodepool", "kubernetes.azure.com/agentpool", "agentpool", "node.kubernetes.io/instance-type"} {
		if v := n.Labels[k]; v != "" {
			return v
		}
	}
	if _, ok := n.Labels["node-role.kubernetes.io/control-plane"]; ok {
		return "control-plane"
	}
	return "nodes"
}

// Sampling limits for kubelet reads.
const (
	perPoolSample  = 3
	maxSampled     = 30
	kubeletWorkers = 6
)

// ConfigzReader fetches one node's configz.
type ConfigzReader func(ctx context.Context, node string) ([]byte, error)

// ReadKubelets samples nodes per pool, reads their configz with bounded
// concurrency and groups nodes whose relevant settings are identical.
func ReadKubelets(ctx context.Context, nodes []corev1.Node, read ConfigzReader) KubeletState {
	pools := map[string][]corev1.Node{}
	for _, n := range nodes {
		if n.Labels["eks.amazonaws.com/compute-type"] == "fargate" {
			pools["fargate"] = append(pools["fargate"], n)
			continue
		}
		p := PoolOf(n)
		pools[p] = append(pools[p], n)
	}
	names := make([]string, 0, len(pools))
	for p := range pools {
		names = append(names, p)
	}
	sort.Strings(names)

	type job struct {
		pool string
		node string
	}
	var jobs []job
	for _, p := range names {
		if p == "fargate" {
			continue
		}
		ns := pools[p]
		sort.Slice(ns, func(i, j int) bool { return ns[i].Name < ns[j].Name })
		for i := 0; i < len(ns) && i < perPoolSample; i++ {
			jobs = append(jobs, job{p, ns[i].Name})
		}
	}
	if len(jobs) > maxSampled {
		jobs = jobs[:maxSampled]
	}
	type result struct {
		cfg *KubeletConfig
		err error
	}
	results := make([]result, len(jobs))
	sem := make(chan struct{}, kubeletWorkers)
	var wg sync.WaitGroup
	for i, j := range jobs {
		wg.Add(1)
		go func(i int, j job) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
			defer cancel()
			b, err := read(cctx, j.node)
			if err != nil {
				results[i] = result{err: err}
				return
			}
			cfg, err := ParseConfigz(b)
			results[i] = result{cfg, err}
		}(i, j)
	}
	wg.Wait()

	st := KubeletState{Allowed: true, Total: len(nodes), Sampled: len(jobs)}
	type key struct{ pool, hash string }
	idx := map[key]int{}
	for i, j := range jobs {
		r := results[i]
		h := "error"
		if r.err == nil {
			b, _ := json.Marshal(r.cfg)
			sum := sha256.Sum256(b)
			h = hex.EncodeToString(sum[:8])
		}
		k := key{j.pool, h}
		gi, ok := idx[k]
		if !ok {
			g := KubeletGroup{Pool: j.pool, TotalNodes: len(pools[j.pool]), Config: r.cfg}
			if r.err != nil {
				g.Err = shortErr(r.err)
			}
			st.Groups = append(st.Groups, g)
			gi = len(st.Groups) - 1
			idx[k] = gi
		}
		st.Groups[gi].Nodes = append(st.Groups[gi].Nodes, j.node)
	}
	if f := pools["fargate"]; len(f) > 0 {
		st.Groups = append(st.Groups, KubeletGroup{Pool: "fargate", TotalNodes: len(f), Err: "Fargate nodes do not expose the kubelet"})
	}
	return st
}

func shortErr(err error) string {
	s := err.Error()
	if len(s) > 160 {
		s = s[:160] + "..."
	}
	return s
}

// CanProxyNodes asks the API server whether the caller may read nodes/proxy.
func CanProxyNodes(ctx context.Context, cs kubernetes.Interface) (bool, string) {
	rev, err := cs.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, &authv1.SelfSubjectAccessReview{
		Spec: authv1.SelfSubjectAccessReviewSpec{ResourceAttributes: &authv1.ResourceAttributes{
			Verb: "get", Resource: "nodes", Subresource: "proxy",
		}},
	}, metav1.CreateOptions{})
	if err != nil {
		return false, "The nodes/proxy access review failed: " + shortErr(err)
	}
	if !rev.Status.Allowed {
		return false, "Your identity may not get nodes/proxy"
	}
	return true, ""
}

// ConfigzVia reads configz through the API server's node proxy (GET only).
func ConfigzVia(cs kubernetes.Interface) ConfigzReader {
	return func(ctx context.Context, node string) ([]byte, error) {
		return cs.CoreV1().RESTClient().Get().AbsPath("/api/v1/nodes", node, "proxy", "configz").DoRaw(ctx)
	}
}

func init() {
	k := func(key, desc, remediation, snippet string) {
		reg(key, check{desc: desc, remediation: remediation, snippet: snippet, eval: kubeletCheck(key)})
	}
	k("kubelet.anonymous-auth", "The kubelet rejects anonymous requests.",
		"Disable anonymous authentication in the kubelet configuration and restart the kubelet.",
		"authentication:\n  anonymous:\n    enabled: false")
	k("kubelet.authz-mode", "The kubelet authorizes requests through the API server (Webhook), not AlwaysAllow.",
		"Set authorization.mode to Webhook in the kubelet configuration.",
		"authorization:\n  mode: Webhook")
	k("kubelet.client-ca", "The kubelet verifies client certificates against a CA.",
		"Point authentication.x509.clientCAFile at the cluster CA.",
		"authentication:\n  x509:\n    clientCAFile: /etc/kubernetes/pki/ca.crt")
	k("kubelet.read-only-port", "The unauthenticated read-only port is off.",
		"Set readOnlyPort to 0.",
		"readOnlyPort: 0")
	k("kubelet.streaming-timeout", "Idle exec, attach and port-forward streams time out.",
		"Set streamingConnectionIdleTimeout to a non-zero duration such as 5m.",
		"streamingConnectionIdleTimeout: 5m")
	k("kubelet.protect-kernel-defaults", "The kubelet refuses to start when kernel tunables differ from what it expects instead of changing them.",
		"Set protectKernelDefaults: true after aligning the node's sysctls with the kubelet's expectations.",
		"protectKernelDefaults: true")
	k("kubelet.iptables-chains", "The kubelet manages its iptables utility chains.",
		"Leave makeIPTablesUtilChains at true.",
		"makeIPTablesUtilChains: true")
	k("kubelet.event-qps", "Event recording is not disabled by the kubelet's rate limit.",
		"Set eventRecordQPS to a value that keeps the events you need (0 means unlimited).",
		"eventRecordQPS: 50")
	k("kubelet.tls-cert", "The kubelet serves TLS with a proper certificate.",
		"Configure tlsCertFile and tlsPrivateKeyFile, or serverTLSBootstrap: true with a CSR approver.",
		"serverTLSBootstrap: true")
	k("kubelet.rotate-certs", "The kubelet rotates its client certificate.",
		"Set rotateCertificates: true.",
		"rotateCertificates: true")
	k("kubelet.rotate-server-cert", "The kubelet rotates its serving certificate.",
		"Do not disable the RotateKubeletServerCertificate feature gate; use serverTLSBootstrap.",
		"serverTLSBootstrap: true\nfeatureGates:\n  RotateKubeletServerCertificate: true")
	k("kubelet.tls-ciphers", "The kubelet only offers strong TLS cipher suites.",
		"List strong suites in tlsCipherSuites.",
		"tlsCipherSuites:\n- TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256\n- TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256\n- TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384\n- TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384")
	reg("kubelet.hostname-override", check{
		desc:   "The kubelet does not override its hostname.",
		manual: "--hostname-override is a command-line flag and is not in configz. Check the kubelet unit or the node bootstrap script (on cloud providers it is often required and can be accepted).",
	})
}
