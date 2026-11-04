package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/yaml"

	"github.com/kanivet/backend/internal/mcp/mcpreg"
)

// Write tools are served on /mcp only. Each is annotated destructive (or
// not) so MCP clients ask the user before running it.
func registerWriteTools(r *mcpreg.Registry, d *Deps) {
	mcpreg.RegisterOn(r, mcpreg.Spec{Name: "restart_workload", Write: true, Feature: "actions", Title: "Restart workload",
		Description: "Rolling restart of a Deployment, StatefulSet or DaemonSet (like kubectl rollout restart)."}, d.restart)
	mcpreg.RegisterOn(r, mcpreg.Spec{Name: "scale_workload", Write: true, Idempotent: true, Feature: "actions", Title: "Scale workload",
		Description: "Set replicas of a Deployment, StatefulSet or ReplicaSet."}, d.scale)
	mcpreg.RegisterOn(r, mcpreg.Spec{Name: "delete_pod", Destructive: true, Feature: "actions", Title: "Delete pod",
		Description: "Delete one pod (its controller usually recreates it)."}, d.deletePod)
	mcpreg.RegisterOn(r, mcpreg.Spec{Name: "apply_yaml", Destructive: true, Feature: "actions", Title: "Apply YAML",
		Description: "Server-side apply one or more YAML documents (field manager kanivet-mcp). Use dryRun=true first to validate and show the user the result."}, d.applyYAML)
	mcpreg.RegisterOn(r, mcpreg.Spec{Name: "helm_rollback", Destructive: true, Feature: "helm", Title: "Helm rollback",
		Description: "Roll a Helm release back to an earlier revision (see get_helm_release history)."}, d.helmRollback)
	mcpreg.RegisterOn(r, mcpreg.Spec{Name: "argo_sync", Destructive: true, Feature: "argo", Title: "Argo CD sync",
		Description: "Trigger a sync of an Argo CD Application through Argo's API (prune optional)."}, d.argoSync)
	mcpreg.RegisterOn(r, mcpreg.Spec{Name: "cordon_node", Write: true, Idempotent: true, Feature: "actions", Title: "Cordon node",
		Description: "Mark a node unschedulable (uncordon=true to undo)."}, d.cordon)
	mcpreg.RegisterOn(r, mcpreg.Spec{Name: "drain_node", Destructive: true, Feature: "actions", Title: "Drain node",
		Description: "Cordon a node and evict its pods (DaemonSet pods ignored)."}, d.drain)
}

type workloadArgs struct {
	mcpreg.ClusterArg
	Kind      string `json:"kind" jsonschema:"Deployment, StatefulSet, DaemonSet"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

func (d *Deps) restart(ctx context.Context, call *mcpreg.Call, in workloadArgs) (any, error) {
	if d.K8s == nil {
		return nil, errNoK8s
	}
	ki, err := d.resolveKind(call.Cluster, in.Kind)
	if err != nil {
		return nil, err
	}
	if err := d.K8s.RestartResource(ctx, call.Cluster, ki.GVR.Group, ki.GVR.Version, ki.Kind, in.Namespace, in.Name); err != nil {
		return nil, err
	}
	return fmt.Sprintf("restarted %s %s/%s", ki.Kind, in.Namespace, in.Name), nil
}

type scaleArgs struct {
	workloadArgs
	Replicas int32 `json:"replicas"`
}

func (d *Deps) scale(ctx context.Context, call *mcpreg.Call, in scaleArgs) (any, error) {
	if d.K8s == nil {
		return nil, errNoK8s
	}
	if in.Replicas < 0 {
		return nil, errors.New("replicas must be >= 0")
	}
	ki, err := d.resolveKind(call.Cluster, in.Kind)
	if err != nil {
		return nil, err
	}
	if err := d.K8s.ScaleResource(ctx, call.Cluster, ki.GVR.Group, ki.GVR.Version, ki.Kind, in.Namespace, in.Name, in.Replicas); err != nil {
		return nil, err
	}
	return fmt.Sprintf("scaled %s %s/%s to %d", ki.Kind, in.Namespace, in.Name, in.Replicas), nil
}

type podArgs struct {
	mcpreg.ClusterArg
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

func (d *Deps) deletePod(ctx context.Context, call *mcpreg.Call, in podArgs) (any, error) {
	if d.K8s == nil {
		return nil, errNoK8s
	}
	cs, err := d.K8s.GetClientForCluster(call.Cluster)
	if err != nil {
		return nil, err
	}
	if err := cs.CoreV1().Pods(in.Namespace).Delete(ctx, in.Name, metav1.DeleteOptions{}); err != nil {
		return nil, err
	}
	return fmt.Sprintf("deleted pod %s/%s", in.Namespace, in.Name), nil
}

type applyArgs struct {
	mcpreg.ClusterArg
	YAML      string `json:"yaml" jsonschema:"one or more YAML documents separated by ---"`
	Namespace string `json:"namespace,omitempty" jsonschema:"default namespace for namespaced objects without one"`
	DryRun    bool   `json:"dryRun,omitempty" jsonschema:"validate server-side without persisting"`
}

func (d *Deps) applyYAML(ctx context.Context, call *mcpreg.Call, in applyArgs) (any, error) {
	if d.K8s == nil {
		return nil, errNoK8s
	}
	dyn, err := d.K8s.GetDynamicClient(call.Cluster)
	if err != nil {
		return nil, err
	}
	var results []string
	for _, doc := range strings.Split(in.YAML, "\n---") {
		if strings.TrimSpace(doc) == "" {
			continue
		}
		var obj map[string]any
		if err := yaml.Unmarshal([]byte(doc), &obj); err != nil {
			return nil, fmt.Errorf("invalid yaml: %w", err)
		}
		if obj == nil {
			continue
		}
		u := &unstructured.Unstructured{Object: obj}
		ki, err := d.resolveKind(call.Cluster, u.GetKind()+groupSuffix(u.GetAPIVersion()))
		if err != nil {
			return nil, err
		}
		ns := u.GetNamespace()
		if ki.Namespaced && ns == "" {
			ns = orDefault(in.Namespace, "default")
			u.SetNamespace(ns)
		}
		data, err := u.MarshalJSON()
		if err != nil {
			return nil, err
		}
		force := true
		opts := metav1.PatchOptions{FieldManager: "kanivet-mcp", Force: &force}
		if in.DryRun {
			opts.DryRun = []string{metav1.DryRunAll}
		}
		var res *unstructured.Unstructured
		if ki.Namespaced {
			res, err = dyn.Resource(ki.GVR).Namespace(ns).Patch(ctx, u.GetName(), types.ApplyPatchType, data, opts)
		} else {
			res, err = dyn.Resource(ki.GVR).Patch(ctx, u.GetName(), types.ApplyPatchType, data, opts)
		}
		if err != nil {
			return nil, fmt.Errorf("%s %s: %w", ki.Kind, u.GetName(), err)
		}
		verb := "applied"
		if in.DryRun {
			verb = "dry-run ok"
		}
		results = append(results, fmt.Sprintf("%s %s %s/%s (rv %s)", verb, ki.Kind, ns, res.GetName(), res.GetResourceVersion()))
	}
	if len(results) == 0 {
		return nil, errors.New("no documents in yaml")
	}
	return strings.Join(results, "\n"), nil
}

func groupSuffix(apiVersion string) string {
	if i := strings.Index(apiVersion, "/"); i > 0 {
		return "." + apiVersion[:i]
	}
	return ""
}

type helmRollbackArgs struct {
	mcpreg.ClusterArg
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Revision  int    `json:"revision" jsonschema:"target revision (0 = previous)"`
}

func (d *Deps) helmRollback(ctx context.Context, call *mcpreg.Call, in helmRollbackArgs) (any, error) {
	if d.Helm == nil {
		return nil, errors.New("helm not available")
	}
	if err := d.Helm.RollbackRelease(ctx, call.Cluster, in.Namespace, in.Name, in.Revision); err != nil {
		return nil, err
	}
	return fmt.Sprintf("rolled back %s/%s to revision %d", in.Namespace, in.Name, in.Revision), nil
}

type argoSyncArgs struct {
	mcpreg.ClusterArg
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Prune     bool   `json:"prune,omitempty"`
	DryRun    bool   `json:"dryRun,omitempty"`
	Revision  string `json:"revision,omitempty"`
}

func (d *Deps) argoSync(ctx context.Context, call *mcpreg.Call, in argoSyncArgs) (any, error) {
	path := fmt.Sprintf("/cluster/argo/applications/%s/%s/sync", url.PathEscape(in.Namespace), url.PathEscape(in.Name))
	_, err := d.callAPI(ctx, http.MethodPost, path, url.Values{"cluster": {call.Cluster}}, map[string]any{"prune": in.Prune, "dryRun": in.DryRun, "revision": in.Revision})
	if err != nil {
		return nil, err
	}
	return fmt.Sprintf("sync started for %s/%s", in.Namespace, in.Name), nil
}

type nodeArgs struct {
	mcpreg.ClusterArg
	Name     string `json:"name"`
	Uncordon bool   `json:"uncordon,omitempty"`
}

func (d *Deps) cordon(ctx context.Context, call *mcpreg.Call, in nodeArgs) (any, error) {
	if d.K8s == nil {
		return nil, errNoK8s
	}
	if err := d.K8s.CordonNode(ctx, call.Cluster, in.Name, !in.Uncordon); err != nil {
		return nil, err
	}
	if in.Uncordon {
		return "uncordoned " + in.Name, nil
	}
	return "cordoned " + in.Name, nil
}

type drainArgs struct {
	mcpreg.ClusterArg
	Name           string `json:"name"`
	DeleteEmptyDir bool   `json:"deleteEmptyDir,omitempty"`
	GracePeriod    int    `json:"gracePeriodSeconds,omitempty" jsonschema:"-1 uses each pod's own"`
}

func (d *Deps) drain(ctx context.Context, call *mcpreg.Call, in drainArgs) (any, error) {
	if d.K8s == nil {
		return nil, errNoK8s
	}
	gp := in.GracePeriod
	if gp == 0 {
		gp = -1
	}
	res, err := d.K8s.DrainNode(ctx, call.Cluster, in.Name, true, in.DeleteEmptyDir, gp)
	if err != nil {
		return nil, err
	}
	return res, nil
}
