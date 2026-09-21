import { describe, expect, it } from 'vitest';
import { initialCenterLayout, resolvePaneId } from './centerPaneLayout';

const SBX = 'arn:aws:eks:eu-north-1:243517631187:cluster/sbx';

describe('initialCenterLayout', () => {
  it('gives a cluster with no saved layout a single root pane', () => {
    expect(initialCenterLayout(undefined, SBX)).toEqual({
      rootNode: { id: 'root', type: 'resourceList', tabId: SBX },
      focusedNodeId: 'root',
      nodeCounter: 1,
    });
  });

  it("restores a cluster's own layout, including a root pane left over from a closed split", () => {
    const layout = { id: 'pane-6', type: 'resourceList' as const, tabId: SBX, size: 50 };
    expect(initialCenterLayout({ centerPaneLayout: layout, focusedCenterPaneId: 'pane-6' }, SBX)).toEqual({
      rootNode: layout,
      focusedNodeId: 'pane-6',
      nodeCounter: 7,
    });
  });

  it('never focuses a pane the layout does not contain, or new tabs open where nothing renders them', () => {
    // The state a cluster is left in after borrowing another cluster's panes.
    const state = {
      centerPaneLayout: { id: 'root', type: 'resourceList' as const, tabId: SBX },
      focusedCenterPaneId: 'pane-6',
    };
    expect(initialCenterLayout(state, SBX).focusedNodeId).toBe('root');
  });

  it('focuses the first pane of a split when the saved focus is gone', () => {
    const state = {
      centerPaneLayout: {
        id: 'pane-3',
        type: 'split' as const,
        direction: 'horizontal' as const,
        children: [
          { id: 'pane-1', type: 'resourceList' as const, tabId: SBX, size: 50 },
          { id: 'pane-2', type: 'resourceList' as const, tabId: SBX, size: 50 },
        ],
      },
      focusedCenterPaneId: 'pane-9',
    };
    const result = initialCenterLayout(state, SBX);
    expect(result.focusedNodeId).toBe('pane-1');
    expect(result.nodeCounter).toBe(4);
  });
});

describe('resolvePaneId', () => {
  const closedSplit = { id: 'pane-6', type: 'resourceList' as const, tabId: SBX, size: 50 };

  it('keeps a pane the layout contains', () => {
    expect(resolvePaneId(closedSplit, 'pane-6')).toBe('pane-6');
  });

  it('does not assume a pane called root exists', () => {
    expect(resolvePaneId(closedSplit, 'root')).toBe('pane-6');
    expect(resolvePaneId(closedSplit, undefined)).toBe('pane-6');
    expect(resolvePaneId(closedSplit, 'pane-2')).toBe('pane-6');
  });

  it('uses root while a cluster has no layout yet', () => {
    expect(resolvePaneId(undefined, undefined)).toBe('root');
    expect(resolvePaneId(undefined, 'pane-6')).toBe('root');
  });
});
