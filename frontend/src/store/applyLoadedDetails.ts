import type { DetailTab, Tab } from './types';

const nameOf = (item: any) => item?.metadata?.name || item?.name;
const namespaceOf = (item: any) => item?.metadata?.namespace || item?.namespace || '';

/** What tells one Kubernetes object from another in a cluster. */
export interface ObjectRef {
  name: string;
  namespace: string;
  kind: string;
  apiVersion: string;
  /** Tells a recreated object from the one it replaced. */
  uid: string;
}

const apiVersionOf = (resource: any): string =>
  resource?.version ? (resource.group ? `${resource.group}/${resource.version}` : resource.version) : '';

// openDetailTab names an object it knows nothing about 'Resource'.
const knownKind = (kind: any): string => (kind && kind !== 'Resource' ? kind : '');

/** The identity of `item`, filled in from the list `resource` it came from. */
export const refOf = (item: any, resource?: any): ObjectRef => ({
  name: nameOf(item) || '',
  namespace: namespaceOf(item),
  kind: knownKind(item?.kind) || knownKind(resource?.kind),
  apiVersion: item?.apiVersion || apiVersionOf(resource),
  uid: item?.metadata?.uid || item?.uid || '',
});

export const tabRef = (dt: DetailTab): ObjectRef => refOf(dt.item, dt.resource);

/**
 * Whether two references name the same object (any incarnation of it). A
 * Kind or apiVersion one side does not know does not tell them apart: a list
 * row or a restored tab may carry only the name.
 */
export const sameObject = (a: ObjectRef, b: ObjectRef): boolean =>
  !!a.name &&
  a.name === b.name &&
  a.namespace === b.namespace &&
  (!a.kind || !b.kind || a.kind === b.kind) &&
  (!a.apiVersion || !b.apiVersion || a.apiVersion === b.apiVersion);

const showsObject = (dt: DetailTab, cluster: string, ref: ObjectRef) =>
  (!dt.cluster || dt.cluster === cluster) && sameObject(tabRef(dt), ref);

/** The item a tab gets for freshly loaded `details`. The events load on their
 * own, so a tab keeps the ones it has until they are loaded again, unless the
 * object was recreated: those belonged to its predecessor. */
const freshTab = (dt: DetailTab, details: any): DetailTab => {
  const before = tabRef(dt).uid;
  const after = details?.metadata?.uid || '';
  const recreated = !!before && !!after && before !== after;
  const kept = recreated ? undefined : dt.item?.events;
  const item = details?.events === undefined && kept !== undefined ? { ...details, events: kept } : details;
  return { ...dt, item, isDeleted: false, isRecreated: recreated || !!dt.isRecreated };
};

export interface ApplyDetailsOptions {
  /** Open the panel on the details (a user action); a background refresh
   * leaves it as it is. */
  reveal?: boolean;
}

/**
 * Puts freshly loaded details into every detail tab of `cluster` that shows
 * that object, wherever the user is by now: the request is async, so they may
 * be on another cluster tab, or another object may be in the preview slot.
 * The tab item is what the panel renders; `detailData` follows the active one.
 *
 * Returns the same array when there is nothing to change.
 */
export function applyDetailsToTabs(tabs: Tab[], cluster: string, details: any, options: ApplyDetailsOptions = {}): Tab[] {
  const tabIndex = tabs.findIndex((t) => t.id === cluster);
  if (tabIndex === -1 || !details) return tabs;
  const state = tabs[tabIndex].state;
  const ref = refOf(details);
  const updated = new Set<string>();
  const detailTabs = state.detailTabs.map((dt) => {
    if (dt.item === details || !showsObject(dt, cluster, ref)) return dt;
    updated.add(dt.id);
    return freshTab(dt, details);
  });
  const changed = updated.size > 0;
  const activeUpdated = !!state.activeDetailTab && updated.has(state.activeDetailTab);
  // With no detail tab open, the details are those of the selected row.
  const selectionOnly = !!options.reveal && !state.activeDetailTab;
  if (!changed && !selectionOnly) return tabs;

  const next = [...tabs];
  next[tabIndex] = {
    ...tabs[tabIndex],
    state: {
      ...state,
      detailTabs: changed ? detailTabs : state.detailTabs,
      detailData: activeUpdated
        ? detailTabs.find((dt) => dt.id === state.activeDetailTab)!.item
        : selectionOnly ? details : state.detailData,
      isDetailsPanelCollapsed: options.reveal && (activeUpdated || selectionOnly) ? false : state.isDetailsPanelCollapsed,
    },
  };
  return next;
}

/**
 * Puts the details a user asked for (a click, a link, history) into the tab
 * of the cluster they were requested for, and opens the panel on them.
 */
export function applyLoadedDetails(tabs: Tab[], cluster: string, details: any): Tab[] {
  const tabIndex = tabs.findIndex((t) => t.id === cluster);
  if (tabIndex === -1) return tabs;
  const state = tabs[tabIndex].state;
  // A page/list navigation can close the preview while its request is in flight.
  if (!state.activeDetailTab && state.selectedNode && (!state.selectedItem || state.selectedNode.type !== 'resource')) return tabs;
  return applyDetailsToTabs(tabs, cluster, details, { reveal: true });
}

/**
 * Marks the detail tabs of `cluster` that show the object `ref` names as
 * deleted: the cluster answered 404 for it. They keep the last copy they had.
 */
export function markDetailsDeleted(tabs: Tab[], cluster: string, ref: ObjectRef): Tab[] {
  const tabIndex = tabs.findIndex((t) => t.id === cluster);
  if (tabIndex === -1 || !ref.name) return tabs;
  const state = tabs[tabIndex].state;
  let changed = false;
  const detailTabs = state.detailTabs.map((dt) => {
    if (dt.isDeleted || !showsObject(dt, cluster, ref)) return dt;
    changed = true;
    return { ...dt, isDeleted: true };
  });
  if (!changed) return tabs;
  const next = [...tabs];
  next[tabIndex] = { ...tabs[tabIndex], state: { ...state, detailTabs } };
  return next;
}

/**
 * Puts an object's events on the detail tabs showing that object, and on
 * `detailData` when it shows it. A tab showing another incarnation of the
 * object (another uid) does not get them.
 */
export function applyEventsToTabs(tabs: Tab[], cluster: string, details: any, events: any[]): Tab[] {
  const tabIndex = tabs.findIndex((t) => t.id === cluster);
  if (tabIndex === -1) return tabs;
  const ref = refOf(details);
  if (!ref.name) return tabs;
  const matches = (item: any, resource?: any) => {
    if (!item) return false;
    const other = refOf(item, resource);
    if (!sameObject(other, ref)) return false;
    return !other.uid || !ref.uid || other.uid === ref.uid;
  };
  const state = tabs[tabIndex].state;
  let changed = false;
  const detailTabs = state.detailTabs.map((dt) => {
    if ((dt.cluster && dt.cluster !== cluster) || !matches(dt.item, dt.resource) || dt.item.events === events) return dt;
    changed = true;
    return { ...dt, item: { ...dt.item, events } };
  });
  const dd = state.detailData;
  const ddMatches = matches(dd) && dd.events !== events;
  if (!changed && !ddMatches) return tabs;
  const active = detailTabs.find((dt) => dt.id === state.activeDetailTab);
  const next = [...tabs];
  next[tabIndex] = {
    ...tabs[tabIndex],
    state: {
      ...state,
      detailTabs,
      detailData: active && matches(active.item, active.resource) ? active.item : ddMatches ? { ...dd, events } : dd,
    },
  };
  return next;
}
