import { describe, it, expect } from 'vitest';
import { createNavigationHandlers } from './keyboardShortcuts';

const pod = (name: string, extra: any = {}) => ({
  name,
  namespace: 'ns',
  ...extra,
});
const keyOf = (item: any) => `${item.namespace}/${item.name}`;

describe('createNavigationHandlers', () => {
  it('moves from the selected row after a watch event replaced its object', () => {
    const items = [pod('a'), pod('b'), pod('c')];
    const selected = items[1];
    // The list now holds a new object for the selected row.
    const updated = [items[0], pod('b', { phase: 'Failed' }), items[2]];
    const picked: any[] = [];
    const nav = createNavigationHandlers(
      'list',
      updated,
      selected,
      (item) => picked.push(item),
      keyOf,
    );
    nav.j();
    nav.k();
    expect(picked.map((i) => i.name)).toEqual(['c', 'a']);
  });

  it('starts from the first row when nothing is selected', () => {
    const items = [pod('a'), pod('b')];
    const picked: any[] = [];
    createNavigationHandlers(
      'list',
      items,
      null,
      (item) => picked.push(item),
      keyOf,
    ).j();
    expect(picked.map((i) => i.name)).toEqual(['a']);
  });

  it('does nothing outside the list', () => {
    const picked: any[] = [];
    createNavigationHandlers(
      'tree',
      [pod('a')],
      null,
      (item) => picked.push(item),
      keyOf,
    ).j();
    expect(picked).toEqual([]);
  });
});
