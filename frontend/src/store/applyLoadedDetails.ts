import type { Tab } from './types';

const nameOf = (item: any) => item?.metadata?.name || item?.name;
const namespaceOf = (item: any) => item?.metadata?.namespace || item?.namespace || '';

/**
 * Puts freshly loaded details into the tab of the cluster they were requested
 * for. The request is async, so the user may be on another cluster tab by the
 * time it answers: resolving the target from "the current tab" at that point
 * either dropped the answer or wrote it into the wrong cluster, leaving the
 * original detail pane on its list-row placeholder (no containers, no metrics).
 *
 * Returns the same array when there is nothing to change.
 */
export function applyLoadedDetails(tabs: Tab[], cluster: string, details: any): Tab[] {
  const tabIndex = tabs.findIndex((t) => t.id === cluster);
  if (tabIndex === -1) return tabs;
  const state = tabs[tabIndex].state;

  // A page/list navigation can close the preview while its request is in flight.
  if (!state.activeDetailTab && state.selectedNode && (!state.selectedItem || state.selectedNode.type !== 'resource')) return tabs;

  let detailTabs = state.detailTabs;
  if (state.activeDetailTab) {
    const active = state.detailTabs.find((dt) => dt.id === state.activeDetailTab);
    const shown = nameOf(active?.item);
    const loaded = nameOf(details);
    // The active detail tab moved on to another resource while this one loaded.
    if (loaded && shown && (shown !== loaded || namespaceOf(active?.item) !== namespaceOf(details))) return tabs;
    const shownKind = active?.resource?.kind || active?.item?.kind;
    if (shownKind && details?.kind && shownKind !== details.kind) return tabs;
    const shownVersion = active?.item?.apiVersion;
    if (shownVersion && details?.apiVersion && shownVersion !== details.apiVersion) return tabs;
    detailTabs = state.detailTabs.map((dt) => (dt.id === state.activeDetailTab ? { ...dt, item: details } : dt));
  }

  const next = [...tabs];
  next[tabIndex] = {
    ...tabs[tabIndex],
    state: {
      ...state,
      detailData: details,
      // Opening a tab reveals it; a later response must preserve a manual collapse.
      isDetailsPanelCollapsed: state.activeDetailTab ? state.isDetailsPanelCollapsed : false,
      detailTabs,
    },
  };
  return next;
}
