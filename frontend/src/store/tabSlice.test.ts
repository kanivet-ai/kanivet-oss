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
const { createInitialTabState, rebuildTabIndex } = await import('./utils');

describe('tab state persistence', () => {
  it('writes a pending tab state snapshot when the window unloads', () => {
    vi.useFakeTimers();
    const store = create<any>()((set, get, api) => ({
      ...createTabSlice(set, get, api),
    }));
    const tabs = [{ id: 'c1', name: 'c1', state: createInitialTabState() }];
    store.setState({
      activeTabs: tabs,
      tabIndexMap: rebuildTabIndex(tabs),
      currentTab: 'c1',
    });

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
});
