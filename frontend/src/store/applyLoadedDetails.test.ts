import { describe, it, expect } from 'vitest';
import { applyDetailsToTabs, applyEventsToTabs, applyLoadedDetails, markDetailsDeleted, refOf } from './applyLoadedDetails';

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

  it('does not reopen details after history navigates to a dashboard or list-only entry', () => {
    for (const type of ['overview', 'finops', 'resource']) {
      const tabs = [tab('cluster-a', { selectedNode: { type }, selectedItem: null })];
      expect(applyLoadedDetails(tabs, 'cluster-a', full)).toBe(tabs);
    }
  });

  it('ignores a late response for a different kind with the same name', () => {
    const tabs = [tab('cluster-a', { activeDetailTab: 'dt', detailTabs: [{ id: 'dt', item: { ...full, kind: 'Service' } }] })];
    expect(applyLoadedDetails(tabs, 'cluster-a', full)).toBe(tabs);
  });

  it('returns the tabs untouched when the cluster tab was closed meanwhile', () => {
    const tabs = [tab('cluster-b')];
    expect(applyLoadedDetails(tabs, 'cluster-a', full)).toBe(tabs);
  });
});

const object = (kind: string, name: string, { metadata, ...extra }: any = {}) => ({
  kind,
  apiVersion: kind === 'Deployment' ? 'apps/v1' : 'v1',
  metadata: { name, namespace: 'ns', uid: `${kind}-${name}-1`, ...(metadata || {}) },
  ...extra,
});
const detailTab = (id: string, item: any, extra: any = {}) => ({ id, item, cluster: 'c', isPinned: true, location: 'detail', ...extra });

describe('applyDetailsToTabs (a refresh)', () => {
  it('fills a tab opened on a typed list row, whose kind is the resource name', () => {
    // Typed list rows say kind 'pods' and no apiVersion; the details say Pod.
    const row = { kind: 'pods', name: 'api-0', namespace: 'ns', uid: 'Pod-api-0-1', metadata: { name: 'api-0', namespace: 'ns' } };
    const resource = { name: 'pods', kind: 'Pod', group: '', version: 'v1' };
    const tabs = [tab('c', { detailData: row, detailTabs: [detailTab('dt', row, { resource })], activeDetailTab: 'dt' })];
    const details = object('Pod', 'api-0', { spec: { containers: [{ name: 'api' }] } });
    const next = applyDetailsToTabs(tabs, 'c', details);
    expect(next[0].state.detailTabs[0].item.spec.containers).toHaveLength(1);
    expect(refOf(row).kind).toBe('');
    expect(refOf(row, resource).kind).toBe('Pod');
  });

  it('writes the refreshed object into the tab the panel renders, not only detailData', () => {
    const before = object('Pod', 'api-0', { status: { phase: 'Pending' } });
    const tabs = [tab('c', { detailData: before, detailTabs: [detailTab('dt', before)], activeDetailTab: 'dt' })];
    const after = object('Pod', 'api-0', { status: { phase: 'Running' } });
    const next = applyDetailsToTabs(tabs, 'c', after);
    expect(next[0].state.detailTabs[0].item.status.phase).toBe('Running');
    expect(next[0].state.detailData.status.phase).toBe('Running');
    // A background refresh does not open a collapsed panel.
    expect(next[0].state.isDetailsPanelCollapsed).toBe(true);
  });

  it('updates the tab it was asked for even when another one is on screen by the time it answers', () => {
    const asked = object('Pod', 'api-0');
    const shown = object('Pod', 'worker-1');
    const tabs = [tab('c', { detailData: shown, activeDetailTab: 'b', detailTabs: [detailTab('a', asked), detailTab('b', shown)] })];
    const next = applyDetailsToTabs(tabs, 'c', object('Pod', 'api-0', { status: { phase: 'Running' } }));
    expect(next[0].state.detailTabs[0].item.status.phase).toBe('Running');
    expect(next[0].state.detailTabs[1]).toBe(tabs[0].state.detailTabs[1]);
    expect(next[0].state.detailData).toBe(shown);
  });

  it('does not put a Deployment into the tab of a Service with the same name', () => {
    const service = object('Service', 'api');
    const tabs = [tab('c', { detailData: service, activeDetailTab: 'dt', detailTabs: [detailTab('dt', service)] })];
    expect(applyDetailsToTabs(tabs, 'c', object('Deployment', 'api'))).toBe(tabs);
  });

  it('keeps the events it has, and marks a recreated object instead of silently swapping it', () => {
    const events = [{ reason: 'Started' }];
    const pod = { ...object('Pod', 'api-0'), events };
    const tabs = [tab('c', { detailData: pod, activeDetailTab: 'dt', detailTabs: [detailTab('dt', pod)] })];
    const same = applyDetailsToTabs(tabs, 'c', object('Pod', 'api-0'));
    expect(same[0].state.detailTabs[0].item.events).toBe(events);
    expect(same[0].state.detailTabs[0].isRecreated).toBe(false);

    const recreated = applyDetailsToTabs(tabs, 'c', object('Pod', 'api-0', { metadata: { uid: 'new' } }));
    const dt = recreated[0].state.detailTabs[0];
    expect(dt.isRecreated).toBe(true);
    expect(dt.item.metadata.uid).toBe('new');
    // Those events were the predecessor's.
    expect(dt.item.events).toBeUndefined();
  });

  it('clears the deleted mark when the object is there again', () => {
    const pod = object('Pod', 'api-0');
    const tabs = [tab('c', { activeDetailTab: 'dt', detailTabs: [detailTab('dt', pod, { isDeleted: true })] })];
    expect(applyDetailsToTabs(tabs, 'c', { ...pod })[0].state.detailTabs[0].isDeleted).toBe(false);
  });
});

describe('markDetailsDeleted', () => {
  it('marks the tabs showing the object, and only those', () => {
    const tabs = [tab('c', { detailTabs: [
      detailTab('pod', { name: 'api', namespace: 'ns' }, { resource: { group: '', version: 'v1', kind: 'Pod' } }),
      detailTab('svc', object('Service', 'api')),
    ] })];
    const next = markDetailsDeleted(tabs, 'c', refOf({ name: 'api', namespace: 'ns' }, { group: '', version: 'v1', kind: 'Pod' }));
    expect(next[0].state.detailTabs[0].isDeleted).toBe(true);
    expect(next[0].state.detailTabs[1].isDeleted).toBeUndefined();
    expect(markDetailsDeleted(next, 'c', refOf({ name: 'api', namespace: 'ns' }, { group: '', version: 'v1', kind: 'Pod' }))).toBe(next);
  });
});

describe('applyEventsToTabs', () => {
  it('puts a Deployment\'s events on its tab, not on the Service of the same name', () => {
    const deployment = object('Deployment', 'api');
    const service = object('Service', 'api');
    const tabs = [tab('c', { detailData: service, activeDetailTab: 'svc', detailTabs: [detailTab('dep', deployment), detailTab('svc', service)] })];
    const events = [{ reason: 'ScalingReplicaSet' }];
    const next = applyEventsToTabs(tabs, 'c', deployment, events);
    expect(next[0].state.detailTabs[0].item.events).toBe(events);
    expect(next[0].state.detailTabs[1].item.events).toBeUndefined();
    expect(next[0].state.detailData.events).toBeUndefined();
  });

  it('does not give one incarnation of an object the events of another', () => {
    const pod = object('Pod', 'api-0', { metadata: { uid: 'new' } });
    const tabs = [tab('c', { detailTabs: [detailTab('dt', pod)] })];
    expect(applyEventsToTabs(tabs, 'c', object('Pod', 'api-0', { metadata: { uid: 'old' } }), [{ reason: 'Killing' }])).toBe(tabs);
  });

  it('replaces events with an empty list once they are gone', () => {
    const pod = { ...object('Pod', 'api-0'), events: [{ reason: 'Started' }] };
    const tabs = [tab('c', { detailTabs: [detailTab('dt', pod)] })];
    expect(applyEventsToTabs(tabs, 'c', pod, [])[0].state.detailTabs[0].item.events).toEqual([]);
  });
});
