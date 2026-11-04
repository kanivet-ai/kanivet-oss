package mcp

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Token efficiency: what an agent needs from an object is its spec and status,
// not the bookkeeping the API server adds. Minify drops that bookkeeping,
// defaulted fields and empty values, and Redact removes secret material.

var droppedMetadata = []string{"managedFields", "selfLink", "resourceVersion", "generation", "uid"}

var droppedAnnotations = []string{
	"kubectl.kubernetes.io/last-applied-configuration",
	"deployment.kubernetes.io/revision-history",
}

// Pod-spec fields whose value is the default; showing them costs tokens and
// says nothing.
var defaultPodFields = map[string]any{
	"dnsPolicy":                     "ClusterFirst",
	"schedulerName":                 "default-scheduler",
	"restartPolicy":                 "Always",
	"terminationGracePeriodSeconds": float64(30),
	"enableServiceLinks":            true,
	"preemptionPolicy":              "PreemptLowerPriority",
	"priority":                      float64(0),
}

var defaultContainerFields = map[string]any{
	"terminationMessagePath":   "/dev/termination-log",
	"terminationMessagePolicy": "File",
}

// Minify returns a trimmed deep copy of a Kubernetes object.
func Minify(obj map[string]any) map[string]any {
	out, _ := deepCopy(obj).(map[string]any)
	if out == nil {
		return map[string]any{}
	}
	if md, ok := out["metadata"].(map[string]any); ok {
		for _, k := range droppedMetadata {
			delete(md, k)
		}
		if ann, ok := md["annotations"].(map[string]any); ok {
			for _, k := range droppedAnnotations {
				delete(ann, k)
			}
		}
	}
	if spec, ok := out["spec"].(map[string]any); ok {
		trimPodSpec(spec)
		if tpl, ok := spec["template"].(map[string]any); ok {
			if ts, ok := tpl["spec"].(map[string]any); ok {
				trimPodSpec(ts)
			}
		}
	}
	if st, ok := out["status"].(map[string]any); ok {
		if conds, ok := st["conditions"].([]any); ok {
			for _, c := range conds {
				if cm, ok := c.(map[string]any); ok {
					delete(cm, "lastProbeTime")
					delete(cm, "lastHeartbeatTime")
				}
			}
		}
		// Nodes report every image they hold; that list is thousands of tokens.
		delete(st, "images")
	}
	pruned, _ := prune(out).(map[string]any)
	if pruned == nil {
		return map[string]any{}
	}
	return pruned
}

func trimPodSpec(spec map[string]any) {
	if _, isPod := spec["containers"]; !isPod {
		return
	}
	for k, v := range defaultPodFields {
		if spec[k] == v {
			delete(spec, k)
		}
	}
	if spec["serviceAccount"] == spec["serviceAccountName"] {
		delete(spec, "serviceAccount")
	}
	if tols, ok := spec["tolerations"].([]any); ok {
		kept := tols[:0]
		for _, t := range tols {
			tm, _ := t.(map[string]any)
			key, _ := tm["key"].(string)
			if key == "node.kubernetes.io/not-ready" || key == "node.kubernetes.io/unreachable" {
				continue
			}
			kept = append(kept, t)
		}
		spec["tolerations"] = kept
	}
	for _, field := range []string{"containers", "initContainers"} {
		cs, _ := spec[field].([]any)
		for _, c := range cs {
			cm, ok := c.(map[string]any)
			if !ok {
				continue
			}
			for k, v := range defaultContainerFields {
				if cm[k] == v {
					delete(cm, k)
				}
			}
		}
	}
}

// prune removes nil, empty strings, empty maps and empty slices recursively.
func prune(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			p := prune(val)
			if isEmpty(p) {
				delete(t, k)
			} else {
				t[k] = p
			}
		}
		return t
	case []any:
		out := make([]any, 0, len(t))
		for _, val := range t {
			p := prune(val)
			if !isEmpty(p) {
				out = append(out, p)
			}
		}
		return out
	}
	return v
}

func isEmpty(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return t == ""
	case map[string]any:
		return len(t) == 0
	case []any:
		return len(t) == 0
	}
	return false
}

func deepCopy(v any) any {
	switch t := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, val := range t {
			m[k] = deepCopy(val)
		}
		return m
	case []any:
		s := make([]any, len(t))
		for i, val := range t {
			s[i] = deepCopy(val)
		}
		return s
	case []map[string]any:
		s := make([]any, len(t))
		for i, val := range t {
			s[i] = deepCopy(val)
		}
		return s
	case map[string]string:
		m := make(map[string]any, len(t))
		for k, val := range t {
			m[k] = val
		}
		return m
	}
	return v
}

// ---------------------------------------------------------------------------
// Redaction

var sensitiveName = regexp.MustCompile(`(?i)(pass(word|wd)?|secret|token|api[_-]?key|access[_-]?key|private[_-]?key|credential|auth|session|cookie|signature|dsn|conn(ection)?[_-]?str)`)

// Redact removes secret material from an object, in place on a copy:
// Secret data and stringData values, env values whose names look sensitive,
// and secret-looking substrings anywhere else.
func Redact(obj map[string]any) map[string]any {
	out, _ := deepCopy(obj).(map[string]any)
	if out == nil {
		return map[string]any{}
	}
	if kind, _ := out["kind"].(string); kind == "Secret" {
		for _, f := range []string{"data", "stringData"} {
			if d, ok := out[f].(map[string]any); ok {
				for k, v := range d {
					s, _ := v.(string)
					d[k] = fmt.Sprintf("<redacted %d bytes>", len(s))
				}
			}
		}
	}
	redactWalk(out)
	return out
}

func redactWalk(v any) {
	switch t := v.(type) {
	case map[string]any:
		// Container env entries: {name: X, value: Y}
		if name, ok := t["name"].(string); ok {
			if val, ok := t["value"].(string); ok && val != "" && sensitiveName.MatchString(name) {
				t["value"] = "<redacted>"
			}
		}
		for k, val := range t {
			if s, ok := val.(string); ok {
				if strings.HasPrefix(s, "<redacted") {
					continue
				}
				t[k] = ScrubText(s)
				continue
			}
			redactWalk(val)
		}
	case []any:
		for i, val := range t {
			if s, ok := val.(string); ok {
				t[i] = ScrubText(s)
				continue
			}
			redactWalk(val)
		}
	}
}

type scrubRule struct {
	re   *regexp.Regexp
	repl string
}

var scrubRules = []scrubRule{
	{regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?(-----END [A-Z ]*PRIVATE KEY-----|$)`), "<redacted private key>"},
	{regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`), "<redacted jwt>"},
	{regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{12,}`), "$1 <redacted>"},
	{regexp.MustCompile(`\b(AKIA|ASIA)[0-9A-Z]{16}\b`), "<redacted aws key>"},
	{regexp.MustCompile(`\b(ghp|gho|ghu|ghs|ghr|github_pat)_[A-Za-z0-9_]{20,}\b`), "<redacted github token>"},
	{regexp.MustCompile(`\bxox[abposr]-[A-Za-z0-9-]{10,}\b`), "<redacted slack token>"},
	{regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}\b`), "<redacted api key>"},
	{regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`), "<redacted google key>"},
	// scheme://user:password@host
	{regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://[^\s:/@]+):[^\s@/]+@`), "$1:<redacted>@"},
	// key=value / key: value / "key":"value" with a sensitive key name
	{regexp.MustCompile(`(?i)("?\b[\w.-]*(?:password|passwd|secret|token|api[_-]?key|access[_-]?key|client[_-]?secret)[\w.-]*"?\s*[:=]\s*)("[^"]*"|'[^']*'|[^\s,;&"'}]+)`), "$1<redacted>"},
}

// ScrubText replaces secret-looking substrings (keys, tokens, passwords in
// URLs or key=value pairs) in free text such as logs and event messages.
func ScrubText(s string) string {
	if s == "" {
		return s
	}
	for _, r := range scrubRules {
		s = r.re.ReplaceAllString(s, r.repl)
	}
	return s
}

// ---------------------------------------------------------------------------
// List rows

// Row is the one-line summary list_resources returns per object.
type Row struct {
	Name      string `json:"name"`
	Namespace string `json:"ns,omitempty"`
	Status    string `json:"status,omitempty"`
	Ready     string `json:"ready,omitempty"`
	Restarts  int64  `json:"restarts,omitempty"`
	Node      string `json:"node,omitempty"`
	Age       string `json:"age,omitempty"`
	Info      string `json:"info,omitempty"`
}

// Summarize builds a Row for any object, with richer status for common kinds.
func Summarize(obj map[string]any, now time.Time) Row {
	md, _ := obj["metadata"].(map[string]any)
	r := Row{Name: str(md, "name"), Namespace: str(md, "namespace")}
	if ts := str(md, "creationTimestamp"); ts != "" {
		if t, err := time.Parse(time.RFC3339, ts); err == nil {
			r.Age = shortAge(now.Sub(t))
		}
	}
	if md != nil && md["deletionTimestamp"] != nil {
		r.Status = "Terminating"
	}
	spec, _ := obj["spec"].(map[string]any)
	status, _ := obj["status"].(map[string]any)
	kind, _ := obj["kind"].(string)
	switch kind {
	case "Pod":
		if r.Status == "" {
			r.Status = podStatus(status)
		}
		ready, total, restarts := podContainers(status)
		r.Ready = fmt.Sprintf("%d/%d", ready, total)
		r.Restarts = restarts
		r.Node = str(spec, "nodeName")
	case "Deployment", "StatefulSet", "ReplicaSet":
		want := num(spec, "replicas")
		r.Ready = fmt.Sprintf("%d/%d", num(status, "readyReplicas"), want)
		if r.Status == "" {
			switch {
			case want == 0:
				r.Status = "ScaledDown"
			case num(status, "readyReplicas") >= want && num(status, "updatedReplicas") >= want:
				r.Status = "Ready"
			default:
				r.Status = "Progressing"
			}
		}
	case "DaemonSet":
		want := num(status, "desiredNumberScheduled")
		r.Ready = fmt.Sprintf("%d/%d", num(status, "numberReady"), want)
	case "Job":
		r.Ready = fmt.Sprintf("%d/%d", num(status, "succeeded"), maxInt(num(spec, "completions"), 1))
		if r.Status == "" {
			r.Status = conditionTrue(status, "Complete", "Failed", "SuccessCriteriaMet", "Suspended")
		}
	case "Service":
		r.Info = strings.TrimSpace(str(spec, "type") + " " + str(spec, "clusterIP"))
	case "Node":
		if r.Status == "" {
			if conditionIs(status, "Ready", "True") {
				r.Status = "Ready"
			} else {
				r.Status = "NotReady"
			}
			if b, _ := spec["unschedulable"].(bool); b {
				r.Status += ",SchedulingDisabled"
			}
		}
		if ni, ok := status["nodeInfo"].(map[string]any); ok {
			r.Info = str(ni, "kubeletVersion")
		}
	case "PersistentVolumeClaim", "PersistentVolume", "Namespace":
		r.Status = str(status, "phase")
	case "Event":
		r.Status = str(obj, "type")
		r.Info = truncateText(str(obj, "reason")+": "+str(obj, "message"), 160)
	}
	if r.Status == "" {
		if p := str(status, "phase"); p != "" {
			r.Status = p
		} else if s := conditionTrue(status, "Ready", "Available", "Synced", "Healthy"); s != "" {
			r.Status = s
		} else if h, ok := status["health"].(map[string]any); ok {
			r.Status = str(h, "status")
		}
	}
	return r
}

func podStatus(status map[string]any) string {
	cs, _ := status["containerStatuses"].([]any)
	for _, c := range cs {
		cm, _ := c.(map[string]any)
		st, _ := cm["state"].(map[string]any)
		if w, ok := st["waiting"].(map[string]any); ok && str(w, "reason") != "" {
			return str(w, "reason")
		}
		if t, ok := st["terminated"].(map[string]any); ok && str(t, "reason") != "" && str(t, "reason") != "Completed" {
			return str(t, "reason")
		}
	}
	if r := str(status, "reason"); r != "" {
		return r
	}
	return str(status, "phase")
}

func podContainers(status map[string]any) (ready, total int, restarts int64) {
	cs, _ := status["containerStatuses"].([]any)
	for _, c := range cs {
		cm, _ := c.(map[string]any)
		total++
		if b, _ := cm["ready"].(bool); b {
			ready++
		}
		restarts += int64(num(cm, "restartCount"))
	}
	return
}

func conditionTrue(status map[string]any, types ...string) string {
	for _, t := range types {
		if conditionIs(status, t, "True") {
			return t
		}
	}
	for _, t := range types {
		if conditionIs(status, t, "False") {
			return "Not" + t
		}
	}
	return ""
}

func conditionIs(status map[string]any, typ, want string) bool {
	conds, _ := status["conditions"].([]any)
	for _, c := range conds {
		cm, _ := c.(map[string]any)
		if str(cm, "type") == typ && str(cm, "status") == want {
			return true
		}
	}
	return false
}

func str(m map[string]any, k string) string {
	if m == nil {
		return ""
	}
	s, _ := m[k].(string)
	return s
}

func num(m map[string]any, k string) int {
	if m == nil {
		return 0
	}
	switch v := m[k].(type) {
	case float64:
		return int(v)
	case int64:
		return int(v)
	case int:
		return v
	case int32:
		return int(v)
	}
	return 0
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func shortAge(d time.Duration) string {
	switch {
	case d < 0:
		return "0s"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func truncateText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
