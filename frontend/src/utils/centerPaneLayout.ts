import type { SplitNode } from '../components/CenterPaneSplitPane';

interface SavedLayout {
  centerPaneLayout?: SplitNode;
  focusedCenterPaneId?: string | null;
}

const firstLeaf = (node: SplitNode): SplitNode =>
  node.type === 'resourceList' || !node.children?.length ? node : firstLeaf(node.children[0]);

const hasPane = (node: SplitNode, id: string): boolean =>
  node.id === id || Boolean(node.children?.some((child) => hasPane(child, id)));

const maxPaneNumber = (node: SplitNode): number => {
  const match = node.id.match(/^pane-(\d+)$/);
  const current = match ? parseInt(match[1], 10) : 0;
  return Math.max(current, ...(node.children || []).map(maxPaneNumber));
};

/**
 * The pane a new tab should open in: the wanted one when the layout contains
 * it, otherwise the layout's first pane. `root` is only a safe answer while
 * there is no layout; closing a split can leave a root pane with another id.
 */
export const resolvePaneId = (layout: SplitNode | undefined, wanted: string | null | undefined) => {
  if (!layout?.id) return 'root';
  return wanted && hasPane(layout, wanted) ? wanted : firstLeaf(layout).id;
};

/**
 * The pane layout a cluster tab starts from. It is derived from that tab's own
 * saved state and nothing else: pane ids differ between clusters (a root pane
 * can be `root` in one and `pane-6` in another), and a resource tab opened in
 * a pane the container is not rendering is invisible. The focused pane is
 * therefore always one the layout contains.
 */
export const initialCenterLayout = (saved: SavedLayout | undefined, tabId: string) => {
  const rootNode: SplitNode = saved?.centerPaneLayout?.id
    ? saved.centerPaneLayout
    : { id: 'root', type: 'resourceList', tabId };
  const focusedNodeId = resolvePaneId(rootNode, saved?.focusedCenterPaneId);
  return { rootNode, focusedNodeId, nodeCounter: maxPaneNumber(rootNode) + 1 };
};
