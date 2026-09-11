package printercolumns

import (
	"context"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

const (
	positiveTTL  = 10 * time.Minute
	negativeTTL  = 30 * time.Second
	fetchTimeout = 3 * time.Second
)

var crdGVR = schema.GroupVersionResource{
	Group:    "apiextensions.k8s.io",
	Version:  "v1",
	Resource: "customresourcedefinitions",
}

// FetchCRD loads a CRD object by cluster and CRD name (plural.group).
type FetchCRD func(ctx context.Context, cluster, crdName string) (map[string]interface{}, error)

// DynamicGetter is the subset of the Kubernetes client used to read CRDs.
type DynamicGetter interface {
	GetDynamicClient(cluster string) (dynamic.Interface, error)
}

type cacheEntry struct {
	cols      []Column
	expiresAt time.Time
}

// Resolver caches CRD additionalPrinterColumns per cluster GVR.
type Resolver struct {
	fetch FetchCRD
	group singleflight.Group
	mu    sync.RWMutex
	cache map[string]cacheEntry
	now   func() time.Time
}

// NewResolver returns a resolver that fetches CRDs via fetch.
func NewResolver(fetch FetchCRD) *Resolver {
	return &Resolver{
		fetch: fetch,
		cache: make(map[string]cacheEntry),
		now:   time.Now,
	}
}

// FetchFromDynamic builds a FetchCRD that reads CRDs through a dynamic client.
func FetchFromDynamic(getter DynamicGetter) FetchCRD {
	return func(ctx context.Context, cluster, crdName string) (map[string]interface{}, error) {
		if getter == nil {
			return nil, nil
		}
		dyn, err := getter.GetDynamicClient(cluster)
		if err != nil {
			return nil, err
		}
		obj, err := dyn.Resource(crdGVR).Get(ctx, crdName, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		return obj.Object, nil
	}
}

// Columns returns printer columns for a GVR. Core types and fetch failures
// yield a nil slice so callers can fall back to generic STATUS/AGE columns.
func (r *Resolver) Columns(ctx context.Context, cluster string, gvr schema.GroupVersionResource) []Column {
	if r == nil || r.fetch == nil || gvr.Group == "" || gvr.Resource == "" {
		return nil
	}
	key := cluster + "|" + gvr.Group + "|" + gvr.Version + "|" + gvr.Resource
	if cols, hit := r.get(key); hit {
		return cols
	}

	v, _, _ := r.group.Do(key, func() (interface{}, error) {
		if cols, hit := r.get(key); hit {
			return cols, nil
		}
		cols := r.load(ctx, cluster, gvr)
		ttl := positiveTTL
		if cols == nil {
			ttl = negativeTTL
		}
		r.mu.Lock()
		r.cache[key] = cacheEntry{cols: cols, expiresAt: r.now().Add(ttl)}
		r.mu.Unlock()
		return cols, nil
	})
	cols, _ := v.([]Column)
	return cols
}

func (r *Resolver) get(key string) ([]Column, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.cache[key]
	if !ok || r.now().After(e.expiresAt) {
		return nil, false
	}
	return e.cols, true
}

func (r *Resolver) load(ctx context.Context, cluster string, gvr schema.GroupVersionResource) []Column {
	fetchCtx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	crd, err := r.fetch(fetchCtx, cluster, gvr.Resource+"."+gvr.Group)
	if err != nil || crd == nil {
		return nil
	}
	return ParseFromCRD(crd, gvr.Version)
}
