import { describe, it, expect } from 'vitest';
import { sortItems } from './columnSorting';

describe('sortItems', () => {
  it('sorts release update timestamps chronologically in both directions without mutating input', () => {
    const items = [
      { name: 'alpha', updated: '2026-03-01T00:00:00Z' },
      { name: 'zulu', updated: '2026-01-01T00:00:00Z' },
      { name: 'middle', updated: '2026-02-01T00:00:00Z' },
      { name: 'offset', updated: '2026-02-01T00:30:00+02:00' },
    ];
    const original = [...items];
    expect(sortItems(items, { sortBy: 'updated', sortOrder: 'asc' }).map(i => i.name))
      .toEqual(['zulu', 'offset', 'middle', 'alpha']);
    expect(sortItems(items, { sortBy: 'updated', sortOrder: 'desc' }).map(i => i.name))
      .toEqual(['alpha', 'middle', 'offset', 'zulu']);
    expect(items).toEqual(original);
  });

  it('handles missing, invalid and equal update timestamps consistently', () => {
    const items = [
      { name: 'first', updated: '2026-01-01T00:00:00Z' },
      { name: 'missing', updated: '' },
      { name: 'invalid', updated: 'invalid' },
      { name: 'second', updated: '2026-01-01T00:00:00Z' },
    ];
    expect(sortItems(items, { sortBy: 'updated', sortOrder: 'asc' }).map(i => i.name))
      .toEqual(['missing', 'invalid', 'first', 'second']);
    expect(sortItems(items, { sortBy: 'updated', sortOrder: 'desc' }).map(i => i.name))
      .toEqual(['first', 'second', 'missing', 'invalid']);
  });

  it('sorts by age, newest first', () => {
    const items = [
      { name: 'old', creationTimestamp: '2026-01-01T00:00:00Z' },
      { name: 'new', creationTimestamp: '2026-03-01T00:00:00Z' },
      { name: 'mid', creationTimestamp: '2026-02-01T00:00:00Z' },
    ];
    expect(
      sortItems(items, { sortBy: 'age', sortOrder: 'desc' }).map((i) => i.name),
    ).toEqual(['new', 'mid', 'old']);
  });

  it('reads the sort key of an unchanged row only once', () => {
    let reads = 0;
    const row = (name: string, day: number) => {
      const item: any = { name };
      Object.defineProperty(item, 'creationTimestamp', {
        get: () => {
          reads++;
          return `2026-01-${String(day).padStart(2, '0')}T00:00:00Z`;
        },
      });
      return item;
    };
    const items = Array.from({ length: 20 }, (_, i) => row(`p${i}`, i + 1));
    sortItems(items, { sortBy: 'age', sortOrder: 'desc' });
    expect(reads).toBe(20);
    // One row replaced, as a watch update does: only it is read again.
    const next = [...items];
    next[3] = row('p3', 28);
    reads = 0;
    const sorted = sortItems(next, { sortBy: 'age', sortOrder: 'desc' });
    expect(reads).toBe(1);
    expect(sorted[0].name).toBe('p3');
  });
});
