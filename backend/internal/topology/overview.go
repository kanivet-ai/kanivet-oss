package topology

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// HealthCounts counts resources per health state.
type HealthCounts struct {
	Healthy int `json:"healthy"`
	Warning int `json:"warning"`
	Error   int `json:"error"`
	Unknown int `json:"unknown"`
}

func (c *HealthCounts) add(h Health) {
	switch h {
	case HealthHealthy:
		c.Healthy++
	case HealthWarning:
		c.Warning++
	case HealthError:
		c.Error++
	default:
		c.Unknown++
	}
}

// Worst is the rollup: the worst state any counted resource is in.
func (c HealthCounts) Worst() Health {
	switch {
	case c.Error > 0:
		return HealthError
	case c.Warning > 0:
		return HealthWarning
	case c.Healthy > 0:
		return HealthHealthy
	}
	return HealthUnknown
}

// NamespaceSummary is one namespace in the cluster overview.
type NamespaceSummary struct {
	Namespace string       `json:"namespace"`
	Health    Health       `json:"health"`
	Pods      HealthCounts `json:"pods"`
	Workloads HealthCounts `json:"workloads"`
	// Apps lists the application names found (see Node.App), sorted, at most 8.
	Apps     []string `json:"apps,omitempty"`
	AppCount int      `json:"appCount"`
}

// Overview is the cluster-wide rollup, one entry per namespace that has
// workloads or pods.
type Overview struct {
	Namespaces []NamespaceSummary `json:"namespaces"`
	Warnings   []string           `json:"warnings,omitempty"`
}

var overviewKinds = []string{"Pod", "Deployment", "StatefulSet", "DaemonSet", "CronJob"}

const overviewTTL = 10 * time.Second

type overviewEntry struct {
	at  time.Time
	ov  *Overview
	err error
}

var (
	overviewMu    sync.Mutex
	overviewCache = map[*Service]map[string]overviewEntry{}
)

// ClusterOverview rolls pod and workload health up per namespace, from five
// cluster-wide LISTs (cached for a few seconds). Kinds we may not list
// become warnings.
func (s *Service) ClusterOverview(ctx context.Context, cluster string) (*Overview, error) {
	overviewMu.Lock()
	if e, ok := overviewCache[s][cluster]; ok && s.now().Sub(e.at) < overviewTTL && e.err == nil {
		overviewMu.Unlock()
		return e.ov, nil
	}
	overviewMu.Unlock()

	src, err := s.source(cluster)
	if err != nil {
		return nil, err
	}
	type res struct {
		kind  string
		items []Health
		apps  []string
		ns    []string
		err   error
	}
	results := make([]res, len(overviewKinds))
	var wg sync.WaitGroup
	for i, kind := range overviewKinds {
		i, kind := i, kind
		wg.Add(1)
		go func() {
			defer wg.Done()
			list, err := src.List(ctx, specByKind[kind], "")
			r := res{kind: kind, err: err}
			for _, u := range list {
				// Owned workloads (ReplicaSets aside) are counted via their top.
				if kind != "Pod" && len(u.GetOwnerReferences()) > 0 {
					continue
				}
				h, _ := healthOf(u)
				r.items = append(r.items, h)
				r.ns = append(r.ns, u.GetNamespace())
				r.apps = append(r.apps, appOf(u.GetLabels(), u.GetAnnotations()))
			}
			results[i] = r
		}()
	}
	wg.Wait()

	byNS := map[string]*NamespaceSummary{}
	apps := map[string]map[string]bool{}
	ov := &Overview{Namespaces: []NamespaceSummary{}}
	failed := 0
	for _, r := range results {
		if r.err != nil {
			failed++
			ov.Warnings = append(ov.Warnings, fmt.Sprintf("%s: %v", r.kind, shortErr(r.err)))
			continue
		}
		for i, h := range r.items {
			ns := r.ns[i]
			sum := byNS[ns]
			if sum == nil {
				sum = &NamespaceSummary{Namespace: ns}
				byNS[ns] = sum
				apps[ns] = map[string]bool{}
			}
			if r.kind == "Pod" {
				sum.Pods.add(h)
			} else {
				sum.Workloads.add(h)
				if a := r.apps[i]; a != "" {
					apps[ns][a] = true
				}
			}
		}
	}
	if failed == len(overviewKinds) {
		return nil, fmt.Errorf("could not read the cluster: %s", ov.Warnings[0])
	}
	for ns, sum := range byNS {
		list := make([]string, 0, len(apps[ns]))
		for a := range apps[ns] {
			list = append(list, a)
		}
		sort.Strings(list)
		sum.AppCount = len(list)
		if len(list) > 8 {
			list = list[:8]
		}
		sum.Apps = list
		all := sum.Pods
		all.Healthy += sum.Workloads.Healthy
		all.Warning += sum.Workloads.Warning
		all.Error += sum.Workloads.Error
		all.Unknown += sum.Workloads.Unknown
		sum.Health = all.Worst()
		ov.Namespaces = append(ov.Namespaces, *sum)
	}
	sort.Slice(ov.Namespaces, func(i, j int) bool { return ov.Namespaces[i].Namespace < ov.Namespaces[j].Namespace })

	overviewMu.Lock()
	if overviewCache[s] == nil {
		overviewCache[s] = map[string]overviewEntry{}
	}
	overviewCache[s][cluster] = overviewEntry{at: s.now(), ov: ov}
	overviewMu.Unlock()
	return ov, nil
}

func shortErr(err error) string {
	if apierrors.IsForbidden(err) {
		return "access denied"
	}
	return err.Error()
}
