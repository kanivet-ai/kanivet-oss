package rightsizing

import (
	"fmt"
	"regexp"
	"strings"
)

// Workload identity comes from the pod name. History mostly belongs to pods
// that no longer exist, and vcluster-synced pods are all owned by the
// vcluster's Service on the host, so owner labels can't group them. Pod names
// can: controllers append a suffix from the Kubernetes "safe" alphabet, which
// has no vowels, so real name segments almost never look like one.
//
// The same rules run twice: as a label_replace chain inside PromQL, so the
// metrics store aggregates per workload before sending anything, and in Go,
// so a live pod maps onto the key its history was stored under. Both sides
// use RE2 with fully anchored patterns, so they agree by construction; the
// tests check each rule on both.

const safeChars = `[bcdfghjklmnpqrstvwxz2456789]`

// keyRule writes target from source when pattern matches the whole source.
// Rules run in order and a later match overwrites an earlier one, which is how
// label_replace chains behave.
type keyRule struct {
	target, source, pattern string
}

var keyRules = []keyRule{
	{"vp", "pod", `(.+)`},
	// vcluster host names are <pod>-x-<namespace>-x-<vcluster>, or their first
	// 52 characters plus a 10-character hex hash when the full name would be
	// too long. Only those shapes count: a Deployment named gateway-x-api is
	// not a vcluster pod.
	{"vp", "pod", `(.+?)-x-(?:.+-x-.+|(?:.*-)?[0-9a-f]{10})`},
	{"vns", "pod", `.+?-x-(.+)-x-.+`},
	{"wk", "vp", `(.+)`},
	{"wk", "vp", `(.+)-[0-9]+`},                                       // StatefulSet ordinal
	{"wk", "vp", `(.+)-` + safeChars + `{5}`},                         // DaemonSet, Job, bare ReplicaSet
	{"wk", "vp", `(.+)-[0-9]{8,}-` + safeChars + `{5}`},               // CronJob run
	{"wk", "vp", `(.+)-` + safeChars + `{6,10}-` + safeChars + `{5}`}, // Deployment
	// The API server cuts a generated name's prefix to 58 characters before
	// adding the 5 random ones, so pods of a Deployment named 47 characters or
	// more are <name>-<hash, cut short><random>, 63 characters with no dash
	// before the random part. trunc holds such a name. The key drops the
	// random part, then the hash with it where the name still has a dash
	// before it; a longer name has no hash left. A CronJob named 49
	// characters or more loses the end of its run's minutes the same way.
	{"trunc", "vp", `(.{63})`},
	{"wk", "trunc", `(.{57}[^-])` + safeChars + `{5}`},
	{"wk", "trunc", `(.+)-(?:` + safeChars + `{6,15}|[0-9]+` + safeChars + `{5})`},
}

var compiledRules = func() []*regexp.Regexp {
	out := make([]*regexp.Regexp, len(keyRules))
	for i, r := range keyRules {
		out[i] = regexp.MustCompile(`^(?:` + r.pattern + `)$`)
	}
	return out
}()

// workloadKey returns the virtual namespace (empty unless the pod is a
// vcluster-synced host pod whose name was not truncated) and the workload key
// for a pod name, exactly as the PromQL relabelling computes them.
func workloadKey(pod string) (vns, wk string) {
	labels := map[string]string{"pod": pod}
	for i, r := range keyRules {
		if m := compiledRules[i].FindStringSubmatch(labels[r.source]); m != nil {
			val := ""
			if len(m) > 1 {
				val = m[1]
			}
			labels[r.target] = val
		}
	}
	return labels["vns"], labels["wk"]
}

// relabel wraps a PromQL expression carrying a pod label so the result also
// carries vns and wk.
func relabel(expr string) string {
	for _, r := range keyRules {
		expr = fmt.Sprintf(`label_replace(%s, %q, "$1", %q, %q)`, expr, r.target, r.source, r.pattern)
	}
	return expr
}

// keyLabels are the labels history is grouped by.
const keyLabels = "namespace, vns, wk, container"

// seriesKey identifies one workload container in history.
type seriesKey struct {
	namespace, vns, wk, container string
}

func keyOf(labels map[string]string) seriesKey {
	return seriesKey{labels["namespace"], labels["vns"], labels["wk"], labels["container"]}
}

// podSelector restricts a query to pods that could belong to the given
// workloads: every pod name starts with its workload key.
func podSelector(wks []string) string {
	alts := make([]string, len(wks))
	for i, wk := range wks {
		alts[i] = regexp.QuoteMeta(wk)
	}
	return fmt.Sprintf(`pod=~"(%s).*"`, promString(strings.Join(alts, "|")))
}

// promString escapes a value for use inside a PromQL double-quoted string.
func promString(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s)
}
