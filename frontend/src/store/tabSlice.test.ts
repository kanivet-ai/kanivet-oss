import { describe, it, expect, vi } from 'vitest';
import { create } from 'zustand';

const unloadHandlers = vi.hoisted(() => {
  const handlers: Array<() => void> = [];
  const g = globalThis as any;
  g.window = g;
  g.addEventListener = (type: string, fn: () => void) => {
    if (type === 'beforeunload') handlers.push(fn);
  };
  const stored = new Map<string, string>();
  g.localStorage = {
    getItem: (k: string) => stored.get(k) ?? null,
    setItem: (k: string, v: string) => stored.set(k, v),
    removeItem: (k: string) => stored.delete(k),
  };
  return handlers;
});

vi.mock('../services/api', () => ({ default: {} }));

const { createTabSlice } = await import('./tabSlice');
const { installPersistence } = await import('./persistence');
const { createInitialTabState, rebuildTabIndex } = await import('./utils');

describe('tab state persistence', () => {
  it('writes a pending tab state snapshot when the window unloads', () => {
    vi.useFakeTimers();
    const store = create<any>()((set, get, api) => ({
      bottomTabs: [],
      activeBottomTab: null,
      ...createTabSlice(set, get, api),
    }));
    installPersistence(store, { alreadyHydrated: true });
    const tabs = [{ id: 'c1', name: 'c1', state: createInitialTabState() }];
    store.setState({
      activeTabs: tabs,
      tabIndexMap: rebuildTabIndex(tabs),
      currentTab: 'c1',
    });
    localStorage.removeItem('kanivet.tabstate.c1');

    store
      .getState()
      .updateCurrentTabState({ activeResourceListTab: 'list-pods' });
    expect(localStorage.getItem('kanivet.tabstate.c1')).toBeNull();

    for (const handler of unloadHandlers) handler();
    expect(
      JSON.parse(localStorage.getItem('kanivet.tabstate.c1')!)
        .activeResourceListTab,
    ).toBe('list-pods');
    vi.useRealTimers();
  });

  // A detail tab holds the object loadDetails answered with: a Secret's data
  // included. localStorage is a file on disk, so the snapshot keeps only what
  // finds the object again.
  it('does not write a detail tab object, such as a Secret with its data, to localStorage', () => {
    vi.useFakeTimers();
    const store = create<any>()((set, get, api) => ({
      bottomTabs: [],
      activeBottomTab: null,
      ...createTabSlice(set, get, api),
    }));
    installPersistence(store, { alreadyHydrated: true });
    const secret = {
      kind: 'Secret',
      apiVersion: 'v1',
      metadata: { name: 'db', namespace: 'prod', uid: 'u1', annotations: { 'kubectl.kubernetes.io/last-applied-configuration': '{"data":{"password":"aHVudGVyMg=="}}' } },
      data: { password: 'aHVudGVyMg==' },
    };
    const state = { ...createInitialTabState(), detailTabs: [{ id: 'dt-1', title: 'db', item: secret, cluster: 'c2', resource: { name: 'secrets', group: '', version: 'v1', kind: 'Secret', namespaced: true }, isPinned: false }], activeDetailTab: 'dt-1' };
    const tabs = [{ id: 'c2', name: 'c2', state }];
    store.setState({ activeTabs: tabs, tabIndexMap: rebuildTabIndex(tabs), currentTab: 'c2' });

    store.getState().updateCurrentTabState({ activeResourceListTab: 'list-secrets' });
    for (const handler of unloadHandlers) handler();

    const saved = localStorage.getItem('kanivet.tabstate.c2')!;
    expect(saved).not.toContain('aHVudGVyMg==');
    expect(JSON.parse(saved).detailTabs[0].item).toEqual({ name: 'db', namespace: 'prod', uid: 'u1', kind: 'Secret', apiVersion: 'v1' });
    vi.useRealTimers();
  });
});
