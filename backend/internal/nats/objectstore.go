package nats

import (
	"context"
	"fmt"

	"github.com/nats-io/nats.go/jetstream"
)

// ListObjectStores lists Object Store bucket names using the real API, not
// the OBJ_ stream-name convention - same reasoning as ListKVBuckets.
func (p *LivePool) ListObjectStores(ctx context.Context, cluster string) ([]string, error) {
	lc, err := p.Get(ctx, cluster)
	if err != nil {
		return nil, err
	}
	lister := lc.js.ObjectStoreNames(ctx)
	var names []string
	for name := range lister.Name() {
		names = append(names, name)
	}
	if err := lister.Error(); err != nil {
		return nil, fmt.Errorf("list object stores: %w", err)
	}
	return names, nil
}

// ListObjects lists every object's metadata in bucket. Read-only.
func (p *LivePool) ListObjects(ctx context.Context, cluster, bucket string) ([]*jetstream.ObjectInfo, error) {
	store, err := p.openObjectStore(ctx, cluster, bucket)
	if err != nil {
		return nil, err
	}
	infos, err := store.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list objects in bucket %s: %w", bucket, err)
	}
	return infos, nil
}

// GetObject opens an object for reading. Read-only (store.Get, never
// Put/Delete/AddLink/UpdateMeta). Caller must close the result.
func (p *LivePool) GetObject(ctx context.Context, cluster, bucket, name string) (jetstream.ObjectResult, error) {
	store, err := p.openObjectStore(ctx, cluster, bucket)
	if err != nil {
		return nil, err
	}
	result, err := store.Get(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("get object %s in bucket %s: %w", name, bucket, err)
	}
	return result, nil
}

func (p *LivePool) openObjectStore(ctx context.Context, cluster, bucket string) (jetstream.ObjectStore, error) {
	lc, err := p.Get(ctx, cluster)
	if err != nil {
		return nil, err
	}
	store, err := lc.js.ObjectStore(ctx, bucket)
	if err != nil {
		return nil, fmt.Errorf("open object store %s: %w", bucket, err)
	}
	return store, nil
}
