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
      addToast: vi.fn(),
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

describe('failed operational actions surface an error toast', () => {
  const failingParams = (action: string, overrides: Record<string, any>) => {
    const { params } = logsRouteParams('Deployment');
    const addToast = vi.fn();
    Object.assign(params, {
      action,
      addToast,
      item: { name: 'api', namespace: 'ns' },
      ...overrides,
    });
    return { params, addToast };
  };

  it('restart failure reports the backend error body', async () => {
    const err = { response: { data: { error: 'deployments.apps "api" not found' } } };
    const { params, addToast } = failingParams('restart', {
      restartResource: vi.fn().mockRejectedValue(err),
    });
    await handleActionSelect(params);
    expect(addToast).toHaveBeenCalledWith({
      type: 'error',
      message: 'Failed to restart api: deployments.apps "api" not found',
    });
  });

  it('trigger failure falls back to the transport message', async () => {
    const { params, addToast } = failingParams('trigger', {
      triggerCronJob: vi.fn().mockRejectedValue(new Error('Network Error')),
    });
    await handleActionSelect(params);
    expect(addToast).toHaveBeenCalledWith({
      type: 'error',
      message: 'Failed to trigger api: Network Error',
    });
  });

  it.each([
    ['cordon', 'Failed to cordon api: forbidden'],
    ['uncordon', 'Failed to uncordon api: forbidden'],
  ])('%s failure toasts', async (action, message) => {
    const { params, addToast } = failingParams(action, {
      cordonNode: vi.fn().mockRejectedValue(new Error('forbidden')),
    });
    await handleActionSelect(params);
    expect(addToast).toHaveBeenCalledWith({ type: 'error', message });
  });

  it('a successful restart does not toast', async () => {
    const { params, addToast } = failingParams('restart', {
      restartResource: vi.fn().mockResolvedValue(undefined),
    });
    await handleActionSelect(params);
    expect(addToast).not.toHaveBeenCalled();
  });
});

describe('crossplane trace action', () => {
  const crossplaneNode = {
    id: 'crossplane-s3.aws.upbound.io-v1beta1-buckets',
    data: { group: 's3.aws.upbound.io', version: 'v1beta1', kind: 'Bucket', name: 'buckets' },
  };

  it('offers Trace for resources under the Crossplane category', () => {
    expect(getAvailableActions({ name: 'b1' }, crossplaneNode)[0]).toBe('Trace');
  });

  it('does not offer Trace elsewhere', () => {
    expect(getAvailableActions({ kind: 'Pod' }, { id: 'workloads-core-v1-pods', data: { kind: 'pods' } })).not.toContain('Trace');
  });

  it('opens a trace tab built from the selected node when the row lacks apiVersion and kind', async () => {
    const { openBottomTab, params } = logsRouteParams('Bucket');
    Object.assign(params, { action: 'Trace', item: { name: 'b1', namespace: '' }, selectedNode: crossplaneNode });
    await handleActionSelect(params);
    expect(openBottomTab).toHaveBeenCalledWith(
      'trace',
      { apiVersion: 's3.aws.upbound.io/v1beta1', kind: 'Bucket', metadata: { name: 'b1', namespace: '' } },
      params.currentTab,
    );
  });
});
