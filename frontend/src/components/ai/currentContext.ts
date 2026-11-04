import { useStore } from '../../store';
import type { ContextChip } from '../../services/api/aiChat';
import { clusterChip, namespaceChip, questionRefersTo, resourceChip } from './aiContext';

function currentTabState(s: any) {
  const cluster: string | null = s.currentTab;
  if (!cluster) return { cluster: null, st: null };
  const tab = s.activeTabs?.find((t: any) => t.id === cluster) || s.activeTabs?.[s.tabIndexMap?.get(cluster)];
  return { cluster, st: tab?.state };
}

/** The current tab's cluster and namespace as removable chips. A resource
 * open in the inspector is never included here; see `inspectorResourceChip`. */
export function currentContextChips(): ContextChip[] {
  const s: any = useStore.getState();
  const { cluster, st } = currentTabState(s);
  if (!cluster) return [];
  const chips: ContextChip[] = [clusterChip(cluster, s.clusterAliases?.[cluster])];
  const ns = st?.selectedNamespace;
  if (ns && ns !== 'all') chips.push(namespaceChip(cluster, ns));
  return chips;
}

/** The resource open (or selected) in the active inspector, as a chip the
 * user can choose to add. */
export function inspectorResourceChip(): ContextChip | null {
  const s: any = useStore.getState();
  const { cluster, st } = currentTabState(s);
  if (!cluster || !st) return null;
  const detail = st.detailTabs?.find((d: any) => d.id === st.activeDetailTab);
  const item = detail?.item || st.selectedItem;
  const md = item?.metadata;
  const kind = item?.kind || detail?.resource?.kind;
  if (!md?.name || !kind) return null;
  return resourceChip(detail?.cluster || cluster, { kind, apiVersion: item.apiVersion, namespace: md.namespace, name: md.name });
}

/** The current cluster's display name (what the cluster tab shows). */
export function currentClusterLabel(): string | null {
  return currentContextChips()[0]?.label ?? null;
}

/**
 * Context for a typed question: cluster and namespace always; the inspector
 * resource only when the question refers to it (or the user added it),
 * otherwise it comes back as a suggestion.
 */
export function askContext(query: string, addSuggested = false): { chips: ContextChip[]; suggestion: ContextChip | null } {
  const chips = currentContextChips();
  const res = inspectorResourceChip();
  if (!res) return { chips, suggestion: null };
  if (addSuggested || questionRefersTo(query, { kind: res.kind || '', name: res.name || '' })) return { chips: [...chips, res], suggestion: null };
  return { chips, suggestion: res };
}
