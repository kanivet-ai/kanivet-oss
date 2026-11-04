import { useStore } from '../../store';
import { pageNode, pageResources } from '../../store/navigationTargets';

/** Opens the Upgrade readiness page for cluster as a pinned page tab. */
export async function openUpgradeReadiness(cluster: string | null | undefined): Promise<void> {
  const state = useStore.getState();
  const target = cluster || state.currentTab;
  if (!target) return;
  const resource = pageResources['upgrade-readiness'];
  const node = pageNode(resource.kind, target);
  if (node) state.selectNode(node);
  await state.openResourceListTab(resource, target, true);
  void state.recordNavigation('upgrade-readiness', 'upgrade-readiness', resource).catch(() => {});
}
