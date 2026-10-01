package rightsizing

import (
	"strings"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestWorkloadKey(t *testing.T) {
	cases := []struct{ pod, vns, wk string }{
		{"api-7d4f8b9c5-x2x4z", "", "api"},
		{"argocd-application-controller-3", "", "argocd-application-controller"},
		{"node-exporter-x2x4z", "", "node-exporter"},
		{"backup-29312345-abcd2", "", "backup-29312345-abcd2"}, // 'a' is not a safe char
		{"backup-29312345-bcdf2", "", "backup"},
		{"opencost-7566c47-t75ds", "", "opencost"},
		{"kube-proxy-x2x4z", "", "kube-proxy"},
		{"etcd", "", "etcd"},
		{"access-manager-78589b95f8-66lwn-x-applications-x-beige-vcluster", "applications", "access-manager"},
		{"coredns-6d4b75cb6d-wzz5l-x-kube-system-x-beige-vcluster", "kube-system", "coredns"},
		{"arch-test-predictor-84767dffdc-bgcmh-x-applications--c77b6caaae", "", "arch-test-predictor"},
		{"beige-vcluster-etcd-0", "", "beige-vcluster-etcd"},
	}
	for _, c := range cases {
		vns, wk := workloadKey(c.pod)
		if vns != c.vns || wk != c.wk {
			t.Errorf("%s: got (%q, %q), want (%q, %q)", c.pod, vns, wk, c.vns, c.wk)
		}
	}
}

func TestRelabelShape(t *testing.T) {
	q := relabel("up")
	if strings.Count(q, "label_replace(") != len(keyRules) {
		t.Fatalf("one label_replace per rule: %s", q)
	}
	// PromQL takes the regex as a double-quoted string; %q must not produce
	// escapes PromQL would read differently.
	if strings.Contains(q, `\\`) {
		t.Fatalf("unexpected escaping: %s", q)
	}
}

func TestAlignPooledStartupOnlyAfterLatestStart(t *testing.T) {
	g := newGrid(time.Unix(100000, 0), 10*time.Hour, 5*time.Minute)
	start := g.end.Add(-time.Hour).Unix() // restarted an hour before the end
	var times []int64
	var vals []float32
	for i := range g.n {
		times = append(times, g.start.Add(time.Duration(i)*g.step).Unix())
		vals = append(vals, 1)
	}
	res := seriesSet{{{Labels: map[string]string{"namespace": "n", "pod": "db-0", "wk": "db", "container": "c"}, Times: times, Values: vals}}}
	cpu, _, startup, _ := alignPooled(res, g, map[string]int64{"n/db-0": start}, nil, nil)
	k := seriesKey{"n", "", "db", "c"}
	kept := len(cpu[k].vals)
	// Two samples fall in the 10 minutes after the restart (t, t+5m).
	if kept != g.n-2 || startup[k] != 1 {
		t.Fatalf("kept %d of %d samples, startup %v", kept, g.n, startup[k])
	}
}

func TestAlignPooledSkipsUnwantedAndPoolsAcrossParts(t *testing.T) {
	g := newGrid(time.Unix(100000, 0), time.Hour, 5*time.Minute)
	ts := func(i int) int64 { return g.start.Add(time.Duration(i) * g.step).Unix() }
	lbl := func(pod, wk string) map[string]string {
		return map[string]string{"namespace": "n", "pod": pod, "wk": wk, "container": "c"}
	}
	// api-1 arrives split across two day parts; gone-1 belongs to a workload
	// that no longer runs.
	res := seriesSet{
		{{Labels: lbl("api-1", "api"), Times: []int64{ts(0), ts(1)}, Values: []float32{1, 2}},
			{Labels: lbl("gone-1", "gone"), Times: []int64{ts(0)}, Values: []float32{9}}},
		{{Labels: lbl("api-1", "api"), Times: []int64{ts(2)}, Values: []float32{3}},
			{Labels: lbl("api-2", "api"), Times: []int64{ts(1)}, Values: []float32{5}}},
	}
	want := scope{want: map[seriesKey]struct{}{{"n", "", "api", "c"}: {}}}
	cpu, _, _, _ := alignPooled(res, g, nil, want.keep(), nil)
	if len(cpu) != 1 {
		t.Fatalf("%d keys aligned, want only the live workload", len(cpu))
	}
	p := cpu[seriesKey{"n", "", "api", "c"}]
	if got := p.at(1); len(got) != 2 || got[0]+got[1] != 7 {
		t.Fatalf("step 1 holds %v, want both replicas", got)
	}
	if len(p.at(0)) != 1 || len(p.at(2)) != 1 || len(p.vals) != 4 {
		t.Fatalf("layout %+v", p)
	}
}

func TestLiveWorkloadsDropFinishedOneOffJobs(t *testing.T) {
	pod := func(name, ownerKind, owner string, phase v1.PodPhase) *v1.Pod {
		p := &v1.Pod{}
		p.Name, p.Namespace = name, "n"
		controller := true
		p.OwnerReferences = []metav1.OwnerReference{{Kind: ownerKind, Name: owner, Controller: &controller}}
		p.Status.Phase = phase
		p.Spec.Containers = []v1.Container{{Name: "c"}}
		return p
	}
	ws := liveWorkloads([]*v1.Pod{
		pod("init-abcde", "Job", "init", v1.PodSucceeded),                   // finished one-off: nothing to resize
		pod("batch-fghjk", "Job", "batch", v1.PodRunning),                   // running one-off: kept
		pod("sync-29000000-xyzab", "Job", "sync-29000000", v1.PodSucceeded), // a CronJob's run: kept
	}, false)
	got := map[string]bool{}
	for _, w := range ws {
		got[w.ref.Kind+"/"+w.ref.Name] = true
	}
	if got["Job/init"] || !got["Job/batch"] || len(ws) != 2 {
		t.Fatalf("workloads %v", got)
	}
}

// A CronJob run lasts minutes: all of it falls in what would be a service's
// startup window, and all of it is the work.
func TestAlignPooledKeepsJobStartup(t *testing.T) {
	g := newGrid(time.Unix(100000, 0), 2*time.Hour, 5*time.Minute)
	start := g.end.Add(-5 * time.Minute).Unix()
	k := seriesKey{"n", "", "backup", "c"}
	res := seriesSet{{{Labels: map[string]string{"namespace": "n", "pod": "backup-29000000-abcde", "wk": "backup", "container": "c"},
		Times: []int64{start, start + 300}, Values: []float32{1, 2}}}}
	cpu, _, _, _ := alignPooled(res, g, map[string]int64{"n/backup-29000000-abcde": start}, nil, map[seriesKey]struct{}{k: {}})
	if len(cpu[k].vals) != 2 {
		t.Fatalf("kept %d samples of a job's run, want both", len(cpu[k].vals))
	}
}

// The running version starts when a pod carrying its template hash first
// reported, not when today's pods happened to be created.
func TestVersionSinceFromHistory(t *testing.T) {
	first := map[string]int64{
		"api-6d4f8b9c5-x2x4z":                         100, // previous version
		"api-7f9c6d8b4-k2lmn":                         500,
		"api-7f9c6d8b4-p9qrs-x-apps-x-beige-vcluster": 400, // same version, in a vcluster
	}
	if got := versionSince("7f9c6d8b4", first); got.Unix() != 400 {
		t.Fatalf("version since %v", got.Unix())
	}
	if !versionSince("", first).IsZero() || !versionSince("deadbeef", first).IsZero() {
		t.Fatal("unknown hash must give no time")
	}
}
