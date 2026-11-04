package cnpg

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
)

// RestartAnnotation triggers a rolling restart of every instance, the same
// annotation `kubectl cnpg restart` writes.
const RestartAnnotation = "kubectl.kubernetes.io/restartedAt"

const phaseSwitchover = "Switchover in progress"

// BackupResult names the Backup object that was created.
type BackupResult struct {
	Name   string `json:"name"`
	Method string `json:"method"`
}

// BackupNow creates a Backup for the cluster using the method the cluster
// declares (barmanObjectStore, volumeSnapshot or a WAL-archiver plugin).
func BackupNow(ctx context.Context, dyn dynamic.Interface, det *Detection, namespace, cluster string, now time.Time) (*BackupResult, error) {
	ck, ok := det.Kind("Cluster")
	bk, okB := det.Kind("Backup")
	if !ok || !okB {
		return nil, fmt.Errorf("CloudNativePG Backup CRD is not installed")
	}
	obj, err := dyn.Resource(ck.GVR()).Namespace(namespace).Get(ctx, cluster, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	backup, err := NewBackup(obj, now)
	if err != nil {
		return nil, err
	}
	backup.SetAPIVersion(Group + "/" + bk.Version)
	created, err := dyn.Resource(bk.GVR()).Namespace(namespace).Create(ctx, backup, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}
	method, _, _ := unstructured.NestedString(created.Object, "spec", "method")
	return &BackupResult{Name: created.GetName(), Method: method}, nil
}

// NewBackup builds the Backup object for a cluster, refusing clusters that
// declare no backup method (the operator would fail the Backup).
func NewBackup(cluster *unstructured.Unstructured, now time.Time) (*unstructured.Unstructured, error) {
	method, plugin := BackupMethodOf(cluster)
	if method == "" {
		return nil, fmt.Errorf("cluster %s/%s has no backup configuration (spec.backup or a WAL-archiver plugin)", cluster.GetNamespace(), cluster.GetName())
	}
	if now.IsZero() {
		now = time.Now()
	}
	spec := map[string]any{"cluster": map[string]any{"name": cluster.GetName()}, "method": method}
	if plugin != "" {
		spec["pluginConfiguration"] = map[string]any{"name": plugin}
	}
	name := fmt.Sprintf("%s-%s", cluster.GetName(), now.UTC().Format("20060102150405"))
	if len(name) > 63 {
		name = name[len(name)-63:]
		name = strings.TrimLeft(name, "-")
	}
	b := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": Group + "/v1",
		"kind":       "Backup",
		"metadata": map[string]any{
			"name":      name,
			"namespace": cluster.GetNamespace(),
			"labels":    map[string]any{"cnpg.io/cluster": cluster.GetName(), "app.kubernetes.io/managed-by": "kanivet"},
		},
		"spec": spec,
	}}
	return b, nil
}

// Promote requests a switchover to the given replica by patching the
// cluster's status, which is what `kubectl cnpg promote` does.
func Promote(ctx context.Context, dyn dynamic.Interface, det *Detection, namespace, cluster, instance string, now time.Time) error {
	ck, ok := det.Kind("Cluster")
	if !ok {
		return fmt.Errorf("CloudNativePG is not installed")
	}
	obj, err := dyn.Resource(ck.GVR()).Namespace(namespace).Get(ctx, cluster, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if err := CheckPromote(obj, instance); err != nil {
		return err
	}
	if now.IsZero() {
		now = time.Now()
	}
	data, _ := json.Marshal(map[string]any{"status": map[string]any{
		"targetPrimary":          instance,
		"targetPrimaryTimestamp": now.UTC().Format(metav1.RFC3339Micro),
		"phase":                  phaseSwitchover,
		"phaseReason":            "Switching over to " + instance,
	}})
	_, err = dyn.Resource(ck.GVR()).Namespace(namespace).Patch(ctx, cluster, types.MergePatchType, data, metav1.PatchOptions{}, "status")
	return err
}

// CheckPromote refuses promotions the operator would reject or that make no
// sense: unknown instance, already primary, or a switchover in flight.
func CheckPromote(cluster *unstructured.Unstructured, instance string) error {
	o := cluster.Object
	if instance == "" {
		return fmt.Errorf("an instance to promote is required")
	}
	names, _, _ := unstructured.NestedStringSlice(o, "status", "instanceNames")
	known := false
	for _, n := range names {
		if n == instance {
			known = true
		}
	}
	if !known {
		return fmt.Errorf("%s is not an instance of %s", instance, cluster.GetName())
	}
	if nstr(o, "status", "currentPrimary") == instance {
		return fmt.Errorf("%s is already the primary", instance)
	}
	cur, target := nstr(o, "status", "currentPrimary"), nstr(o, "status", "targetPrimary")
	if target != "" && target != cur {
		return fmt.Errorf("a switchover to %s is already in progress", target)
	}
	return nil
}

// Restart requests a rolling restart of every instance.
func Restart(ctx context.Context, dyn dynamic.Interface, det *Detection, namespace, cluster string, now time.Time) error {
	ck, ok := det.Kind("Cluster")
	if !ok {
		return fmt.Errorf("CloudNativePG is not installed")
	}
	if now.IsZero() {
		now = time.Now()
	}
	data, _ := json.Marshal(map[string]any{"metadata": map[string]any{"annotations": map[string]any{RestartAnnotation: now.UTC().Format(time.RFC3339)}}})
	_, err := dyn.Resource(ck.GVR()).Namespace(namespace).Patch(ctx, cluster, types.MergePatchType, data, metav1.PatchOptions{})
	return err
}

// ParseReplicationLag reads cnpg_pg_replication_lag (seconds) from a
// Prometheus text exposition, as served on an instance's :9187/metrics.
func ParseReplicationLag(metrics string) (float64, bool) {
	sc := bufio.NewScanner(strings.NewReader(metrics))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "cnpg_pg_replication_lag") {
			continue
		}
		rest := strings.TrimPrefix(line, "cnpg_pg_replication_lag")
		if rest != "" && rest[0] != ' ' && rest[0] != '{' {
			continue
		}
		// "name{labels} value [timestamp]": the value follows the labels.
		if i := strings.LastIndex(rest, "}"); i >= 0 {
			rest = rest[i+1:]
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		if v, err := strconv.ParseFloat(fields[0], 64); err == nil {
			return v, true
		}
	}
	return 0, false
}
