import { describe, it, expect, vi } from 'vitest';

vi.mock('../store', () => ({ useStore: { getState: () => ({}) } }));
vi.mock('../utils/resourceIcons', () => ({
  getResourceIcon: () => null,
  getCategoryIcon: () => null,
}));
vi.mock('./icons/ExpandIcon', () => ({ default: () => null }));
vi.mock('./TreeNode.css', () => ({}));

const { createTreeCursor } = await import('./TreeNode');

describe('createTreeCursor', () => {
  it('tells its readers only about real moves', () => {
    const cursor = createTreeCursor();
    const seen: Array<[string | null, string | null]> = [];
    const unsubscribe = cursor.subscribe(() =>
      seen.push([cursor.focused, cursor.selected]),
    );
    cursor.set('pods', 'pods');
    cursor.set('pods', 'pods');
    cursor.set('services', 'pods');
    unsubscribe();
    cursor.set('nodes', 'nodes');
    expect(seen).toEqual([
      ['pods', 'pods'],
      ['services', 'pods'],
    ]);
  });
});
