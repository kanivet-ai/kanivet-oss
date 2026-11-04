package upgrade

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kanivet/backend/internal/helm"
)

func TestCatalogParsesAndIsConsistent(t *testing.T) {
	c := DefaultCatalog()
	if len(c.APIs) < 30 {
		t.Fatalf("catalog too small: %d", len(c.APIs))
	}
	seen := map[string]bool{}
	for _, d := range c.APIs {
		key := d.APIVersion() + "|" + d.Kind
		if seen[key] {
			t.Errorf("duplicate entry %s", key)
		}
		seen[key] = true
		if d.RemovedIn != "" && mustVersion(d.RemovedIn).Less(mustVersion(d.DeprecatedIn)) {
			t.Errorf("%s removed before deprecated", key)
		}
	}
	if _, ok := c.Lookup("policy/v1beta1", "PodSecurityPolicy"); !ok {
		t.Fatal("PSP missing")
	}
	if _, ok := c.LookupResource("batch", "v1beta1", "cronjobs"); !ok {
		t.Fatal("cronjobs missing")
	}
}

func TestParseVersion(t *testing.T) {
	for in, want := range map[string]string{
		"v1.29.3":              "1.29",
		"v1.27.16-eks-a18cd3a": "1.27",
		"1.30":                 "1.30",
		"v1.28.9+k3s1":         "1.28",
		"v1.31.1-gke.1678000":  "1.31",
	} {
		v, err := ParseVersion(in)
		if err != nil || v.String() != want {
			t.Errorf("ParseVersion(%q) = %v, %v; want %s", in, v, err, want)
		}
	}
	if _, err := ParseVersion("garbage"); err == nil {
		t.Error("expected error")
	}
	targets := DefaultCatalog().TargetsFrom(Version{1, 35})
	if len(targets) == 0 || targets[0] != "1.36" {
		t.Errorf("targets = %v", targets)
	}
}

func baseInputs() *Inputs {
	return &Inputs{
		ServerVersion: "v1.24.17",
		Served: map[string]bool{
			"policy/v1beta1/podsecuritypolicies": true,
			"batch/v1beta1/cronjobs":             true,
		},
		CollectedAt: time.Now(),
	}
}

func TestAnalyzeSeverityBySource(t *testing.T) {
	in := baseInputs()
	in.Requests = []DeprecatedRequest{{Group: "batch", Version: "v1beta1", Resource: "cronjobs", RemovedRelease: "1.25"}}
	in.Objects = []ObservedObject{
		{APIVersion: "batch/v1beta1", LiveAPIVersion: "batch/v1", Kind: "CronJob", Name: "nightly", Namespace: "ops", Source: SourceLastApplied},
		{APIVersion: "policy/v1beta1", Kind: "PodDisruptionBudget", Name: "web", Namespace: "shop", Source: SourceHelm, HelmRelease: "shop", HelmNamespace: "shop"},
		{APIVersion: "policy/v1beta1", LiveAPIVersion: "policy/v1beta1", Kind: "PodSecurityPolicy", Name: "restricted", Source: SourceLive},
		// Already removed long ago, still in a Helm manifest.
		{APIVersion: "extensions/v1beta1", Kind: "Ingress", Name: "old", Namespace: "legacy", Source: SourceHelm, HelmRelease: "legacy"},
		// A current API is ignored.
		{APIVersion: "apps/v1", Kind: "Deployment", Name: "fine", Source: SourceLastApplied},
	}
	rep, err := Analyze(DefaultCatalog(), "c", in, "1.25")
	if err != nil {
		t.Fatal(err)
	}
	find := func(av, kind string) *APIFinding {
		for i := range rep.APIs {
			if rep.APIs[i].APIVersion == av && rep.APIs[i].Kind == kind {
				return &rep.APIs[i]
			}
		}
		return nil
	}
	cj := find("batch/v1beta1", "CronJob")
	if cj == nil || cj.Severity != SeverityBlocker || !cj.LiveRequests || !cj.Served {
		t.Fatalf("cronjob finding = %+v", cj)
	}
	if len(cj.Resources) != 1 || cj.Resources[0].Severity != SeverityWarning {
		t.Errorf("last-applied cronjob should be a warning: %+v", cj.Resources)
	}
	if pdb := find("policy/v1beta1", "PodDisruptionBudget"); pdb == nil || pdb.Severity != SeverityBlocker {
		t.Errorf("helm PDB should block: %+v", pdb)
	}
	if psp := find("policy/v1beta1", "PodSecurityPolicy"); psp == nil || psp.Severity != SeverityBlocker || psp.Replacement != "" {
		t.Errorf("PSP should block: %+v", psp)
	}
	ing := find("extensions/v1beta1", "Ingress")
	if ing == nil || !ing.AlreadyRemoved || ing.Severity != SeverityBlocker {
		t.Errorf("removed ingress in helm: %+v", ing)
	}
	if find("apps/v1", "Deployment") != nil {
		t.Error("apps/v1 is not deprecated")
	}
	if rep.Summary.Verdict != "blocked" || rep.Summary.Blockers != 4 {
		t.Errorf("summary = %+v", rep.Summary)
	}
	if rep.APIs[0].Severity != SeverityBlocker {
		t.Error("blockers sort first")
	}
	if rep.Summary.OK == 0 {
		t.Error("unused APIs removed in 1.25 should count as OK")
	}
}

func TestAnalyzeTargetWindow(t *testing.T) {
	in := baseInputs()
	in.ServerVersion = "v1.27.3"
	in.Objects = []ObservedObject{
		{APIVersion: "flowcontrol.apiserver.k8s.io/v1beta2", Kind: "FlowSchema", Name: "x", Source: SourceLastApplied},
	}
	// 1.28: deprecated (since 1.26) but not yet removed.
	rep, _ := Analyze(DefaultCatalog(), "c", in, "1.28")
	if len(rep.APIs) != 1 || rep.APIs[0].Severity != SeverityWarning || rep.APIs[0].RemovedByTarget {
		t.Fatalf("1.28: %+v", rep.APIs)
	}
	// 1.29 removes it.
	rep, _ = Analyze(DefaultCatalog(), "c", in, "1.29")
	if !rep.APIs[0].RemovedByTarget {
		t.Fatal("1.29 removes v1beta2")
	}
	if len(rep.Path) != 2 || len(rep.Notes) == 0 {
		t.Errorf("path %v notes %v", rep.Path, rep.Notes)
	}
	// Default target is the next minor.
	rep, _ = Analyze(DefaultCatalog(), "c", in, "")
	if rep.TargetVersion != "1.28" {
		t.Errorf("default target %s", rep.TargetVersion)
	}
	if _, err := Analyze(DefaultCatalog(), "c", in, "1.20"); err == nil {
		t.Error("an older target is an error")
	}
}

func TestAnalyzeNodesSkew(t *testing.T) {
	in := baseInputs()
	in.ServerVersion = "v1.30.2"
	in.Nodes = []NodeInfo{
		{Name: "a", KubeletVersion: "v1.30.2"},
		{Name: "b", KubeletVersion: "v1.28.5"},
		{Name: "c", KubeletVersion: "v1.27.1"},
		{Name: "d", KubeletVersion: "v1.31.0"},
	}
	rep, _ := Analyze(DefaultCatalog(), "c", in, "1.31")
	got := map[string]Severity{}
	for _, n := range rep.Nodes {
		got[n.Node] = n.Severity
	}
	if got["c"] != SeverityBlocker || got["b"] != SeverityWarning || got["d"] != SeverityWarning || got["a"] != "" {
		t.Errorf("nodes = %v", got)
	}
	if rep.NodeSummary.MinKubelet != "1.27" || rep.NodeSummary.MaxSkew != 4 {
		t.Errorf("summary = %+v", rep.NodeSummary)
	}
}

func TestAnalyzeCRDs(t *testing.T) {
	in := baseInputs()
	in.CRDs = []CRDInfo{
		{Name: "widgets.example.com", Kind: "Widget", StoredVersions: []string{"v1alpha1", "v1"},
			Versions: []CRDVersion{{Name: "v1", Served: true, Storage: true}}},
		{Name: "fine.example.com", Kind: "Fine", StoredVersions: []string{"v1"},
			Versions: []CRDVersion{{Name: "v1", Served: true, Storage: true}}},
	}
	rep, _ := Analyze(DefaultCatalog(), "c", in, "1.25")
	if len(rep.CRDs) != 1 || rep.CRDs[0].Version != "v1alpha1" || rep.CRDs[0].Severity != SeverityWarning {
		t.Errorf("crds = %+v", rep.CRDs)
	}
}

func TestParseDeprecatedRequests(t *testing.T) {
	raw := []byte(`# HELP apiserver_requested_deprecated_apis Gauge of deprecated APIs
# TYPE apiserver_requested_deprecated_apis gauge
apiserver_requested_deprecated_apis{group="batch",removed_release="1.25",resource="cronjobs",subresource="",version="v1beta1"} 1
apiserver_requested_deprecated_apis{group="policy",removed_release="1.25",resource="podsecuritypolicies",subresource="",version="v1beta1"} 1
apiserver_requested_deprecated_apis{group="policy",removed_release="1.25",resource="podsecuritypolicies",subresource="status",version="v1beta1"} 1
apiserver_requested_deprecated_apis{group="x",removed_release="",resource="old",subresource="",version="v1"} 0
apiserver_request_total{code="200"} 5
`)
	got := ParseDeprecatedRequests(raw)
	if len(got) != 3 || got[0].Resource != "cronjobs" || got[0].RemovedRelease != "1.25" || got[2].Subresource != "status" {
		t.Fatalf("got %+v", got)
	}
}

func TestLastAppliedAPIVersion(t *testing.T) {
	ann := map[string]string{lastAppliedAnnotation: `{"apiVersion":"extensions/v1beta1","kind":"Ingress"}`}
	if got := LastAppliedAPIVersion(ann); got != "extensions/v1beta1" {
		t.Errorf("got %q", got)
	}
	if LastAppliedAPIVersion(map[string]string{lastAppliedAnnotation: "{bad"}) != "" {
		t.Error("bad JSON yields empty")
	}
}

func TestParseManifestAndHelmObjects(t *testing.T) {
	manifest := `---
# Source: chart/templates/pdb.yaml
apiVersion: policy/v1beta1
kind: PodDisruptionBudget
metadata:
  name: web
spec:
  minAvailable: 1
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: cfg
data:
  script: |
    echo a
    ---
    echo b
---
apiVersion: v1
kind: List
items:
- apiVersion: batch/v1beta1
  kind: CronJob
  metadata:
    name: job
    namespace: other
`
	objs := ParseManifest(manifest)
	if len(objs) != 3 {
		t.Fatalf("objs = %+v", objs)
	}
	byKind := map[string][]servedResource{
		"PodDisruptionBudget": {{Group: "policy", Version: "v1", Resource: "poddisruptionbudgets", Kind: "PodDisruptionBudget", Namespaced: true, Listable: true}},
	}
	got := helmObjects(DefaultCatalog(), helm.Release{Name: "shop", Namespace: "shop-ns"}, manifest, byKind)
	if len(got) != 2 {
		t.Fatalf("helm objects = %+v", got)
	}
	if got[0].Namespace != "shop-ns" || got[0].LiveAPIVersion != "policy/v1" || got[0].HelmRelease != "shop" {
		t.Errorf("pdb = %+v", got[0])
	}
	if got[1].Namespace != "other" || got[1].LiveAPIVersion != "" {
		t.Errorf("cronjob = %+v", got[1])
	}
}

func TestListTargetsPrefersReplacement(t *testing.T) {
	byKind := map[string][]servedResource{
		"Ingress": {
			{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses", Kind: "Ingress", Listable: true},
		},
		"Widget": {{Group: "example.com", Version: "v1", Resource: "widgets", Kind: "Widget", Listable: true}},
		"Event":  {{Group: "", Version: "v1", Resource: "events", Kind: "Event", Listable: true}},
	}
	ts := listTargets(DefaultCatalog(), byKind)
	if len(ts) != 1 || ts[0].Resource != "ingresses" {
		t.Errorf("targets = %+v", ts)
	}
}

func TestServiceCachesInputsAcrossTargets(t *testing.T) {
	s := &Service{cat: DefaultCatalog(), cache: map[string]*Inputs{}}
	var calls atomic.Int32
	s.collectFn = func(ctx context.Context, cluster string) (*Inputs, error) {
		calls.Add(1)
		in := baseInputs()
		return in, nil
	}
	for _, target := range []string{"1.25", "1.26", ""} {
		if _, err := s.UpgradeReport(context.Background(), "c", target, false); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Errorf("collected %d times", calls.Load())
	}
	if _, err := s.UpgradeReport(context.Background(), "c", "1.25", true); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Errorf("refresh should collect again")
	}
}
