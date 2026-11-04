package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// The methods below are what the AI chat (internal/mcp/investigate) needs
// from the cluster: secret-redacted reads for prompt context, and a patch
// path that Kanivet itself runs (dry-run first, apply only on a user click).
// Agents never get these; they only see the read-only MCP endpoint.

// FetchRedacted returns the live object, minified and with secrets redacted.
func (s *Service) FetchRedacted(ctx context.Context, cluster, kind, namespace, name string) (map[string]any, error) {
	ki, err := s.deps.resolveKind(cluster, kind)
	if err != nil {
		return nil, err
	}
	obj, err := s.deps.fetch(ctx, cluster, ki, namespace, name)
	if err != nil {
		return nil, err
	}
	return Redact(Minify(obj)), nil
}

// Scrub removes secret-looking substrings from free text.
func (s *Service) Scrub(text string) string { return ScrubText(text) }

// PatchLive patches one object with patchType "merge", "strategic" or
// "json". dryRun runs it server-side without persisting. It returns the
// redacted object before and after the patch.
func (s *Service) PatchLive(ctx context.Context, cluster, kind, namespace, name, patchType string, patch []byte, dryRun bool) (before, after map[string]any, err error) {
	if s.deps.K8s == nil {
		return nil, nil, errNoK8s
	}
	ki, err := s.deps.resolveKind(cluster, kind)
	if err != nil {
		return nil, nil, err
	}
	if !json.Valid(patch) {
		return nil, nil, fmt.Errorf("patch is not valid JSON")
	}
	pt := types.MergePatchType
	switch strings.ToLower(patchType) {
	case "", "merge":
	case "strategic":
		pt = types.StrategicMergePatchType
	case "json":
		pt = types.JSONPatchType
	default:
		return nil, nil, fmt.Errorf("unsupported patch type %q", patchType)
	}
	cur, err := s.deps.fetch(ctx, cluster, ki, namespace, name)
	if err != nil {
		return nil, nil, err
	}
	dyn, err := s.deps.K8s.GetDynamicClient(cluster)
	if err != nil {
		return nil, nil, err
	}
	opts := metav1.PatchOptions{FieldManager: "kanivet-ai"}
	if dryRun {
		opts.DryRun = []string{metav1.DryRunAll}
	}
	ri := dyn.Resource(ki.GVR)
	var res interface {
		UnstructuredContent() map[string]any
	}
	if ki.Namespaced {
		if namespace == "" {
			namespace = "default"
		}
		res, err = ri.Namespace(namespace).Patch(ctx, name, pt, patch, opts)
	} else {
		res, err = ri.Patch(ctx, name, pt, patch, opts)
	}
	if err != nil {
		return Redact(Minify(cur)), nil, err
	}
	return Redact(Minify(cur)), Redact(Minify(res.UnstructuredContent())), nil
}
