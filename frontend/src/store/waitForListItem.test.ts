import { describe, expect, it, vi } from 'vitest';
import { create } from 'zustand';
import { restoreSelectedRow, waitForListItem } from './waitForListItem';

const node = { id: 'n', label: 'pods', type: 'resource', data: {} };
const makeStore = () => {
  const store = create<any>()((_set, get) => ({
    currentTab: 'c1',
    tab: { selectedNode: node, selectedItem: null, listItems: [] as any[] },
    getCurrentTabState: () => get().tab,
  }));
  const setTab = (patch: any) => store.setState((s: any) => ({ tab: { ...s.tab, ...patch } }));
  return { store, setTab };
};
const ref = { name: 'web-1', namespace: 'prod' };
const row = { name: 'web-1', namespace: 'prod', kind: 'Pod' };

describe('waiting for a restored row', () => {
  it('finds a row that is already in the list', async () => {
    const { store, setTab } = makeStore();
    setTab({ listItems: [row] });
    expect(await waitForListItem(store, 'c1', ref)).toEqual({ status: 'found', item: row });
  });

  it('finds the row once the list streams it in', async () => {
    const { store, setTab } = makeStore();
    const pending = waitForListItem(store, 'c1', ref);
    setTab({ listItems: [{ name: 'other', namespace: 'prod' }] });
    setTab({ listItems: [{ name: 'other', namespace: 'prod' }, row] });
    expect(await pending).toEqual({ status: 'found', item: row });
  });

  it('gives up when the user selected another row', async () => {
    const { store, setTab } = makeStore();
    const pending = waitForListItem(store, 'c1', ref);
    setTab({ selectedItem: { name: 'mine', namespace: 'prod' } });
    setTab({ listItems: [row] });
    expect(await pending).toEqual({ status: 'moved' });
  });

  it('gives up when the user moved to another list or cluster', async () => {
    const a = makeStore();
    const pendingList = waitForListItem(a.store, 'c1', ref);
    a.setTab({ selectedNode: { ...node, id: 'other' } });
    expect(await pendingList).toEqual({ status: 'moved' });

    const b = makeStore();
    const pendingCluster = waitForListItem(b.store, 'c1', ref);
    b.store.setState({ currentTab: 'c2' });
    expect(await pendingCluster).toEqual({ status: 'moved' });
  });

  it('reports a row that never shows up as gone after the timeout', async () => {
    vi.useFakeTimers();
    const { store } = makeStore();
    const pending = waitForListItem(store, 'c1', ref, 1000);
    vi.advanceTimersByTime(1001);
    expect(await pending).toEqual({ status: 'gone' });
    vi.useRealTimers();
  });

  it('reports a row as gone soon after the list has loaded without it', async () => {
    vi.useFakeTimers();
    const { store, setTab } = makeStore();
    const pending = waitForListItem(store, 'c1', ref);
    setTab({ listItems: [{ name: 'other', namespace: 'prod' }], hasReceivedInitialListData: true });
    vi.advanceTimersByTime(2001);
    expect(await pending).toEqual({ status: 'gone' });
    vi.useRealTimers();
  });

  it('still finds a row that arrives in a later batch of a loaded list', async () => {
    vi.useFakeTimers();
    const { store, setTab } = makeStore();
    const pending = waitForListItem(store, 'c1', ref);
    setTab({ listItems: [{ name: 'other', namespace: 'prod' }], hasReceivedInitialListData: true });
    vi.advanceTimersByTime(1000);
    setTab({ listItems: [{ name: 'other', namespace: 'prod' }, row] });
    expect(await pending).toEqual({ status: 'found', item: row });
    vi.useRealTimers();
  });
});

describe('keeping the restored row selected', () => {
  it('is not cancelled when the sidebar selects the same list again', async () => {
    const { store, setTab } = makeStore();
    const pending = waitForListItem(store, 'c1', ref);
    setTab({ selectedNode: { ...node } });
    setTab({ listItems: [row] });
    expect(await pending).toEqual({ status: 'found', item: row });
  });

  it('selects again when another load resets the list right after', async () => {
    const { store, setTab } = makeStore();
    setTab({ listItems: [row] });
    const applied: any[] = [];
    const done = restoreSelectedRow(store, 'c1', ref, (item) => {
      applied.push(item);
      setTab({ selectedItem: item });
    });
    await Promise.resolve();
    await Promise.resolve();
    expect(applied).toHaveLength(1);
    // The sidebar's load of the same list clears the rows and the selection.
    setTab({ listItems: [], selectedItem: null });
    setTab({ listItems: [row] });
    await new Promise((resolve) => setTimeout(resolve, 5));
    expect(applied).toHaveLength(2);
    // Now it holds; choosing another row ends the watch.
    setTab({ selectedItem: { name: 'mine', namespace: 'prod' } });
    expect(await done).toBe('found');
  });

  it('leaves the user alone once they chose another row', async () => {
    const { store, setTab } = makeStore();
    setTab({ listItems: [row] });
    let calls = 0;
    const done = restoreSelectedRow(store, 'c1', ref, () => {
      calls++;
      setTab({ selectedItem: row });
    });
    await new Promise((resolve) => setTimeout(resolve, 5));
    setTab({ selectedItem: { name: 'other', namespace: 'prod' } });
    expect(await done).toBe('found');
    setTab({ selectedItem: null });
    await new Promise((resolve) => setTimeout(resolve, 5));
    expect(calls).toBe(1);
  });
});
