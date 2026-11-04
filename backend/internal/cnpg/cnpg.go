// Package cnpg reads CloudNativePG clusters, their instances, backups,
// scheduled backups and poolers, and runs the common operator actions
// (backup now, promote, rolling restart). See README.md for the service API.
package cnpg

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kanivet/backend/internal/crdprobe"
	"github.com/kanivet/backend/internal/gitops"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

const Group = "postgresql.cnpg.io"

// Kind is one CloudNativePG CRD.
type Kind struct {
	Kind    string `json:"kind"`
	Plural  string `json:"plural"`
	Version string `json:"version,omitempty"`
}

func (k Kind) GVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: Group, Version: k.Version, Resource: k.Plural}
}

var KnownKinds = []Kind{
	{Kind: "Cluster", Plural: "clusters"},
	{Kind: "Backup", Plural: "backups"},
	{Kind: "ScheduledBackup", Plural: "scheduledbackups"},
	{Kind: "Pooler", Plural: "poolers"},
}

// Detection says whether the operator's CRDs are installed.
type Detection struct {
	Installed bool   `json:"installed"`
	Kinds     []Kind `json:"kinds"`
}

func (d *Detection) Kind(kind string) (Kind, bool) {
	if d == nil {
		return Kind{}, false
	}
	for _, k := range d.Kinds {
		if strings.EqualFold(k.Kind, kind) || k.Plural == kind {
			return k, true
		}
	}
	return Kind{}, false
}

// Detect probes the CNPG CRDs. The Cluster CRD decides Installed.
func Detect(ctx context.Context, dyn dynamic.Interface) (*Detection, error) {
	det := &Detection{Kinds: []Kind{}}
	if dyn == nil {
		return det, nil
	}
	for _, k := range KnownKinds {
		res, err := crdprobe.Probe(ctx, dyn, k.Plural, Group)
		if err != nil {
			if k.Kind == "Cluster" {
				return det, err
			}
			continue
		}
		if !res.Found {
			continue
		}
		k.Version = res.Version
		det.Kinds = append(det.Kinds, k)
		if k.Kind == "Cluster" {
			det.Installed = true
		}
	}
	return det, nil
}

// Health values for a cluster.
const (
	HealthHealthy     = "Healthy"
	HealthDegraded    = "Degraded"
	HealthFailing     = "Failing"
	HealthProgressing = "Progressing"
)

// ClusterSummary is the list view of one Cluster.
type ClusterSummary struct {
	Namespace                string `json:"namespace"`
	Name                     string `json:"name"`
	Phase                    string `json:"phase,omitempty"`
	PhaseReason              string `json:"phaseReason,omitempty"`
	Health                   string `json:"health"`
	Instances                int64  `json:"instances"`
	ReadyInstances           int64  `json:"readyInstances"`
	CurrentPrimary           string `json:"currentPrimary,omitempty"`
	TargetPrimary            string `json:"targetPrimary,omitempty"`
	Timeline                 int64  `json:"timeline,omitempty"`
	Image                    string `json:"image,omitempty"`
	StorageSize              string `json:"storageSize,omitempty"`
	BackupMethod             string `json:"backupMethod,omitempty"`
	BackupPlugin             string `json:"backupPlugin,omitempty"`
	FirstRecoverabilityPoint string `json:"firstRecoverabilityPoint,omitempty"`
	LastSuccessfulBackup     string `json:"lastSuccessfulBackup,omitempty"`
	LastFailedBackup         string `json:"lastFailedBackup,omitempty"`
	CreatedAt                string `json:"createdAt,omitempty"`
}

// Instance is one PostgreSQL instance (pod) of a cluster.
type Instance struct {
	Name     string `json:"name"`
	Role     string `json:"role"`   // primary or replica
	Status   string `json:"status"` // healthy, replicating, failed, unknown
	Ready    bool   `json:"ready"`
	Node     string `json:"node,omitempty"`
	PodPhase string `json:"podPhase,omitempty"`
	Restarts int32  `json:"restarts"`
	// LagSeconds is the replica's replay lag from the instance's metrics
	// endpoint, when it could be read.
	LagSeconds *float64 `json:"lagSeconds,omitempty"`
}

// Backup is one Backup object.
type Backup struct {
	Name      string `json:"name"`
	Phase     string `json:"phase,omitempty"`
	Method    string `json:"method,omitempty"`
	Target    string `json:"target,omitempty"`
	StartedAt string `json:"startedAt,omitempty"`
	StoppedAt string `json:"stoppedAt,omitempty"`
	Error     string `json:"error,omitempty"`
	CreatedAt string `json:"createdAt,omitempty"`
}

// ScheduledBackup is one ScheduledBackup object.
type ScheduledBackup struct {
	Name             string `json:"name"`
	Schedule         string `json:"schedule"`
	Suspended        bool   `json:"suspended"`
	Immediate        bool   `json:"immediate"`
	Method           string `json:"method,omitempty"`
	LastScheduleTime string `json:"lastScheduleTime,omitempty"`
	NextScheduleTime string `json:"nextScheduleTime,omitempty"`
}

// Pooler is one PgBouncer Pooler object.
type Pooler struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	Instances int64  `json:"instances"`
	PoolMode  string `json:"poolMode,omitempty"`
}

// ClusterStatus is the CNPGClusterStatus answer.
type ClusterStatus struct {
	Cluster          ClusterSummary     `json:"cluster"`
	Instances        []Instance         `json:"instances"`
	Backups          []Backup           `json:"backups"`
	ScheduledBackups []ScheduledBackup  `json:"scheduledBackups"`
	Poolers          []Pooler           `json:"poolers"`
	Conditions       []gitops.Condition `json:"conditions"`
	// BackupConfigured is false when the cluster declares no backup method,
	// in which case "Backup now" cannot work.
	BackupConfigured bool `json:"backupConfigured"`
}

func nstr(o map[string]any, path ...string) string {
	s, _, _ := unstructured.NestedString(o, path...)
	return s
}

func nint(o map[string]any, path ...string) int64 {
	v, found, _ := unstructured.NestedFieldNoCopy(o, path...)
	if !found {
		return 0
	}
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	}
	return 0
}

// BackupMethodOf returns the method a new Backup should use for this cluster
// ("barmanObjectStore", "volumeSnapshot" or "plugin") and the plugin name.
func BackupMethodOf(cluster *unstructured.Unstructured) (method, plugin string) {
	o := cluster.Object
	if _, ok, _ := unstructured.NestedMap(o, "spec", "backup", "barmanObjectStore"); ok {
		return "barmanObjectStore", ""
	}
	if _, ok, _ := unstructured.NestedMap(o, "spec", "backup", "volumeSnapshot"); ok {
		return "volumeSnapshot", ""
	}
	plugins, _, _ := unstructured.NestedSlice(o, "spec", "plugins")
	for _, p := range plugins {
		m, ok := p.(map[string]any)
		if !ok {
			continue
		}
		if wal, _ := m["isWALArchiver"].(bool); wal {
			n, _ := m["name"].(string)
			return "plugin", n
		}
	}
	return "", ""
}

// ParseCluster turns a Cluster object into its summary.
func ParseCluster(obj *unstructured.Unstructured) ClusterSummary {
	o := obj.Object
	s := ClusterSummary{
		Namespace:                obj.GetNamespace(),
		Name:                     obj.GetName(),
		Phase:                    nstr(o, "status", "phase"),
		PhaseReason:              nstr(o, "status", "phaseReason"),
		Instances:                nint(o, "spec", "instances"),
		ReadyInstances:           nint(o, "status", "readyInstances"),
		CurrentPrimary:           nstr(o, "status", "currentPrimary"),
		TargetPrimary:            nstr(o, "status", "targetPrimary"),
		Timeline:                 nint(o, "status", "timelineID"),
		Image:                    nstr(o, "status", "image"),
		StorageSize:              nstr(o, "spec", "storage", "size"),
		FirstRecoverabilityPoint: nstr(o, "status", "firstRecoverabilityPoint"),
		LastSuccessfulBackup:     nstr(o, "status", "lastSuccessfulBackup"),
		LastFailedBackup:         nstr(o, "status", "lastFailedBackup"),
	}
	if s.Image == "" {
		s.Image = nstr(o, "spec", "imageName")
	}
	if ts := obj.GetCreationTimestamp(); !ts.IsZero() {
		s.CreatedAt = ts.UTC().Format(time.RFC3339)
	}
	s.BackupMethod, s.BackupPlugin = BackupMethodOf(obj)
	s.Health = clusterHealth(s)
	return s
}

func clusterHealth(s ClusterSummary) string {
	p := strings.ToLower(s.Phase)
	switch {
	case strings.Contains(p, "healthy") && s.ReadyInstances >= s.Instances:
		return HealthHealthy
	case strings.Contains(p, "fail") || strings.Contains(p, "unrecoverable") || (s.Instances > 0 && s.ReadyInstances == 0 && p != "" && !strings.Contains(p, "setting up")):
		return HealthFailing
	case s.ReadyInstances < s.Instances && strings.Contains(p, "healthy"):
		return HealthDegraded
	}
	return HealthProgressing
}

// ParseInstances reads instance names and their status buckets from the
// Cluster's status. Pod facts are merged later by MergePods.
func ParseInstances(obj *unstructured.Unstructured) []Instance {
	o := obj.Object
	primary := nstr(o, "status", "currentPrimary")
	byName := map[string]*Instance{}
	names, _, _ := unstructured.NestedStringSlice(o, "status", "instanceNames")
	for _, n := range names {
		byName[n] = &Instance{Name: n, Status: "unknown"}
	}
	buckets, _, _ := unstructured.NestedMap(o, "status", "instancesStatus")
	for bucket, raw := range buckets {
		list, _ := raw.([]any)
		for _, r := range list {
			n, _ := r.(string)
			if n == "" {
				continue
			}
			in := byName[n]
			if in == nil {
				in = &Instance{Name: n}
				byName[n] = in
			}
			in.Status = bucket
		}
	}
	out := make([]Instance, 0, len(byName))
	for _, in := range byName {
		in.Role = "replica"
		if in.Name == primary {
			in.Role = "primary"
		}
		out = append(out, *in)
	}
	sortInstances(out)
	return out
}

func sortInstances(in []Instance) {
	sort.SliceStable(in, func(i, j int) bool {
		if (in[i].Role == "primary") != (in[j].Role == "primary") {
			return in[i].Role == "primary"
		}
		return in[i].Name < in[j].Name
	})
}

// PodFacts is what MergePods needs from a pod.
type PodFacts struct {
	Name     string
	Node     string
	Phase    string
	Ready    bool
	Restarts int32
	Role     string // from the cnpg.io/instanceRole label
}

// MergePods folds pod facts into instances; pods unknown to the status are
// added (the status can lag a scale-up).
func MergePods(instances []Instance, pods []PodFacts) []Instance {
	idx := map[string]int{}
	for i, in := range instances {
		idx[in.Name] = i
	}
	for _, p := range pods {
		i, ok := idx[p.Name]
		if !ok {
			instances = append(instances, Instance{Name: p.Name, Status: "unknown", Role: "replica"})
			i = len(instances) - 1
			idx[p.Name] = i
		}
		in := &instances[i]
		// Role stays as status.currentPrimary says: the pod label can flip
		// ahead of it during a switchover, and the status is what the
		// operator acts on.
		in.Node, in.PodPhase, in.Ready, in.Restarts = p.Node, p.Phase, p.Ready, p.Restarts
	}
	sortInstances(instances)
	return instances
}

// ParseBackup reads one Backup.
func ParseBackup(obj *unstructured.Unstructured) Backup {
	o := obj.Object
	b := Backup{
		Name:      obj.GetName(),
		Phase:     nstr(o, "status", "phase"),
		Method:    nstr(o, "status", "method"),
		Target:    nstr(o, "spec", "target"),
		StartedAt: nstr(o, "status", "startedAt"),
		StoppedAt: nstr(o, "status", "stoppedAt"),
		Error:     nstr(o, "status", "error"),
	}
	if b.Method == "" {
		b.Method = nstr(o, "spec", "method")
	}
	if ts := obj.GetCreationTimestamp(); !ts.IsZero() {
		b.CreatedAt = ts.UTC().Format(time.RFC3339)
	}
	return b
}

// ParseScheduledBackup reads one ScheduledBackup.
func ParseScheduledBackup(obj *unstructured.Unstructured) ScheduledBackup {
	o := obj.Object
	s := ScheduledBackup{
		Name:             obj.GetName(),
		Schedule:         nstr(o, "spec", "schedule"),
		Method:           nstr(o, "spec", "method"),
		LastScheduleTime: nstr(o, "status", "lastScheduleTime"),
		NextScheduleTime: nstr(o, "status", "nextScheduleTime"),
	}
	s.Suspended, _, _ = unstructured.NestedBool(o, "spec", "suspend")
	s.Immediate, _, _ = unstructured.NestedBool(o, "spec", "immediate")
	return s
}

// ParsePooler reads one Pooler.
func ParsePooler(obj *unstructured.Unstructured) Pooler {
	o := obj.Object
	p := Pooler{Name: obj.GetName(), Type: nstr(o, "spec", "type"), Instances: nint(o, "spec", "instances"), PoolMode: nstr(o, "spec", "pgbouncer", "poolMode")}
	if p.Type == "" {
		p.Type = "rw"
	}
	return p
}

func clusterOf(obj *unstructured.Unstructured) string {
	return nstr(obj.Object, "spec", "cluster", "name")
}

// ListClusters lists every Cluster.
func ListClusters(ctx context.Context, dyn dynamic.Interface, det *Detection) ([]ClusterSummary, error) {
	k, ok := det.Kind("Cluster")
	if !ok || dyn == nil {
		return []ClusterSummary{}, nil
	}
	list, err := dyn.Resource(k.GVR()).Namespace(metav1.NamespaceAll).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]ClusterSummary, 0, len(list.Items))
	for i := range list.Items {
		out = append(out, ParseCluster(&list.Items[i]))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// Deps are the optional reads ClusterStatus enriches with. Nil fields are
// skipped, so callers without pod access still get a useful answer.
type Deps struct {
	// Pods lists the cluster's instance pods.
	Pods func(ctx context.Context, namespace, cluster string) ([]PodFacts, error)
	// Lag returns a replica's replay lag in seconds.
	Lag func(ctx context.Context, namespace, pod string) (float64, error)
}

// GetClusterStatus assembles the full view of one cluster.
func GetClusterStatus(ctx context.Context, dyn dynamic.Interface, det *Detection, namespace, name string, deps Deps) (*ClusterStatus, error) {
	k, ok := det.Kind("Cluster")
	if !ok {
		return nil, fmt.Errorf("CloudNativePG is not installed")
	}
	obj, err := dyn.Resource(k.GVR()).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	st := &ClusterStatus{
		Cluster:          ParseCluster(obj),
		Instances:        ParseInstances(obj),
		Backups:          []Backup{},
		ScheduledBackups: []ScheduledBackup{},
		Poolers:          []Pooler{},
		Conditions:       gitops.ReadConditions(obj),
	}
	st.BackupConfigured = st.Cluster.BackupMethod != ""

	if deps.Pods != nil {
		if pods, err := deps.Pods(ctx, namespace, name); err == nil {
			st.Instances = MergePods(st.Instances, pods)
		}
	}
	if deps.Lag != nil {
		for i := range st.Instances {
			in := &st.Instances[i]
			if in.Role != "replica" || !in.Ready {
				continue
			}
			if lag, err := deps.Lag(ctx, namespace, in.Name); err == nil {
				v := lag
				in.LagSeconds = &v
			}
		}
	}

	readList := func(kind string, each func(*unstructured.Unstructured)) {
		kk, ok := det.Kind(kind)
		if !ok {
			return
		}
		list, err := dyn.Resource(kk.GVR()).Namespace(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			// RBAC or a missing CRD hides only this section.
			return
		}
		for i := range list.Items {
			if clusterOf(&list.Items[i]) == name {
				each(&list.Items[i])
			}
		}
	}
	readList("Backup", func(o *unstructured.Unstructured) { st.Backups = append(st.Backups, ParseBackup(o)) })
	readList("ScheduledBackup", func(o *unstructured.Unstructured) {
		st.ScheduledBackups = append(st.ScheduledBackups, ParseScheduledBackup(o))
	})
	readList("Pooler", func(o *unstructured.Unstructured) { st.Poolers = append(st.Poolers, ParsePooler(o)) })
	sort.Slice(st.Backups, func(i, j int) bool { return backupTime(st.Backups[i]) > backupTime(st.Backups[j]) })
	if st.Cluster.LastSuccessfulBackup == "" {
		for _, b := range st.Backups {
			if b.Phase == "completed" {
				st.Cluster.LastSuccessfulBackup = b.StoppedAt
				break
			}
		}
	}
	return st, nil
}

func backupTime(b Backup) string {
	if b.StartedAt != "" {
		return b.StartedAt
	}
	return b.CreatedAt
}
