export interface ItemRef {
  name: string;
  namespace?: string;
}

interface WaitableStore {
  getState: () => any;
  subscribe: (listener: (state: any) => void) => () => void;
}

const WAIT_MS = 20000;
// After the list's first full answer, a row still missing is given this long to
// show up in a later batch before it is taken as gone.
const GONE_GRACE_MS = 2000;

export type WaitResult =
  | { status: 'found'; item: any }
  /** The list loaded and has no such row (deleted while the app was closed). */
  | { status: 'gone' }
  /** The user went elsewhere, so nothing should be selected for them. */
  | { status: 'moved' };

// The same list, whichever code selected the node: the sidebar selects it
// again, with a new object, when its tree loads.
const nodeKey = (node: any): string | null => {
  if (!node) return null;
  const data = node.data;
  return node.type === 'resource' && data?.name
    ? `resource:${data.group || ''}/${data.version}/${data.name}`
    : `${node.type}:${node.id}`;
};

const sameItem = (item: any, ref: ItemRef) =>
  item?.name === ref.name && (item?.namespace || '') === (ref.namespace || '');

/**
 * Resolves with the row of the shown cluster's list that `ref` names, once the
 * list has it. A list streams in after it is requested, so a row looked up
 * right after the request is usually not there yet.
 *
 * Reports 'moved' when the user has gone elsewhere (another cluster tab,
 * another list, a row of their own choosing), and 'gone' when the list has
 * loaded without the row or never shows it.
 */
export const waitForListItem = (
  store: WaitableStore,
  cluster: string,
  ref: ItemRef,
  timeoutMs = WAIT_MS,
): Promise<WaitResult> =>
  new Promise((resolve) => {
    const startNode = nodeKey(store.getState().getCurrentTabState?.()?.selectedNode);
    let lastItems: any[] | undefined;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let unsubscribe: (() => void) | undefined;

    let graceStarted = false;

    const finish = (result: WaitResult) => {
      if (timer) clearTimeout(timer);
      unsubscribe?.();
      resolve(result);
    };

    // true: stop waiting without a row.
    const moved = (state: any, tabState: any) =>
      state.currentTab !== cluster ||
      !tabState ||
      nodeKey(tabState.selectedNode) !== startNode ||
      (!!tabState.selectedItem && !sameItem(tabState.selectedItem, ref));

    const check = (state: any) => {
      const tabState = state.getCurrentTabState?.();
      if (moved(state, tabState)) return finish({ status: 'moved' });
      const items: any[] = tabState.listItems || [];
      // Rows arrive in batches; only a new list can hold the row.
      if (items === lastItems) return;
      lastItems = items;
      const match = items.find((item) => sameItem(item, ref));
      if (match) return finish({ status: 'found', item: match });
      if (tabState.hasReceivedInitialListData && !graceStarted) {
        graceStarted = true;
        if (timer) clearTimeout(timer);
        timer = setTimeout(() => finish({ status: 'gone' }), Math.min(GONE_GRACE_MS, timeoutMs));
      }
    };

    timer = setTimeout(() => finish({ status: 'gone' }), timeoutMs);
    unsubscribe = store.subscribe((state) => check(state));
    check(store.getState());
  });

/** Resolves true when the selected row is cleared without the user choosing
 * another (a load resetting the list), false when the window ends first or the
 * user moved on. */
const selectionLost = (store: WaitableStore, cluster: string, ref: ItemRef, windowMs: number): Promise<boolean> =>
  new Promise((resolve) => {
    if (windowMs <= 0) return resolve(false);
    const startNode = nodeKey(store.getState().getCurrentTabState?.()?.selectedNode);
    let unsubscribe: (() => void) | undefined;
    const timer = setTimeout(() => done(false), windowMs);
    const done = (lost: boolean) => {
      clearTimeout(timer);
      unsubscribe?.();
      resolve(lost);
    };
    unsubscribe = store.subscribe((state) => {
      const tabState = state.getCurrentTabState?.();
      if (state.currentTab !== cluster || !tabState || nodeKey(tabState.selectedNode) !== startNode) return done(false);
      if (!tabState.selectedItem) return done(true);
      if (!sameItem(tabState.selectedItem, ref)) done(false);
    });
  });

/**
 * Selects the saved row once the list has it, and keeps it selected through the
 * loads a restore sets off: another load of the same list can reset it right
 * after, which would silently drop the selection. Stops for good when the user
 * picks another row or goes elsewhere.
 */
export const restoreSelectedRow = async (
  store: WaitableStore,
  cluster: string,
  ref: ItemRef,
  apply: (item: any) => Promise<void> | void,
  { windowMs = 12000, waitMs = WAIT_MS }: { windowMs?: number; waitMs?: number } = {},
): Promise<WaitResult['status']> => {
  const deadline = Date.now() + windowMs;
  for (let round = 0; round < 4; round++) {
    const result = await waitForListItem(store, cluster, ref, waitMs);
    if (result.status !== 'found') return result.status;
    await apply(result.item);
    if (!(await selectionLost(store, cluster, ref, deadline - Date.now()))) return 'found';
  }
  return 'found';
};
