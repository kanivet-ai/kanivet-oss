package gitops

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
)

// ReconcileOptions tune FluxReconcile.
type ReconcileOptions struct {
	// WithSource also asks the object's source to reconcile first, like
	// `flux reconcile kustomization --with-source`.
	WithSource bool
	// Now is the timestamp written to the annotation; zero means time.Now.
	Now time.Time
}

// ReconcileResult says what was annotated.
type ReconcileResult struct {
	RequestedAt string      `json:"requestedAt"`
	Objects     []ObjectRef `json:"objects"`
}

// FluxReconcile sets reconcile.fluxcd.io/requestedAt on the object (and its
// source when asked). Suspended objects are refused: Flux ignores the
// annotation on them, so success would be a lie.
func FluxReconcile(ctx context.Context, dyn dynamic.Interface, det *FluxDetection, kind, namespace, name string, opts ReconcileOptions) (*ReconcileResult, error) {
	k, ok := det.Kind(kind)
	if !ok {
		return nil, fmt.Errorf("flux kind %q is not installed", kind)
	}
	obj, err := dyn.Resource(k.GVR()).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	parsed := ParseFluxObject(k, obj)
	if parsed.Suspended {
		return nil, fmt.Errorf("%s %s/%s is suspended; resume it before reconciling", k.Kind, namespace, name)
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	res := &ReconcileResult{RequestedAt: stamp}

	if opts.WithSource && parsed.SourceRef != nil {
		if sk, ok := det.Kind(parsed.SourceRef.Kind); ok {
			if err := annotate(ctx, dyn, sk, parsed.SourceRef.Namespace, parsed.SourceRef.Name, ReconcileAnnotation, stamp); err != nil {
				return nil, fmt.Errorf("reconcile source %s/%s: %w", parsed.SourceRef.Namespace, parsed.SourceRef.Name, err)
			}
			res.Objects = append(res.Objects, ObjectRef{APIVersion: sk.APIVersion(), Kind: sk.Kind, Namespace: parsed.SourceRef.Namespace, Name: parsed.SourceRef.Name})
		}
	}
	if err := annotate(ctx, dyn, k, namespace, name, ReconcileAnnotation, stamp); err != nil {
		return nil, err
	}
	res.Objects = append(res.Objects, ObjectRef{APIVersion: k.APIVersion(), Kind: k.Kind, Namespace: namespace, Name: name})
	return res, nil
}

// FluxSetSuspended patches spec.suspend. Resuming also requests a reconcile so
// the object catches up immediately, as `flux resume` does.
func FluxSetSuspended(ctx context.Context, dyn dynamic.Interface, det *FluxDetection, kind, namespace, name string, suspend bool) error {
	k, ok := det.Kind(kind)
	if !ok {
		return fmt.Errorf("flux kind %q is not installed", kind)
	}
	patch := map[string]any{"spec": map[string]any{"suspend": suspend}}
	if !suspend {
		patch["metadata"] = map[string]any{"annotations": map[string]any{ReconcileAnnotation: time.Now().UTC().Format(time.RFC3339Nano)}}
	}
	data, _ := json.Marshal(patch)
	_, err := dyn.Resource(k.GVR()).Namespace(namespace).Patch(ctx, name, types.MergePatchType, data, metav1.PatchOptions{})
	return err
}

func annotate(ctx context.Context, dyn dynamic.Interface, k FluxKind, namespace, name, key, value string) error {
	data, _ := json.Marshal(map[string]any{"metadata": map[string]any{"annotations": map[string]any{key: value}}})
	_, err := dyn.Resource(k.GVR()).Namespace(namespace).Patch(ctx, name, types.MergePatchType, data, metav1.PatchOptions{})
	return err
}
