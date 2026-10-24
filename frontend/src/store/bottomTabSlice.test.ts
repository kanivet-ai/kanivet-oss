import { describe, expect, it } from 'vitest';
import { create } from 'zustand';
import { createBottomTabSlice } from './bottomTabSlice';

const makeStore = () => create<any>()((set, get, storeApi) => ({ ...createBottomTabSlice(set, get, storeApi) }));

const object = (kind: string, name: string, apiVersion = kind === 'Service' ? 'v1' : 'apps/v1') =>
  ({ kind, apiVersion, metadata: { name, namespace: 'ns' } });

describe('openBottomTab', () => {
  it('opens the editor of a Deployment, not the one of the Service with the same name', () => {
    const store = makeStore();
    store.getState().openBottomTab('edit', object('Service', 'api'), 'c1');
    store.getState().openBottomTab('edit', object('Deployment', 'api'), 'c1');
    const { bottomTabs, activeBottomTab } = store.getState();
    expect(bottomTabs).toHaveLength(2);
    expect(bottomTabs[0].id).not.toBe(bottomTabs[1].id);
    const active = bottomTabs.find((t: any) => t.id === activeBottomTab);
    expect(active.resource.kind).toBe('Deployment');
    // Both titles say which is which.
    expect(bottomTabs.map((t: any) => t.title)).toEqual(['Service api (ns)', 'Deployment api (ns)']);
  });

  it('keeps the logs of a StatefulSet apart from those of a Deployment of the same name', () => {
    const store = makeStore();
    store.getState().openBottomTab('deployment-logs', object('Deployment', 'db'), 'c1');
    store.getState().openBottomTab('deployment-logs', object('StatefulSet', 'db'), 'c1');
    expect(store.getState().bottomTabs.map((t: any) => t.resource.kind)).toEqual(['Deployment', 'StatefulSet']);
  });

  it('still goes back to the open tab of the same object', () => {
    const store = makeStore();
    store.getState().openBottomTab('edit', object('Deployment', 'api'), 'c1');
    const first = store.getState().activeBottomTab;
    store.getState().openBottomTab('logs', { kind: 'Pod', metadata: { name: 'p', namespace: 'ns' } }, 'c1');
    store.getState().openBottomTab('edit', object('Deployment', 'api'), 'c1');
    expect(store.getState().bottomTabs).toHaveLength(2);
    expect(store.getState().activeBottomTab).toBe(first);
    expect(store.getState().bottomTabs[0].title).toBe('api (ns)');
  });
});
