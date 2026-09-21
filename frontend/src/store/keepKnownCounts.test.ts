import { describe, it, expect } from 'vitest';
import { keepKnownCounts } from './keepKnownCounts';

const res = (id: string, count?: number, children?: any[]) =>
  ({ id, label: id, type: 'resource', count, ...(children && { children }) }) as any;

describe('keepKnownCounts', () => {
  it('keeps the count a node already had while its refresh is pending', () => {
    const existing = [res('workloads-core-v1-pods', 780), res('workloads-apps-v1-deployments', 214)];
    const placeholders = [res('workloads-core-v1-pods'), res('workloads-apps-v1-deployments')];
    expect(keepKnownCounts(existing, placeholders).map((n) => n.count)).toEqual([780, 214]);
  });

  it('never overrides a count the new nodes already carry', () => {
    const next = keepKnownCounts([res('pods', 780)], [res('pods', 12)]);
    expect(next[0].count).toBe(12);
  });

  it('leaves nodes it has not seen before without a count', () => {
    const next = keepKnownCounts([res('pods', 780)], [res('pods'), res('jobs')]);
    expect(next.map((n) => n.count)).toEqual([780, undefined]);
  });

  it('carries counts through grouped children', () => {
    const existing = [res('crossplane-s3-v1', 3, [res('buckets', 3)])];
    const next = keepKnownCounts(existing, [res('crossplane-s3-v1', undefined, [res('buckets')])]);
    expect(next[0].count).toBe(3);
    expect(next[0].children![0].count).toBe(3);
  });

  it('returns the new nodes as they are when nothing was loaded before', () => {
    const placeholders = [res('pods')];
    expect(keepKnownCounts(undefined, placeholders)).toBe(placeholders);
  });
});
