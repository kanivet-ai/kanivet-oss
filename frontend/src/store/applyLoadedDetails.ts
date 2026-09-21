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

  let detailTabs = state.detailTabs;
  if (state.activeDetailTab) {
    const active = state.detailTabs.find((dt) => dt.id === state.activeDetailTab);
    const shown = nameOf(active?.item);
    const loaded = nameOf(details);
    // The active detail tab moved on to another resource while this one loaded.
    if (loaded && shown && (shown !== loaded || namespaceOf(active?.item) !== namespaceOf(details))) return tabs;
    detailTabs = state.detailTabs.map((dt) => (dt.id === state.activeDetailTab ? { ...dt, item: details } : dt));
  }

  const next = [...tabs];
  next[tabIndex] = {
    ...tabs[tabIndex],
    state: { ...state, detailData: details, isDetailsPanelCollapsed: false, detailTabs },
  };
  return next;
}
