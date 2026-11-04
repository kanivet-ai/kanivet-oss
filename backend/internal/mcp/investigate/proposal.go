package investigate

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"sigs.k8s.io/yaml"
)

// Proposal is a live-cluster patch the agent proposed. Kanivet dry-runs and
// applies it itself, only when the user clicks; the agent never writes.
type Proposal struct {
	ID         string          `json:"id"`
	Summary    string          `json:"summary,omitempty"`
	Kind       string          `json:"kind"`
	APIVersion string          `json:"apiVersion,omitempty"`
	Namespace  string          `json:"namespace,omitempty"`
	Name       string          `json:"name"`
	PatchType  string          `json:"patchType,omitempty"` // merge | strategic | json
	Patch      json.RawMessage `json:"patch"`
	// Status: proposed | dry-run-ok | dry-run-failed | applied | apply-failed | rejected
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
	Before string `json:"before,omitempty"` // redacted YAML
	After  string `json:"after,omitempty"`  // redacted YAML (dry-run result)
}

// ClusterOps is what the chat needs from Kanivet's cluster layer
// (implemented by mcp.Service).
type ClusterOps interface {
	FetchRedacted(ctx context.Context, cluster, kind, namespace, name string) (map[string]any, error)
	PatchLive(ctx context.Context, cluster, kind, namespace, name, patchType string, patch []byte, dryRun bool) (before, after map[string]any, err error)
	Scrub(text string) string
}

var patchFence = regexp.MustCompile("(?s)```kanivet-patch\\s*(\\{.*?\\})\\s*```")

// ExtractProposals finds kanivet-patch blocks in an answer.
func ExtractProposals(text string) []Proposal {
	var out []Proposal
	for i, m := range patchFence.FindAllStringSubmatch(text, -1) {
		var p Proposal
		if json.Unmarshal([]byte(m[1]), &p) != nil || p.Kind == "" || p.Name == "" || len(p.Patch) == 0 {
			continue
		}
		p.ID = "p" + itoa(i+1)
		p.Status = "proposed"
		out = append(out, p)
	}
	return out
}

func itoa(i int) string {
	b, _ := json.Marshal(i)
	return string(b)
}

func toYAML(m map[string]any) string {
	if m == nil {
		return ""
	}
	if md, ok := m["metadata"].(map[string]any); ok {
		delete(md, "managedFields")
		delete(md, "resourceVersion")
		delete(md, "generation")
	}
	b, err := yaml.Marshal(m)
	if err != nil {
		return ""
	}
	return string(b)
}

// DryRun runs the proposal server-side without persisting.
func DryRun(ctx context.Context, ops ClusterOps, cluster string, p *Proposal) {
	before, after, err := ops.PatchLive(ctx, cluster, p.Kind, p.Namespace, p.Name, p.PatchType, p.Patch, true)
	p.Before = toYAML(before)
	if err != nil {
		p.Status, p.Error, p.After = "dry-run-failed", err.Error(), ""
		return
	}
	p.After, p.Status, p.Error = toYAML(after), "dry-run-ok", ""
}

// ErrNeedsDryRun is returned when applying a proposal that has no
// successful dry-run.
var ErrNeedsDryRun = errors.New("run a dry-run first; the patch must pass it before it can be applied")

// Apply patches the live object. Callers must only reach this from an
// explicit user approval.
func Apply(ctx context.Context, ops ClusterOps, cluster string, p *Proposal) error {
	if p.Status != "dry-run-ok" {
		return ErrNeedsDryRun
	}
	_, after, err := ops.PatchLive(ctx, cluster, p.Kind, p.Namespace, p.Name, p.PatchType, p.Patch, false)
	if err != nil {
		p.Status, p.Error = "apply-failed", err.Error()
		return err
	}
	p.Status, p.Error = "applied", ""
	if a := toYAML(after); a != "" {
		p.After = a
	}
	return nil
}

// stripProposals removes kanivet-patch blocks from display text.
func stripProposals(s string) string {
	return strings.TrimSpace(patchFence.ReplaceAllString(s, ""))
}
