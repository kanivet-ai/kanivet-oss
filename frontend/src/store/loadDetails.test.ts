import { describe, it, expect, vi } from 'vitest';
import { create } from 'zustand';

const api = vi.hoisted(() => ({
  calls: [] as string[],
  details: null as null | ((value: any) => void),
}));

vi.mock('../services/api', () => ({
  default: {
    getResourceDetails: () => {
      api.calls.push('details');
      return new Promise((resolve) => {
        api.details = resolve;
      });
    },
    getResourceEvents: async () => {
      api.calls.push('events');
      return [{ reason: 'Started' }];
    },
  },
}));
vi.mock('../services/islandNotifications', () => ({
  notifyDrainComplete: () => {},
  notifyRolloutComplete: () => {},
}));

const { createResourceSlice } = await import('./resourceSlice');

describe('loadDetails', () => {
  it('asks for the events together with the details and shows both', async () => {
    const pod = { kind: 'Pod', metadata: { name: 'api-0', namespace: 'ns' } };
    const store = create<any>()((set, get, storeApi) => ({
      activeTabs: [
        {
          id: 'c1',
          name: 'c1',
          state: {
            detailData: pod,
            detailTabs: [{ id: 'dt-1', item: pod }],
            activeDetailTab: 'dt-1',
          },
        },
      ],
      ...createResourceSlice(set, get, storeApi),
    }));
    const loading = store
      .getState()
      .loadDetails(
        'c1',
        { group: '', version: 'v1', kind: 'Pod' },
        { name: 'api-0', namespace: 'ns' },
      );
    // The events request is out before the details have answered.
    expect([...api.calls].sort()).toEqual(['details', 'events']);
    api.details!({ ...pod, spec: { containers: [] } });
    await loading;
    await Promise.resolve();
    const state = store.getState().activeTabs[0].state;
    expect(state.detailData.spec).toEqual({ containers: [] });
    expect(state.detailData.events).toEqual([{ reason: 'Started' }]);
    expect(state.detailTabs[0].item.events).toEqual([{ reason: 'Started' }]);
  });
});
