package nats

import (
	"context"
	"fmt"

	"github.com/nats-io/nats.go/jetstream"
)

// ListKVBuckets lists KV bucket names using the real KV API, not the KV_
// stream-name convention the tier-1 tree view uses for labeling - this is
// authoritative, the naming convention is only a hint.
func (p *LivePool) ListKVBuckets(ctx context.Context, cluster string) ([]string, error) {
	lc, err := p.Get(ctx, cluster)
	if err != nil {
		return nil, err
	}
	lister := lc.js.KeyValueStoreNames(ctx)
	var names []string
	for name := range lister.Name() {
		names = append(names, name)
	}
	if err := lister.Error(); err != nil {
		return nil, fmt.Errorf("list KV buckets: %w", err)
	}
	return names, nil
}

// ListKVKeys lists every key currently in bucket. Read-only.
func (p *LivePool) ListKVKeys(ctx context.Context, cluster, bucket string) ([]string, error) {
	kv, err := p.openKV(ctx, cluster, bucket)
	if err != nil {
		return nil, err
	}
	keys, err := kv.Keys(ctx)
	if err != nil {
		return nil, fmt.Errorf("list keys in bucket %s: %w", bucket, err)
	}
	return keys, nil
}

// GetKVEntry reads a key's current value and revision. Read-only (kv.Get,
// never Put/Delete/Purge/Create/Update).
func (p *LivePool) GetKVEntry(ctx context.Context, cluster, bucket, key string) (jetstream.KeyValueEntry, error) {
	kv, err := p.openKV(ctx, cluster, bucket)
	if err != nil {
		return nil, err
	}
	entry, err := kv.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("get key %s in bucket %s: %w", key, bucket, err)
	}
	return entry, nil
}

// GetKVHistory reads every past revision of a key. Read-only.
func (p *LivePool) GetKVHistory(ctx context.Context, cluster, bucket, key string) ([]jetstream.KeyValueEntry, error) {
	kv, err := p.openKV(ctx, cluster, bucket)
	if err != nil {
		return nil, err
	}
	history, err := kv.History(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("get history for key %s in bucket %s: %w", key, bucket, err)
	}
	return history, nil
}

func (p *LivePool) openKV(ctx context.Context, cluster, bucket string) (jetstream.KeyValue, error) {
	lc, err := p.Get(ctx, cluster)
	if err != nil {
		return nil, err
	}
	kv, err := lc.js.KeyValue(ctx, bucket)
	if err != nil {
		return nil, fmt.Errorf("open KV bucket %s: %w", bucket, err)
	}
	return kv, nil
}
