import { useStore } from '../../store';
import type { ContextChip } from '../../services/api/aiChat';
import { clusterChip, namespaceChip, resourceChip } from './aiContext';

/** The current tab's context (cluster, namespace, open/selected resource)
 * as removable chips for "Ask AI". */
export function currentContextChips(): ContextChip[] {
  const s: any = useStore.getState();
  const cluster: string | null = s.currentTab;
  if (!cluster) return [];
  const chips: ContextChip[] = [clusterChip(cluster)];
  const tab = s.activeTabs?.find((t: any) => t.id === cluster) || s.activeTabs?.[s.tabIndexMap?.get(cluster)];
  const st = tab?.state;
  const ns = st?.selectedNamespace;
  if (ns && ns !== 'all') chips.push(namespaceChip(cluster, ns));
  const detail = st?.detailTabs?.find((d: any) => d.id === st.activeDetailTab);
  const item = detail?.item || st?.selectedItem;
  const md = item?.metadata;
  const kind = item?.kind || detail?.resource?.kind;
  if (md?.name && kind) {
    chips.push(resourceChip(detail?.cluster || cluster, { kind, apiVersion: item.apiVersion, namespace: md.namespace, name: md.name }));
  }
  return chips;
}
