package compare

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"reflect"
	"strings"
)

const lastAppliedAnnotation = "kubectl.kubernetes.io/last-applied-configuration"

// Metadata the server owns. Stripped unless Options.IncludeMetadata.
var serverMetadata = []string{
	"managedFields", "resourceVersion", "uid", "creationTimestamp",
	"generation", "selfLink", "ownerReferences", "deletionGracePeriodSeconds",
}

// Annotations and labels the server or a controller writes.
var serverAnnotations = []string{
	lastAppliedAnnotation,
	"deployment.kubernetes.io/revision",
	"deprecated.daemonset.template.generation",
	"meta.helm.sh/release-name",
	"meta.helm.sh/release-namespace",
}

var serverLabels = []string{"pod-template-hash", "controller-revision-hash"}

// hashKey keys the Secret value hashes. It is random per process, so two
// values can be compared within a session but a hash cannot be checked
// against a guessed value offline.
var hashKey = func() []byte {
	k := make([]byte, 32)
	_, _ = rand.Read(k)
	return k
}()

// RedactedValue is how a Secret value appears in a comparison.
func RedactedValue(raw []byte) string {
	m := hmac.New(sha256.New, hashKey)
	m.Write(raw)
	return fmt.Sprintf("<redacted hash:%s, %d bytes>", hex.EncodeToString(m.Sum(nil))[:12], len(raw))
}

// RedactSecret replaces every value of a Secret's data and stringData with a
// keyed hash of its decoded bytes, so equal values compare equal and key sets
// stay visible, but no value leaves the backend.
func RedactSecret(obj map[string]any) bool {
	if kind, _ := obj["kind"].(string); kind != "Secret" {
		return false
	}
	for _, field := range []string{"data", "stringData"} {
		m, ok := obj[field].(map[string]any)
		if !ok {
			continue
		}
		for k, v := range m {
			s := fmt.Sprint(v)
			raw := []byte(s)
			if field == "data" {
				if dec, err := base64.StdEncoding.DecodeString(s); err == nil {
					raw = dec
				}
			}
			m[k] = RedactedValue(raw)
		}
	}
	// stringData is write-only sugar for data: fold it in so a manifest that
	// uses stringData compares with the live object, which never has it.
	if sd, ok := obj["stringData"].(map[string]any); ok {
		d, _ := obj["data"].(map[string]any)
		if d == nil {
			d = map[string]any{}
		}
		for k, v := range sd {
			d[k] = v
		}
		obj["data"] = d
		delete(obj, "stringData")
	}
	return true
}

// Normalize strips what would make every comparison noisy and returns notes
// on what it did. obj is modified in place.
func Normalize(obj map[string]any, opts Options) []string {
	var notes []string
	if RedactSecret(obj) {
		notes = append(notes, "Secret values are hidden; equal hashes mean equal values.")
	}
	if md, ok := obj["metadata"].(map[string]any); ok {
		// last-applied may embed a Secret's data verbatim, so it is always removed.
		if ann, ok := md["annotations"].(map[string]any); ok {
			delete(ann, lastAppliedAnnotation)
		}
		if !opts.IncludeMetadata {
			for _, f := range serverMetadata {
				delete(md, f)
			}
			if ann, ok := md["annotations"].(map[string]any); ok {
				for _, a := range serverAnnotations {
					delete(ann, a)
				}
			}
			if lbl, ok := md["labels"].(map[string]any); ok {
				for _, l := range serverLabels {
					delete(lbl, l)
				}
			}
		}
		for _, f := range []string{"annotations", "labels"} {
			if m, ok := md[f].(map[string]any); ok && len(m) == 0 {
				delete(md, f)
			}
		}
	}
	if !opts.IncludeStatus {
		if _, ok := obj["status"]; ok {
			delete(obj, "status")
		}
	}
	if !opts.KeepDefaults {
		stripDefaults(obj)
	}
	return notes
}

// --- server defaults -------------------------------------------------------

func dropIfEq(m map[string]any, key string, def any) {
	if v, ok := m[key]; ok && looseEqual(v, def) {
		delete(m, key)
	}
}

func dropIfEmpty(m map[string]any, key string) {
	switch v := m[key].(type) {
	case map[string]any:
		if len(v) == 0 {
			delete(m, key)
		}
	case []any:
		if len(v) == 0 {
			delete(m, key)
		}
	}
}

// looseEqual compares JSON-ish values, treating int64/float64/int alike.
func looseEqual(a, b any) bool {
	if fa, ok := toFloat(a); ok {
		if fb, ok := toFloat(b); ok {
			return fa == fb
		}
	}
	return reflect.DeepEqual(a, b)
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, true
	case float32:
		return float64(n), true
	}
	return 0, false
}

func mapAt(obj map[string]any, path ...string) map[string]any {
	cur := obj
	for _, p := range path {
		next, ok := cur[p].(map[string]any)
		if !ok {
			return nil
		}
		cur = next
	}
	return cur
}

// PodSpecPath is where a kind keeps its pod spec.
func PodSpecPath(kind string) []string {
	switch kind {
	case "Pod":
		return []string{"spec"}
	case "CronJob":
		return []string{"spec", "jobTemplate", "spec", "template", "spec"}
	case "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Job", "ReplicationController":
		return []string{"spec", "template", "spec"}
	}
	return nil
}

func stripDefaults(obj map[string]any) {
	kind, _ := obj["kind"].(string)
	spec, _ := obj["spec"].(map[string]any)

	if p := PodSpecPath(kind); p != nil {
		if ps := mapAt(obj, p...); ps != nil {
			stripPodSpecDefaults(ps)
		}
		if tmd := mapAt(obj, p[:len(p)-1]...); tmd != nil && len(p) > 1 {
			if md, ok := tmd["metadata"].(map[string]any); ok {
				delete(md, "creationTimestamp")
				if lbl, ok := md["labels"].(map[string]any); ok {
					for _, l := range serverLabels {
						delete(lbl, l)
					}
				}
				dropIfEmpty(md, "labels")
				if len(md) == 0 {
					delete(tmd, "metadata")
				}
			}
		}
	}
	if spec == nil {
		return
	}
	switch kind {
	case "Deployment":
		dropIfEq(spec, "progressDeadlineSeconds", 600)
		dropIfEq(spec, "revisionHistoryLimit", 10)
		dropIfEq(spec, "strategy", map[string]any{"type": "RollingUpdate", "rollingUpdate": map[string]any{"maxSurge": "25%", "maxUnavailable": "25%"}})
	case "StatefulSet":
		dropIfEq(spec, "revisionHistoryLimit", 10)
		dropIfEq(spec, "podManagementPolicy", "OrderedReady")
		dropIfEq(spec, "updateStrategy", map[string]any{"type": "RollingUpdate", "rollingUpdate": map[string]any{"partition": 0}})
		dropIfEq(spec, "persistentVolumeClaimRetentionPolicy", map[string]any{"whenDeleted": "Retain", "whenScaled": "Retain"})
	case "DaemonSet":
		dropIfEq(spec, "revisionHistoryLimit", 10)
		dropIfEq(spec, "updateStrategy", map[string]any{"type": "RollingUpdate", "rollingUpdate": map[string]any{"maxSurge": 0, "maxUnavailable": 1}})
	case "CronJob":
		dropIfEq(spec, "concurrencyPolicy", "Allow")
		dropIfEq(spec, "successfulJobsHistoryLimit", 3)
		dropIfEq(spec, "failedJobsHistoryLimit", 1)
		dropIfEq(spec, "suspend", false)
	case "Service":
		// Assigned by the cluster: different on every cluster, never declared.
		delete(spec, "clusterIP")
		delete(spec, "clusterIPs")
		dropIfEq(spec, "sessionAffinity", "None")
		dropIfEq(spec, "internalTrafficPolicy", "Cluster")
		dropIfEq(spec, "ipFamilyPolicy", "SingleStack")
		dropIfEq(spec, "ipFamilies", []any{"IPv4"})
		dropIfEq(spec, "type", "ClusterIP")
		if ports, ok := spec["ports"].([]any); ok {
			for _, p := range ports {
				if pm, ok := p.(map[string]any); ok {
					dropIfEq(pm, "protocol", "TCP")
				}
			}
		}
	}
}

func stripPodSpecDefaults(ps map[string]any) {
	dropIfEq(ps, "dnsPolicy", "ClusterFirst")
	dropIfEq(ps, "restartPolicy", "Always")
	dropIfEq(ps, "schedulerName", "default-scheduler")
	dropIfEq(ps, "terminationGracePeriodSeconds", 30)
	dropIfEq(ps, "enableServiceLinks", true)
	dropIfEmpty(ps, "securityContext")
	// serviceAccount is the deprecated alias the server mirrors from serviceAccountName.
	if ps["serviceAccount"] != nil && looseEqual(ps["serviceAccount"], ps["serviceAccountName"]) {
		delete(ps, "serviceAccount")
	}
	for _, list := range []string{"containers", "initContainers", "ephemeralContainers"} {
		cs, _ := ps[list].([]any)
		for _, c := range cs {
			cm, ok := c.(map[string]any)
			if !ok {
				continue
			}
			dropIfEq(cm, "terminationMessagePath", "/dev/termination-log")
			dropIfEq(cm, "terminationMessagePolicy", "File")
			dropIfEmpty(cm, "resources")
			if ports, ok := cm["ports"].([]any); ok {
				for _, p := range ports {
					if pm, ok := p.(map[string]any); ok {
						dropIfEq(pm, "protocol", "TCP")
					}
				}
			}
			for _, probe := range []string{"livenessProbe", "readinessProbe", "startupProbe"} {
				if pm, ok := cm[probe].(map[string]any); ok {
					dropIfEq(pm, "timeoutSeconds", 1)
					dropIfEq(pm, "periodSeconds", 10)
					dropIfEq(pm, "successThreshold", 1)
					dropIfEq(pm, "failureThreshold", 3)
					if hg, ok := pm["httpGet"].(map[string]any); ok {
						dropIfEq(hg, "scheme", "HTTP")
					}
				}
			}
		}
	}
	if vols, ok := ps["volumes"].([]any); ok {
		for _, v := range vols {
			vm, ok := v.(map[string]any)
			if !ok {
				continue
			}
			for _, src := range []string{"secret", "configMap", "projected", "downwardAPI"} {
				if sm, ok := vm[src].(map[string]any); ok {
					dropIfEq(sm, "defaultMode", 420)
				}
			}
		}
	}
}

// --- declared-only pruning -------------------------------------------------

// PruneUndeclared returns live restricted to the map keys present in
// declared. List items are matched by name (or index) and pruned
// recursively, but extra items are kept: an extra container is real drift,
// while a field the server filled in is not.
func PruneUndeclared(live, declared any) any {
	switch d := declared.(type) {
	case map[string]any:
		l, ok := live.(map[string]any)
		if !ok {
			return live
		}
		out := make(map[string]any, len(d))
		for k, dv := range d {
			if lv, ok := l[k]; ok {
				out[k] = PruneUndeclared(lv, dv)
			}
		}
		return out
	case []any:
		l, ok := live.([]any)
		if !ok {
			return live
		}
		if dk, ok := namedItems(d); ok {
			if lk, ok := namedItems(l); ok {
				out := make([]any, 0, len(l))
				for _, item := range l {
					name := itemName(item)
					if dv, ok := dk[name]; ok {
						out = append(out, PruneUndeclared(item, dv))
					} else {
						out = append(out, item)
					}
				}
				_ = lk
				return out
			}
		}
		out := make([]any, len(l))
		for i, item := range l {
			if i < len(d) {
				out[i] = PruneUndeclared(item, d[i])
			} else {
				out[i] = item
			}
		}
		return out
	}
	return live
}

func itemName(v any) string {
	m, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	for _, k := range []string{"name", "key", "mountPath", "containerPort"} {
		if s, ok := m[k]; ok && s != nil {
			return fmt.Sprint(s)
		}
	}
	return ""
}

// namedItems indexes a list by item name when every item has a distinct one.
func namedItems(list []any) (map[string]any, bool) {
	if len(list) == 0 {
		return nil, false
	}
	out := make(map[string]any, len(list))
	for _, item := range list {
		n := itemName(item)
		if n == "" {
			return nil, false
		}
		if _, dup := out[n]; dup {
			return nil, false
		}
		out[n] = item
	}
	return out, true
}

func isIdentKey(k string) bool {
	return k != "" && !strings.ContainsAny(k, "./[]\" ")
}
