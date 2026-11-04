import { describe, expect, it } from 'vitest';
import type { ChangeEntry } from '../types/changes';
import { apiVersionOf, dayLabel, defaultBaseline, filterHistory, groupByDay, primaryCategory } from './changes';

const ref = { cluster: 'c', group: 'apps', version: 'v1', kind: 'Deployment', namespace: 'prod', name: 'web' };
const e = (id: string, source: ChangeEntry['source'], time: string, extra: Partial<ChangeEntry> = {}): ChangeEntry => ({
  id, source, time, subject: ref, title: id, categories: [], hasSnapshot: false, ...extra,
});

describe('changes utils', () => {
  const entries = [
    e('ev', 'event', '2026-11-04T10:00:00Z'),
    e('rs3', 'replicaset', '2026-11-04T09:00:00Z', { hasSnapshot: true, categories: ['rollout', 'image'] }),
    e('h2', 'helm', '2026-11-04T08:30:00Z', { hasSnapshot: true }),
    e('mf', 'managedfields', '2026-11-04T08:00:00Z'),
    e('rs2', 'replicaset', '2026-11-03T09:00:00Z', { hasSnapshot: true }),
  ];

  it('filters the timeline views', () => {
    expect(filterHistory(entries, 'revisions').map((x) => x.id)).toEqual(['rs3', 'h2', 'rs2']);
    expect(filterHistory(entries, 'edits').map((x) => x.id)).toEqual(['mf']);
    expect(filterHistory(entries, 'events').map((x) => x.id)).toEqual(['ev']);
    expect(filterHistory(entries, 'all')).toHaveLength(5);
  });

  it('diffs a revision against the previous one of the same source', () => {
    expect(defaultBaseline(entries, entries[1])?.id).toBe('rs2');
    expect(defaultBaseline(entries, entries[4])).toBeNull();
  });

  it('names an entry by its most specific category', () => {
    expect(primaryCategory(entries[1])).toBe('image');
    expect(primaryCategory(entries[0])).toBe('other');
  });

  it('groups by day', () => {
    const now = new Date('2026-11-04T12:00:00');
    expect(dayLabel(new Date('2026-11-04T08:00:00').toISOString(), now)).toBe('Today');
    expect(dayLabel(new Date('2026-11-03T08:00:00').toISOString(), now)).toBe('Yesterday');
    expect(dayLabel('0001-01-01T00:00:00Z', now)).toBe('Unknown time');
    const local = entries.map((x) => ({ ...x, time: new Date(x.time.replace('Z', '')).toISOString() }));
    expect(groupByDay(local, now).map((g) => g.entries.length)).toEqual([4, 1]);
  });

  it('builds apiVersion strings', () => {
    expect(apiVersionOf({ group: '', version: 'v1' })).toBe('v1');
    expect(apiVersionOf({ group: 'apps', version: 'v1' })).toBe('apps/v1');
  });
});
