import { useStore } from '../../store';
import { pageNode, pageResources } from '../../store/navigationTargets';
import { setNetworkIntent, type NetworkIntent } from './networkUtils';

/** Opens (or focuses) the cluster's Network page, optionally on a path or view. */
export async function openNetworkPage(cluster: string, intent?: NetworkIntent): Promise<void> {
  if (intent) setNetworkIntent(intent);
  const s = useStore.getState();
  const node = pageNode('NetworkPage', cluster);
  if (node) s.selectNode(node as any);
  await s.openResourceListTab(pageResources.network, cluster, true);
  if (node) s.recordNavigation('network', node.id, node.data).catch(() => {});
}
