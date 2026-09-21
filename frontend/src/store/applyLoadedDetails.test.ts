import { describe, it, expect } from 'vitest';
import { applyLoadedDetails } from './applyLoadedDetails';

const pod = (name: string, namespace = 'ns', extra: any = {}) => ({
  kind: 'Pod',
  metadata: { name, namespace },
  ...extra,
});

const tab = (id: string, state: any = {}) => ({
  id,
  name: id,
  state: { detailData: null, detailTabs: [], activeDetailTab: null, isDetailsPanelCollapsed: true, ...state },
}) as any;

const full = pod('api-0', 'ns', { spec: { containers: [{ name: 'api' }] } });

describe('applyLoadedDetails', () => {
  it('writes the details into the cluster they were loaded for, not another tab', () => {
    // The request was made on cluster-a; by the time it answers the user is on cluster-b.
    const tabs = [
      tab('cluster-a', { detailData: pod('api-0'), detailTabs: [{ id: 'dt-1', item: pod('api-0') }], activeDetailTab: 'dt-1' }),
      tab('cluster-b', { detailData: pod('worker-3'), detailTabs: [{ id: 'dt-9', item: pod('worker-3') }], activeDetailTab: 'dt-9' }),
    ];
    const next = applyLoadedDetails(tabs, 'cluster-a', full);
    expect(next[0].state.detailData).toBe(full);
    expect(next[0].state.detailTabs[0].item).toBe(full);
    expect(next[0].state.isDetailsPanelCollapsed).toBe(false);
    expect(next[1]).toBe(tabs[1]);
  });

  it('does not put one cluster\'s details into a tab that has no detail tab open', () => {
    const tabs = [
      tab('cluster-a', { detailData: pod('api-0'), detailTabs: [{ id: 'dt-1', item: pod('api-0') }], activeDetailTab: 'dt-1' }),
      tab('cluster-b'),
    ];
    const next = applyLoadedDetails(tabs, 'cluster-a', full);
    expect(next[1].state.detailData).toBeNull();
    expect(next[0].state.detailData).toBe(full);
  });

  it('sets detailData when the cluster has no active detail tab', () => {
    const tabs = [tab('cluster-a')];
    const next = applyLoadedDetails(tabs, 'cluster-a', full);
    expect(next[0].state.detailData).toBe(full);
    expect(next[0].state.isDetailsPanelCollapsed).toBe(false);
  });

  it('ignores details for a resource other than the one the active detail tab shows', () => {
    const tabs = [
      tab('cluster-a', { detailData: pod('other'), detailTabs: [{ id: 'dt-1', item: pod('other') }], activeDetailTab: 'dt-1' }),
    ];
    expect(applyLoadedDetails(tabs, 'cluster-a', full)).toBe(tabs);
  });

  it('returns the tabs untouched when the cluster tab was closed meanwhile', () => {
    const tabs = [tab('cluster-b')];
    expect(applyLoadedDetails(tabs, 'cluster-a', full)).toBe(tabs);
  });
});
