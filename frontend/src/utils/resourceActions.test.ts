import { describe, expect, it, vi } from 'vitest';
import { getAvailableActions, handleActionSelect } from './resourceActions';

const logsRouteParams = (kind: string) => {
  const openBottomTab = vi.fn();
  return {
    openBottomTab,
    params: {
      action: 'logs',
      item: { kind, metadata: { name: 'x', namespace: 'ns' }, spec: {} },
      currentTab: { cluster: 'c1' },
      selectedNode: { data: { kind: kind.toLowerCase() } },
      loadDetails: vi.fn().mockResolvedValue(undefined),
      openBottomTab,
      restartResource: vi.fn(),
      triggerCronJob: vi.fn(),
      cordonNode: vi.fn(),
      reloadListItems: vi.fn(),
      setRestartingItems: vi.fn(),
      setScaleDialog: vi.fn(),
      setSelectedResources: vi.fn(),
      setShowDeleteConfirm: vi.fn(),
      setDrainDialog: vi.fn(),
      setTaintDialog: vi.fn(),
      getCurrentTabState: vi.fn(),
      getResourceKey: vi.fn(),
    } as any,
  };
};

describe('workload logs actions', () => {
  it.each(['deployments', 'statefulsets', 'daemonsets', 'replicasets'])(
    '%s offers a Logs action',
    (kind) => {
      expect(getAvailableActions({ kind })).toContain('Logs');
    },
  );

  it.each(['Deployment', 'StatefulSet', 'DaemonSet', 'ReplicaSet', 'Job'])(
    'logs on %s opens deployment-logs tab',
    async (kind) => {
      const { openBottomTab, params } = logsRouteParams(kind);
      await handleActionSelect(params);
      expect(openBottomTab).toHaveBeenCalledWith(
        'deployment-logs',
        expect.objectContaining({ kind }),
        params.currentTab,
      );
    },
  );

  it('logs on Pod opens plain logs tab', async () => {
    const { openBottomTab, params } = logsRouteParams('Pod');
    await handleActionSelect(params);
    expect(openBottomTab).toHaveBeenCalledWith(
      'logs',
      expect.objectContaining({ kind: 'Pod' }),
      params.currentTab,
    );
  });
});

describe('edit and shell actions', () => {
  it.each(['edit', 'shell'])('%s uses the freshly loaded details, not the stale tab detailData', async (action) => {
    const { openBottomTab, params } = logsRouteParams('Pod');
    const fresh = { kind: 'Pod', metadata: { name: 'x', namespace: 'ns' } };
    params.action = action;
    params.loadDetails = vi.fn().mockResolvedValue(fresh);
    params.getCurrentTabState = vi.fn().mockReturnValue({ detailData: { kind: 'Pod', metadata: { name: 'previous', namespace: 'ns' } } });
    await handleActionSelect(params);
    expect(openBottomTab).toHaveBeenCalledWith(action, fresh, params.currentTab);
  });
});
