package upgrade

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strconv"
	"strings"

	"sigs.k8s.io/yaml"
)

const lastAppliedAnnotation = "kubectl.kubernetes.io/last-applied-configuration"

const deprecatedMetric = "apiserver_requested_deprecated_apis"

// ParseDeprecatedRequests reads the API server's Prometheus text output and
// returns the apiserver_requested_deprecated_apis series whose value is 1.
func ParseDeprecatedRequests(raw []byte) []DeprecatedRequest {
	var out []DeprecatedRequest
	seen := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, deprecatedMetric+"{") {
			continue
		}
		end := strings.LastIndex(line, "}")
		if end < 0 {
			continue
		}
		value := strings.TrimSpace(line[end+1:])
		if f := strings.Fields(value); len(f) > 0 {
			value = f[0]
		}
		if v, err := strconv.ParseFloat(value, 64); err != nil || v <= 0 {
			continue
		}
		labels := parseLabels(line[len(deprecatedMetric)+1 : end])
		r := DeprecatedRequest{
			Group:          labels["group"],
			Version:        labels["version"],
			Resource:       labels["resource"],
			Subresource:    labels["subresource"],
			RemovedRelease: labels["removed_release"],
		}
		key := r.Group + "/" + r.Version + "/" + r.Resource + "/" + r.Subresource
		if r.Resource == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, r)
	}
	return out
}

// parseLabels reads `a="x",b="y"` with Prometheus escaping.
func parseLabels(s string) map[string]string {
	out := map[string]string{}
	for len(s) > 0 {
		eq := strings.IndexByte(s, '=')
		if eq < 0 || eq+1 >= len(s) || s[eq+1] != '"' {
			break
		}
		key := strings.TrimSpace(strings.TrimLeft(s[:eq], ","))
		i := eq + 2
		var b strings.Builder
		for i < len(s) && s[i] != '"' {
			if s[i] == '\\' && i+1 < len(s) {
				i++
				switch s[i] {
				case 'n':
					b.WriteByte('\n')
				default:
					b.WriteByte(s[i])
				}
			} else {
				b.WriteByte(s[i])
			}
			i++
		}
		out[key] = b.String()
		if i+1 > len(s) {
			break
		}
		s = s[i+1:]
	}
	return out
}

// LastAppliedAPIVersion returns the apiVersion recorded in the
// last-applied-configuration annotation, if any.
func LastAppliedAPIVersion(annotations map[string]string) string {
	raw := annotations[lastAppliedAnnotation]
	if raw == "" {
		return ""
	}
	var head struct {
		APIVersion string `json:"apiVersion"`
	}
	if err := json.Unmarshal([]byte(raw), &head); err != nil {
		return ""
	}
	return head.APIVersion
}

// ManifestObject is the identity of one document in a Helm manifest.
type ManifestObject struct {
	APIVersion string
	Kind       string
	Name       string
	Namespace  string
}

// ParseManifest splits a multi-document YAML manifest and returns the
// identity of each object, expanding List kinds. Documents that do not parse
// are skipped.
func ParseManifest(manifest string) []ManifestObject {
	var out []ManifestObject
	for _, doc := range splitYAMLDocuments(manifest) {
		var head struct {
			APIVersion string `json:"apiVersion"`
			Kind       string `json:"kind"`
			Metadata   struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
			} `json:"metadata"`
			Items []struct {
				APIVersion string `json:"apiVersion"`
				Kind       string `json:"kind"`
				Metadata   struct {
					Name      string `json:"name"`
					Namespace string `json:"namespace"`
				} `json:"metadata"`
			} `json:"items"`
		}
		if err := yaml.Unmarshal([]byte(doc), &head); err != nil {
			continue
		}
		if strings.HasSuffix(head.Kind, "List") && len(head.Items) > 0 {
			for _, it := range head.Items {
				if it.Kind != "" && it.APIVersion != "" {
					out = append(out, ManifestObject{it.APIVersion, it.Kind, it.Metadata.Name, it.Metadata.Namespace})
				}
			}
			continue
		}
		if head.Kind == "" || head.APIVersion == "" {
			continue
		}
		out = append(out, ManifestObject{head.APIVersion, head.Kind, head.Metadata.Name, head.Metadata.Namespace})
	}
	return out
}

// splitYAMLDocuments splits on lines that are exactly a document separator,
// so "---" inside a block scalar on an indented line is left alone.
func splitYAMLDocuments(s string) []string {
	var docs []string
	var cur strings.Builder
	for _, line := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		t := strings.TrimRight(line, " \t")
		if t == "---" || strings.HasPrefix(t, "--- ") {
			if strings.TrimSpace(cur.String()) != "" {
				docs = append(docs, cur.String())
			}
			cur.Reset()
			continue
		}
		cur.WriteString(line)
		cur.WriteByte('\n')
	}
	if strings.TrimSpace(cur.String()) != "" {
		docs = append(docs, cur.String())
	}
	return docs
}
