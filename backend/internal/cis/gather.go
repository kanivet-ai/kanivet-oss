package cis

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/kanivet/backend/internal/clusterinfo"
	"github.com/kanivet/backend/internal/health"
	"github.com/kanivet/backend/internal/rbac"
)

var (
	gvrTrivyCompliance = schema.GroupVersionResource{Group: "aquasecurity.github.io", Version: "v1alpha1", Resource: "clustercompliancereports"}
	gvrKubescapeScans  = schema.GroupVersionResource{Group: "spdx.softwarecomposition.kubescape.io", Version: "v1beta1", Resource: "workloadconfigurationscans"}
)

func errText(err error) string {
	if apierrors.IsForbidden(err) {
		return "forbidden"
	}
	return shortErr(err)
}

// gather reads everything the checks need. Each read is independent and
// best-effort: a failure degrades the controls that need it.
func (s *Service) gather(ctx context.Context, cluster string) (*Inputs, error) {
	cs, cfg, err := s.clients.GetClientAndConfig(cluster)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	snap, err := s.loader.Load(ctx, cluster)
	if err != nil {
		return nil, err
	}
	in := &Inputs{Snap: snap}
	opts := metav1.ListOptions{ResourceVersion: "0"}

	var ksPods []string
	for _, p := range snap.Pods {
		if p.Namespace == "kube-system" {
			ksPods = append(ksPods, p.Name)
		}
	}
	host := ""
	if cfg != nil {
		host = cfg.Host
	}
	in.Provider = clusterinfo.Detect(clusterinfo.Gather(ctx, cs, host, snap.Nodes, ksPods))
	// The shared pod cache slims container command lines away, so the
	// control-plane static pods are read in full (kube-system only).
	if in.Provider.ControlPlaneVisible {
		if r, err := cs.CoreV1().Pods("kube-system").List(ctx, opts); err == nil {
			pods := make([]*corev1.Pod, 0, len(r.Items))
			for i := range r.Items {
				pods = append(pods, &r.Items[i])
			}
			in.ControlPlane = ReadControlPlane(pods)
		}
	}

	var wg sync.WaitGroup
	run := func(f func()) {
		wg.Add(1)
		go func() { defer wg.Done(); f() }()
	}
	run(func() {
		r, err := cs.CoreV1().Namespaces().List(ctx, opts)
		if err != nil {
			in.NamespacesErr = errText(err)
			return
		}
		in.Namespaces = r.Items
	})
	run(func() {
		r, err := cs.CoreV1().ServiceAccounts("").List(ctx, opts)
		if err == nil {
			in.ServiceAccounts = r.Items
			if in.ServiceAccounts == nil {
				in.ServiceAccounts = []corev1.ServiceAccount{}
			}
		}
	})
	run(func() {
		if s.rbac == nil {
			in.RBACErr = "RBAC service unavailable"
			return
		}
		g, err := s.rbac.Graph(ctx, cluster)
		if err != nil {
			in.RBACErr = errText(err)
			return
		}
		in.RBAC = g
	})
	run(func() {
		var names []string
		if r, err := cs.AdmissionregistrationV1().ValidatingWebhookConfigurations().List(ctx, opts); err == nil {
			for _, w := range r.Items {
				names = append(names, w.Name)
			}
		} else {
			in.AdmissionErr = errText(err)
		}
		if r, err := cs.AdmissionregistrationV1().MutatingWebhookConfigurations().List(ctx, opts); err == nil {
			for _, w := range r.Items {
				names = append(names, w.Name)
			}
		}
		if r, err := cs.AdmissionregistrationV1().ValidatingAdmissionPolicies().List(ctx, opts); err == nil {
			in.PolicyCount = len(r.Items)
		}
		in.AdmissionWebhooks = names
	})
	run(func() {
		if in.Provider.Provider != clusterinfo.EKS {
			return
		}
		cm, err := cs.CoreV1().ConfigMaps("kube-system").Get(ctx, "aws-auth", metav1.GetOptions{})
		switch {
		case apierrors.IsNotFound(err):
		case err != nil:
			in.AWSAuthErr = errText(err)
		default:
			in.AWSAuthFound, in.AWSAuth = true, cm.Data
		}
	})
	run(func() { in.Kubelet = s.kubelets(ctx, cs, snap.Nodes) })
	run(func() { in.Scanners = s.scanners(ctx, cluster, cs, snap) })
	wg.Wait()
	return in, nil
}

func (s *Service) kubelets(ctx context.Context, cs kubernetes.Interface, nodes []corev1.Node) KubeletState {
	if len(nodes) == 0 {
		return KubeletState{Reason: "Nodes could not be read"}
	}
	ok, reason := CanProxyNodes(ctx, cs)
	if !ok {
		return KubeletState{Reason: reason, Total: len(nodes)}
	}
	read := s.configz
	if read == nil {
		read = ConfigzVia(cs)
	}
	return ReadKubelets(ctx, nodes, read)
}

// scanners reads existing scanner results. Missing CRDs are normal.
func (s *Service) scanners(ctx context.Context, cluster string, cs kubernetes.Interface, snap *health.Snapshot) []ScannerReport {
	var out []ScannerReport
	dyn, err := s.clients.GetDynamicClient(cluster)
	if err == nil {
		out = append(out, readTrivy(ctx, dyn)...)
		if rep, ok := readKubescape(ctx, dyn); ok {
			out = append(out, rep)
		}
	}
	if rep, ok := readKubeBench(ctx, cs, snap); ok {
		out = append(out, rep)
	}
	return out
}

func readTrivy(ctx context.Context, dyn dynamic.Interface) []ScannerReport {
	l, err := dyn.Resource(gvrTrivyCompliance).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil
	}
	var out []ScannerReport
	for i := range l.Items {
		if rep, ok := ParseTrivyCompliance(&l.Items[i]); ok {
			out = append(out, rep)
		}
	}
	return out
}

func readKubescape(ctx context.Context, dyn dynamic.Interface) (ScannerReport, bool) {
	l, err := dyn.Resource(gvrKubescapeScans).Namespace("").List(ctx, metav1.ListOptions{Limit: 500})
	if err != nil || len(l.Items) == 0 {
		return ScannerReport{}, false
	}
	return ParseKubescapeScans(l.Items)
}

// readKubeBench finds the newest completed Job named like kube-bench and
// reads its pods' logs (read-only).
func readKubeBench(ctx context.Context, cs kubernetes.Interface, snap *health.Snapshot) (ScannerReport, bool) {
	var best *struct {
		ns, name string
		at       time.Time
	}
	for _, j := range snap.Jobs {
		if !strings.Contains(j.Name, "kube-bench") || j.Status.Succeeded == 0 || j.Status.CompletionTime == nil {
			continue
		}
		if best == nil || j.Status.CompletionTime.After(best.at) {
			best = &struct {
				ns, name string
				at       time.Time
			}{j.Namespace, j.Name, j.Status.CompletionTime.Time}
		}
	}
	if best == nil {
		return ScannerReport{}, false
	}
	var pods []*corev1.Pod
	for _, p := range snap.Pods {
		if p.Namespace == best.ns && (p.Labels["job-name"] == best.name || p.Labels["batch.kubernetes.io/job-name"] == best.name) {
			pods = append(pods, p)
		}
	}
	sort.Slice(pods, func(i, j int) bool { return pods[i].Name < pods[j].Name })
	rep := ScannerReport{Source: SourceKubeBench, Report: "Job " + best.ns + "/" + best.name, ScannedAt: best.at, Controls: map[string]ScannerFinding{}}
	tail := int64(5000)
	for _, p := range pods {
		b, err := cs.CoreV1().Pods(p.Namespace).GetLogs(p.Name, &corev1.PodLogOptions{TailLines: &tail}).DoRaw(ctx)
		if err != nil {
			continue
		}
		for id, f := range ParseKubeBench(string(b)) {
			f.Report, f.ScannedAt = rep.Report, rep.ScannedAt
			if prev, ok := rep.Controls[id]; ok && prev.Status == StatusFail {
				continue
			}
			rep.Controls[id] = f
		}
	}
	return rep, len(rep.Controls) > 0
}

// rbacGraph is what the service needs from the rbac package.
type rbacGraph interface {
	Graph(ctx context.Context, cluster string) (*rbac.Graph, error)
}
