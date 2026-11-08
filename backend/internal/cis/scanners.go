package cis

import (
	"bufio"
	"regexp"
	"sort"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Scanner sources.
const (
	SourceBuiltIn   = "built-in"
	SourceTrivy     = "trivy-operator"
	SourceKubescape = "kubescape"
	SourceKubeBench = "kube-bench"
)

// ScannerReport is one scanner report reduced to per-control verdicts.
type ScannerReport struct {
	Source string
	Report string
	// Profile is the benchmark the report covers ("" when unknown; it is
	// then merged into whatever profile is shown).
	Profile   string
	ScannedAt time.Time
	Controls  map[string]ScannerFinding
}

// trivyProfile maps a Trivy compliance spec id to our profile.
func trivyProfile(spec string) string {
	s := strings.ToLower(spec)
	switch {
	case strings.HasPrefix(s, "eks-cis"):
		return ProfileEKS
	case strings.HasPrefix(s, "gke-cis"):
		return ProfileGKE
	case strings.HasPrefix(s, "aks-cis"):
		return ProfileAKS
	case strings.HasPrefix(s, "k3s-cis"):
		return ProfileK3s
	case strings.HasPrefix(s, "rke2-cis"):
		return ProfileRKE2
	case strings.HasPrefix(s, "k8s-cis"), strings.HasPrefix(s, "cis"):
		return ProfileK8s
	}
	return ""
}

// ParseTrivyCompliance reads a ClusterComplianceReport. Only CIS specs are
// kept (nsa, pss reports are other frameworks). Both the summary and the
// detailed report formats are read; a control without a fail count (manual
// in Trivy) is skipped.
func ParseTrivyCompliance(u *unstructured.Unstructured) (ScannerReport, bool) {
	spec, _, _ := unstructured.NestedString(u.Object, "spec", "compliance", "id")
	if spec == "" {
		spec = u.GetName()
	}
	prof := trivyProfile(spec)
	if prof == "" {
		return ScannerReport{}, false
	}
	rep := ScannerReport{Source: SourceTrivy, Report: u.GetName() + " (" + spec + ")", Profile: prof, Controls: map[string]ScannerFinding{}}
	if ts, ok, _ := unstructured.NestedString(u.Object, "status", "updateTimestamp"); ok {
		rep.ScannedAt, _ = time.Parse(time.RFC3339, ts)
	}
	add := func(id string, failed int64, known bool, name string) {
		if id == "" || !known {
			return
		}
		f := ScannerFinding{Source: SourceTrivy, Report: rep.Report, ScannedAt: rep.ScannedAt, Status: StatusPass}
		if failed > 0 {
			f.Status = StatusFail
			f.Detail = itoa(int(failed)) + " failing resource(s)"
		}
		if name != "" && f.Detail == "" {
			f.Detail = name
		}
		rep.Controls[id] = f
	}
	if checks, ok, _ := unstructured.NestedSlice(u.Object, "status", "summaryReport", "controlCheck"); ok {
		for _, x := range checks {
			m, _ := x.(map[string]any)
			id, _ := m["id"].(string)
			name, _ := m["name"].(string)
			tf, known := asInt(m["totalFail"])
			add(id, tf, known, name)
		}
	}
	if results, ok, _ := unstructured.NestedSlice(u.Object, "status", "detailReport", "results"); ok {
		for _, x := range results {
			m, _ := x.(map[string]any)
			id, _ := m["id"].(string)
			name, _ := m["name"].(string)
			cs, _ := m["checks"].([]any)
			if len(cs) == 0 {
				continue
			}
			var failed int64
			for _, ch := range cs {
				cm, _ := ch.(map[string]any)
				if ok, _ := cm["success"].(bool); !ok {
					failed++
				}
			}
			if _, seen := rep.Controls[id]; !seen {
				add(id, failed, true, name)
			}
		}
	}
	return rep, len(rep.Controls) > 0
}

func asInt(v any) (int64, bool) {
	switch t := v.(type) {
	case int64:
		return t, true
	case int:
		return int64(t), true
	case float64:
		return int64(t), true
	}
	return 0, false
}

var reKubescapeCIS = regexp.MustCompile(`^CIS-(\d+(?:\.\d+)+)$`)

// ParseKubescapeScans folds Kubescape WorkloadConfigurationScan objects
// into CIS verdicts. Only controls whose id is a CIS number ("CIS-5.1.3",
// from the cis-v1.x frameworks) are kept; a control fails if any scanned
// object fails it.
func ParseKubescapeScans(items []unstructured.Unstructured) (ScannerReport, bool) {
	rep := ScannerReport{Source: SourceKubescape, Report: "workloadconfigurationscans", Controls: map[string]ScannerFinding{}}
	failed := map[string]int{}
	for _, it := range items {
		if t := it.GetCreationTimestamp().Time; t.After(rep.ScannedAt) {
			rep.ScannedAt = t
		}
		ctrls, _, _ := unstructured.NestedMap(it.Object, "spec", "controls")
		for key, v := range ctrls {
			m, _ := v.(map[string]any)
			id, _ := m["controlID"].(string)
			if id == "" {
				id = key
			}
			mm := reKubescapeCIS.FindStringSubmatch(id)
			if mm == nil {
				continue
			}
			st, _, _ := unstructured.NestedString(m, "status", "status")
			switch st {
			case "failed":
				failed[mm[1]]++
			case "passed":
				if _, ok := failed[mm[1]]; !ok {
					failed[mm[1]] = 0
				}
			}
		}
	}
	for id, n := range failed {
		f := ScannerFinding{Source: SourceKubescape, Report: rep.Report, ScannedAt: rep.ScannedAt, Status: StatusPass}
		if n > 0 {
			f.Status, f.Detail = StatusFail, itoa(n)+" failing object(s)"
		}
		rep.Controls[id] = f
	}
	return rep, len(rep.Controls) > 0
}

var reBenchLine = regexp.MustCompile(`^\[(PASS|FAIL|WARN|INFO)\]\s+(\d+(?:\.\d+)+)\s+(.*)$`)

// ParseKubeBench reads kube-bench's text output. PASS and FAIL are
// verdicts; WARN means kube-bench could not decide (manual).
func ParseKubeBench(logs string) map[string]ScannerFinding {
	out := map[string]ScannerFinding{}
	sc := bufio.NewScanner(strings.NewReader(logs))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		m := reBenchLine.FindStringSubmatch(strings.TrimSpace(sc.Text()))
		if m == nil || m[1] == "INFO" {
			continue
		}
		// Section headers are "[INFO] 1.1 ..."; check lines have 3+ parts
		// except etcd's "2.1".
		f := ScannerFinding{Source: SourceKubeBench, Detail: strings.TrimSpace(m[3])}
		switch m[1] {
		case "PASS":
			f.Status = StatusPass
		case "FAIL":
			f.Status = StatusFail
		case "WARN":
			f.Status = StatusManual
		}
		if prev, ok := out[m[2]]; ok && prev.Status == StatusFail {
			continue // several node targets: any fail wins
		}
		out[m[2]] = f
	}
	return out
}

// mergeScanners applies scanner verdicts to built-in results. A scanner
// fills a control Kanivet could not decide (manual, not-visible, error);
// where both decided and disagree, ours stays and the disagreement shows.
func mergeScanners(results []ControlResult, reps []ScannerReport, profile string) []ScannerSource {
	var sources []ScannerSource
	byID := map[string]int{}
	for i, r := range results {
		byID[r.ID] = i
	}
	sorted := append([]ScannerReport(nil), reps...)
	// Newest first, so the freshest verdict fills a control.
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].ScannedAt.After(sorted[j].ScannedAt) })
	for _, rep := range sorted {
		used := rep.Profile == "" || rep.Profile == profile
		sources = append(sources, ScannerSource{Source: rep.Source, Report: rep.Report, Profile: rep.Profile, Controls: len(rep.Controls), ScannedAt: rep.ScannedAt, Used: used})
		if !used {
			continue
		}
		for id, f := range rep.Controls {
			i, ok := byID[id]
			if !ok {
				continue
			}
			r := &results[i]
			r.Scanners = append(r.Scanners, f)
			if !f.Status.Automated() {
				continue
			}
			switch {
			case r.Source == SourceBuiltIn && (r.Status == StatusManual || r.Status == StatusNotVisible || r.Status == StatusError):
				r.Status = f.Status
				r.Source = f.Source
			case r.Status.Automated() && r.Status != f.Status:
				r.Disagreement = true
			}
		}
	}
	return sources
}
