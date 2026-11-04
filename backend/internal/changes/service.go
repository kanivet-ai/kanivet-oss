package changes

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/kanivet/backend/internal/helm"
)

// Clients is the part of k8s.Client the service needs.
type Clients interface {
	GetClientForCluster(cluster string) (kubernetes.Interface, error)
	GetDynamicClient(cluster string) (dynamic.Interface, error)
	GetResourceName(cluster, group, version, kind string) string
}

// HelmSource is the part of helm.Service the service needs.
type HelmSource interface {
	ListReleases(ctx context.Context, cluster, namespace string, allNamespaces bool) ([]helm.Release, error)
	GetReleaseHistory(ctx context.Context, cluster, namespace, name string, max int) ([]helm.HistoryEntry, error)
	GetReleaseRevision(ctx context.Context, cluster, namespace, name string, revision int) (*helm.ReleaseRevision, error)
}

// Service answers history questions from the cluster's own records.
type Service struct {
	clients Clients
	helm    HelmSource

	feedMu    sync.Mutex
	feedCache map[string]cachedFeed
}

type cachedFeed struct {
	at   time.Time
	feed Feed
}

const (
	feedCacheTTL     = 30 * time.Second
	maxEvents        = 50
	defaultFeedLimit = 300
	defaultFeedSince = 24 * time.Hour
)

var (
	gvrArgoApps        = schema.GroupVersionResource{Group: "argoproj.io", Version: "v1alpha1", Resource: "applications"}
	gvrFluxHelmRelease = schema.GroupVersionResource{Group: "helm.toolkit.fluxcd.io", Version: "v2", Resource: "helmreleases"}
	gvrFluxKustomize   = schema.GroupVersionResource{Group: "kustomize.toolkit.fluxcd.io", Version: "v1", Resource: "kustomizations"}
)

// NewService builds the service. helmSrc may be nil.
func NewService(clients Clients, helmSrc HelmSource) *Service {
	return &Service{clients: clients, helm: helmSrc, feedCache: map[string]cachedFeed{}}
}

// sourceWarning turns a source failure into a warning, or "" when the source
// simply does not exist in the cluster (CRD not installed).
func sourceWarning(what string, err error) string {
	if err == nil || apierrors.IsNotFound(err) || strings.Contains(err.Error(), "the server could not find the requested resource") {
		return ""
	}
	if apierrors.IsForbidden(err) {
		return what + ": not permitted by RBAC"
	}
	return what + ": " + err.Error()
}

// ChangesFor reconstructs one resource's history, newest first.
func (s *Service) ChangesFor(ctx context.Context, ref ResourceRef) (*History, error) {
	if ref.Cluster == "" || ref.Kind == "" || ref.Name == "" {
		return nil, errors.New("cluster, kind and name are required")
	}
	if ref.Version == "" {
		ref.Version = "v1"
	}
	h := &History{Subject: ref, Entries: []Entry{}}
	warn := func(w string) {
		if w != "" {
			h.Warnings = append(h.Warnings, w)
		}
	}
	dyn, err := s.clients.GetDynamicClient(ref.Cluster)
	if err != nil {
		return nil, err
	}
	resource := s.clients.GetResourceName(ref.Cluster, ref.Group, ref.Version, ref.Kind)
	gvr := schema.GroupVersionResource{Group: ref.Group, Version: ref.Version, Resource: resource}
	var ri dynamic.ResourceInterface = dyn.Resource(gvr)
	if ref.Namespace != "" {
		ri = dyn.Resource(gvr).Namespace(ref.Namespace)
	}
	obj, err := ri.Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	typed, _ := s.clients.GetClientForCluster(ref.Cluster)

	// Creation.
	if ct := obj.GetCreationTimestamp(); !ct.IsZero() {
		h.Entries = append(h.Entries, Entry{
			ID: "created", Source: SourceCreation, Time: ct.Time, Subject: ref,
			Title: "Created", Categories: []Category{CatCreated},
		})
	}
	h.Sources = append(h.Sources, SourceCreation)

	// Rollouts.
	if typed != nil && ref.Group == "apps" {
		switch ref.Kind {
		case "Deployment":
			h.Sources = append(h.Sources, SourceReplicaSet)
			rss, err := typed.AppsV1().ReplicaSets(ref.Namespace).List(ctx, metav1.ListOptions{LabelSelector: selectorOf(obj)})
			if err != nil {
				warn(sourceWarning("ReplicaSets", err))
			} else {
				cur, _ := strconv.ParseInt(obj.GetAnnotations()[revisionAnnotation], 10, 64)
				h.Entries = append(h.Entries, revisionEntries(ref, replicaSetRevisions(ref.Cluster, obj.GetUID(), rss.Items), cur)...)
			}
		case "StatefulSet", "DaemonSet":
			h.Sources = append(h.Sources, SourceControllerRevision)
			crs, err := typed.AppsV1().ControllerRevisions(ref.Namespace).List(ctx, metav1.ListOptions{LabelSelector: selectorOf(obj)})
			if err != nil {
				warn(sourceWarning("ControllerRevisions", err))
			} else {
				h.Entries = append(h.Entries, revisionEntries(ref, controllerRevisions(ref.Cluster, obj.GetUID(), crs.Items), 0)...)
			}
		}
	}

	// Helm: the release itself, or the release that manages this object.
	if s.helm != nil {
		relName, relNS := obj.GetAnnotations()["meta.helm.sh/release-name"], obj.GetAnnotations()["meta.helm.sh/release-namespace"]
		if relName != "" {
			if relNS == "" {
				relNS = ref.Namespace
			}
			h.Sources = append(h.Sources, SourceHelm)
			hist, err := s.helm.GetReleaseHistory(ctx, ref.Cluster, relNS, relName, 50)
			if err != nil {
				warn(sourceWarning("Helm history", err))
			} else {
				h.Entries = append(h.Entries, helmEntries(ref, relNS, relName, hist)...)
			}
		}
	}

	// Argo CD.
	if ref.Group == "argoproj.io" && ref.Kind == "Application" {
		h.Sources = append(h.Sources, SourceArgoCD)
		h.Entries = append(h.Entries, argoEntries(ref, obj.Object)...)
	} else if app := argoAppOf(obj); app != "" {
		h.Sources = append(h.Sources, SourceArgoCD)
		apps, err := dyn.Resource(gvrArgoApps).List(ctx, metav1.ListOptions{FieldSelector: "metadata.name=" + app})
		if err != nil {
			warn(sourceWarning("Argo CD applications", err))
		} else {
			for i := range apps.Items {
				h.Entries = append(h.Entries, argoEntries(ref, apps.Items[i].Object)...)
			}
		}
	}

	// Flux.
	if strings.HasSuffix(ref.Group, ".toolkit.fluxcd.io") {
		h.Sources = append(h.Sources, SourceFlux)
		h.Entries = append(h.Entries, fluxEntries(ref, obj.Object)...)
	} else if gvr, ns, name, ok := fluxOwnerOf(obj); ok {
		h.Sources = append(h.Sources, SourceFlux)
		owner, err := dyn.Resource(gvr).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			warn(sourceWarning("Flux "+gvr.Resource, err))
		} else {
			h.Entries = append(h.Entries, fluxEntries(ref, owner.Object)...)
		}
	}

	// managedFields.
	h.Sources = append(h.Sources, SourceManagedFields)
	h.Entries = append(h.Entries, managedFieldsEntries(ref, obj.GetManagedFields())...)

	// Events.
	if typed != nil {
		h.Sources = append(h.Sources, SourceEvent)
		evs, err := listEventsFor(ctx, typed, ref, obj.GetUID())
		if err != nil {
			warn(sourceWarning("Events", err))
		} else {
			h.Entries = append(h.Entries, evs...)
		}
	}

	sortEntries(h.Entries)
	return h, nil
}

func sortEntries(e []Entry) {
	sort.SliceStable(e, func(i, j int) bool { return e[i].Time.After(e[j].Time) })
}

func selectorOf(obj *unstructured.Unstructured) string {
	m, found, _ := unstructured.NestedStringMap(obj.Object, "spec", "selector", "matchLabels")
	if !found || len(m) == 0 {
		return ""
	}
	return metav1.FormatLabelSelector(&metav1.LabelSelector{MatchLabels: m})
}

func argoAppOf(obj *unstructured.Unstructured) string {
	if v := obj.GetLabels()["argocd.argoproj.io/instance"]; v != "" {
		return v
	}
	// tracking-id: <app>:<group>/<kind>:<ns>/<name>
	if v := obj.GetAnnotations()["argocd.argoproj.io/tracking-id"]; v != "" {
		app := strings.SplitN(v, ":", 2)[0]
		if i := strings.LastIndex(app, "_"); i >= 0 {
			app = app[i+1:]
		}
		return app
	}
	return ""
}

func fluxOwnerOf(obj *unstructured.Unstructured) (schema.GroupVersionResource, string, string, bool) {
	l := obj.GetLabels()
	if n := l["helm.toolkit.fluxcd.io/name"]; n != "" {
		return gvrFluxHelmRelease, l["helm.toolkit.fluxcd.io/namespace"], n, true
	}
	if n := l["kustomize.toolkit.fluxcd.io/name"]; n != "" {
		return gvrFluxKustomize, l["kustomize.toolkit.fluxcd.io/namespace"], n, true
	}
	return schema.GroupVersionResource{}, "", "", false
}

func listEventsFor(ctx context.Context, c kubernetes.Interface, ref ResourceRef, uid types.UID) ([]Entry, error) {
	sel := "involvedObject.name=" + ref.Name + ",involvedObject.kind=" + ref.Kind
	ns := ref.Namespace
	list, err := c.CoreV1().Events(ns).List(ctx, metav1.ListOptions{FieldSelector: sel, Limit: 500})
	if err != nil {
		return nil, err
	}
	evs := list.Items
	sort.Slice(evs, func(i, j int) bool { return eventTime(&evs[i]).After(eventTime(&evs[j])) })
	var out []Entry
	for i := range evs {
		ev := &evs[i]
		if uid != "" && ev.InvolvedObject.UID != "" && ev.InvolvedObject.UID != uid {
			continue
		}
		if len(out) >= maxEvents {
			break
		}
		out = append(out, eventEntry(ref, ev))
	}
	return out, nil
}

func eventTime(ev *corev1.Event) time.Time {
	switch {
	case !ev.LastTimestamp.IsZero():
		return ev.LastTimestamp.Time
	case !ev.EventTime.IsZero():
		return ev.EventTime.Time
	default:
		return ev.FirstTimestamp.Time
	}
}

func eventEntry(ref ResourceRef, ev *corev1.Event) Entry {
	title := ev.Reason
	if ev.Count > 1 {
		title = fmt.Sprintf("%s (×%d)", ev.Reason, ev.Count)
	}
	cats := []Category{CatEvent}
	switch ev.Reason {
	case "ScalingReplicaSet", "SuccessfulRescale":
		cats = append(cats, CatReplicas)
	case "Killing", "Created", "Started", "Pulled":
	}
	return Entry{
		ID:         "event:" + string(ev.UID),
		Source:     SourceEvent,
		Time:       eventTime(ev),
		Subject:    ref,
		Title:      title,
		Summary:    ev.Message,
		Categories: cats,
		Actor:      ev.Source.Component,
		Warning:    ev.Type == corev1.EventTypeWarning,
	}
}

// Snapshot returns the YAML of one entry's revision, for entries whose
// snapshot is not sent inline (Helm). id is the Entry.ID.
func (s *Service) Snapshot(ctx context.Context, cluster, id string) (string, error) {
	if !strings.HasPrefix(id, "helm:") || s.helm == nil {
		return "", fmt.Errorf("entry %q has no fetchable snapshot", id)
	}
	rest := strings.TrimPrefix(id, "helm:")
	i := strings.LastIndex(rest, ":")
	if i < 0 {
		return "", fmt.Errorf("bad helm entry id %q", id)
	}
	nsName, revStr := rest[:i], rest[i+1:]
	parts := strings.SplitN(nsName, "/", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("bad helm entry id %q", id)
	}
	rev, err := strconv.Atoi(revStr)
	if err != nil {
		return "", fmt.Errorf("bad helm revision in %q", id)
	}
	r, err := s.helm.GetReleaseRevision(ctx, cluster, parts[0], parts[1], rev)
	if err != nil {
		return "", err
	}
	return helmSnapshot(r), nil
}

// RecentChanges is the cluster-wide feed: rollouts (ReplicaSets and
// ControllerRevisions created since f.Since), workload creations, Helm
// upgrades and Argo CD / Flux syncs. Results are cached for 30s per cluster.
func (s *Service) RecentChanges(ctx context.Context, cluster string, f Filter) (*Feed, error) {
	if cluster == "" {
		return nil, errors.New("cluster is required")
	}
	if f.Since.IsZero() {
		f.Since = time.Now().Add(-defaultFeedSince)
	}
	all, err := s.clusterFeed(ctx, cluster)
	if err != nil {
		return nil, err
	}
	out := &Feed{Cluster: cluster, Since: f.Since, Entries: []Entry{}, Warnings: all.Warnings}
	limit := f.Limit
	if limit <= 0 {
		limit = defaultFeedLimit
	}
	for _, e := range all.Entries {
		if e.Time.Before(f.Since) {
			continue
		}
		if !matches(e, f) {
			continue
		}
		out.Entries = append(out.Entries, e)
		if len(out.Entries) >= limit {
			break
		}
	}
	return out, nil
}

func matches(e Entry, f Filter) bool {
	if len(f.Namespaces) > 0 && !contains(f.Namespaces, e.Subject.Namespace) {
		return false
	}
	if len(f.Kinds) > 0 && !contains(f.Kinds, e.Subject.Kind) {
		return false
	}
	if len(f.Sources) > 0 {
		ok := false
		for _, s := range f.Sources {
			ok = ok || s == e.Source
		}
		if !ok {
			return false
		}
	}
	if len(f.Categories) > 0 {
		ok := false
		for _, want := range f.Categories {
			for _, c := range e.Categories {
				ok = ok || c == want
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if strings.EqualFold(x, v) {
			return true
		}
	}
	return false
}

// feedWindow is how far back the cached cluster feed reaches.
const feedWindow = 14 * 24 * time.Hour

func (s *Service) clusterFeed(ctx context.Context, cluster string) (Feed, error) {
	s.feedMu.Lock()
	if c, ok := s.feedCache[cluster]; ok && time.Since(c.at) < feedCacheTTL {
		s.feedMu.Unlock()
		return c.feed, nil
	}
	s.feedMu.Unlock()

	typed, err := s.clients.GetClientForCluster(cluster)
	if err != nil {
		return Feed{}, err
	}
	since := time.Now().Add(-feedWindow)
	feed := Feed{Cluster: cluster, Since: since}
	warn := func(w string) {
		if w != "" {
			feed.Warnings = append(feed.Warnings, w)
		}
	}
	rv0 := metav1.ListOptions{ResourceVersion: "0"}

	var wg sync.WaitGroup
	var mu sync.Mutex
	add := func(es []Entry) {
		mu.Lock()
		feed.Entries = append(feed.Entries, es...)
		mu.Unlock()
	}
	addWarn := func(w string) {
		mu.Lock()
		warn(w)
		mu.Unlock()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		deps, err := typed.AppsV1().Deployments("").List(ctx, rv0)
		if err != nil {
			addWarn(sourceWarning("Deployments", err))
			return
		}
		rss, err := typed.AppsV1().ReplicaSets("").List(ctx, rv0)
		if err != nil {
			addWarn(sourceWarning("ReplicaSets", err))
			return
		}
		add(deploymentFeed(cluster, deps.Items, rss.Items, since))
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		sts, err1 := typed.AppsV1().StatefulSets("").List(ctx, rv0)
		dss, err2 := typed.AppsV1().DaemonSets("").List(ctx, rv0)
		crs, err := typed.AppsV1().ControllerRevisions("").List(ctx, rv0)
		if err != nil {
			addWarn(sourceWarning("ControllerRevisions", err))
			return
		}
		var owners []ownerInfo
		if err1 == nil {
			for i := range sts.Items {
				o := &sts.Items[i]
				owners = append(owners, ownerInfo{"StatefulSet", o.ObjectMeta})
			}
		} else {
			addWarn(sourceWarning("StatefulSets", err1))
		}
		if err2 == nil {
			for i := range dss.Items {
				o := &dss.Items[i]
				owners = append(owners, ownerInfo{"DaemonSet", o.ObjectMeta})
			}
		} else {
			addWarn(sourceWarning("DaemonSets", err2))
		}
		add(controllerRevisionFeed(cluster, owners, crs.Items, since))
	}()
	if s.helm != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rels, err := s.helm.ListReleases(ctx, cluster, "", true)
			if err != nil {
				addWarn(sourceWarning("Helm releases", err))
				return
			}
			add(helmFeed(cluster, rels, since))
		}()
	}
	dyn, derr := s.clients.GetDynamicClient(cluster)
	if derr == nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			apps, err := dyn.Resource(gvrArgoApps).List(ctx, rv0)
			if err != nil {
				addWarn(sourceWarning("Argo CD applications", err))
			} else {
				for i := range apps.Items {
					ref := refOf(cluster, &apps.Items[i])
					add(argoEntries(ref, apps.Items[i].Object))
				}
			}
			for _, gvr := range []schema.GroupVersionResource{gvrFluxHelmRelease, gvrFluxKustomize} {
				list, err := dyn.Resource(gvr).List(ctx, rv0)
				if err != nil {
					addWarn(sourceWarning("Flux "+gvr.Resource, err))
					continue
				}
				for i := range list.Items {
					add(fluxEntries(refOf(cluster, &list.Items[i]), list.Items[i].Object))
				}
			}
		}()
	}
	wg.Wait()

	kept := feed.Entries[:0]
	for _, e := range feed.Entries {
		if !e.Time.Before(since) {
			// The feed carries no snapshots: the History tab fetches them.
			e.Snapshot = ""
			kept = append(kept, e)
		}
	}
	feed.Entries = kept
	sortEntries(feed.Entries)
	if ctx.Err() == nil {
		s.feedMu.Lock()
		s.feedCache[cluster] = cachedFeed{at: time.Now(), feed: feed}
		s.feedMu.Unlock()
	}
	return feed, nil
}

func refOf(cluster string, u *unstructured.Unstructured) ResourceRef {
	gv := u.GroupVersionKind()
	return ResourceRef{Cluster: cluster, Group: gv.Group, Version: gv.Version, Kind: gv.Kind, Namespace: u.GetNamespace(), Name: u.GetName()}
}

type ownerInfo struct {
	kind string
	meta metav1.ObjectMeta
}

func deploymentFeed(cluster string, deps []appsv1.Deployment, rss []appsv1.ReplicaSet, since time.Time) []Entry {
	byOwner := map[types.UID][]appsv1.ReplicaSet{}
	for i := range rss {
		for _, o := range rss[i].OwnerReferences {
			if o.Kind == "Deployment" {
				byOwner[o.UID] = append(byOwner[o.UID], rss[i])
			}
		}
	}
	var out []Entry
	for i := range deps {
		d := &deps[i]
		ref := ResourceRef{Cluster: cluster, Group: "apps", Version: "v1", Kind: "Deployment", Namespace: d.Namespace, Name: d.Name}
		owned := byOwner[d.UID]
		cur, _ := strconv.ParseInt(d.Annotations[revisionAnnotation], 10, 64)
		for _, e := range revisionEntries(ref, replicaSetRevisions(cluster, d.UID, owned), cur) {
			if !e.Time.Before(since) {
				out = append(out, e)
			}
		}
	}
	return out
}

func controllerRevisionFeed(cluster string, owners []ownerInfo, crs []appsv1.ControllerRevision, since time.Time) []Entry {
	var out []Entry
	for _, o := range owners {
		ref := ResourceRef{Cluster: cluster, Group: "apps", Version: "v1", Kind: o.kind, Namespace: o.meta.Namespace, Name: o.meta.Name}
		for _, e := range revisionEntries(ref, controllerRevisions(cluster, o.meta.UID, crs), 0) {
			if !e.Time.Before(since) {
				out = append(out, e)
			}
		}
	}
	return out
}

// helmFeed lists each release's current revision; earlier revisions are in
// the release's own History.
func helmFeed(cluster string, rels []helm.Release, since time.Time) []Entry {
	var out []Entry
	for _, r := range rels {
		if r.Updated.Before(since) {
			continue
		}
		ref := ResourceRef{Cluster: cluster, Kind: "HelmRelease", Namespace: r.Namespace, Name: r.Name}
		hist := []helm.HistoryEntry{{Revision: r.Revision, Status: r.Status, Chart: r.Chart + "-" + r.ChartVersion, AppVersion: r.AppVersion, Updated: r.Updated, Description: r.Description}}
		out = append(out, helmEntries(ref, r.Namespace, r.Name, hist)...)
	}
	return out
}
