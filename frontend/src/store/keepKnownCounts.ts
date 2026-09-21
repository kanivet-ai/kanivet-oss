import type { TreeNode } from './types';

/**
 * Re-expanding a category first lays down its resource nodes without counts
 * and fills them in once the backend has counted. That refresh runs on every
 * cluster tab switch, so counts already on screen went blank each time. Carry
 * over what is known; the fresh numbers replace it when they arrive.
 */
export function keepKnownCounts(existing: TreeNode[] | undefined, next: TreeNode[]): TreeNode[] {
  if (!existing || existing.length === 0) return next;
  const known = new Map(existing.map((node) => [node.id, node]));
  return next.map((node) => {
    const previous = known.get(node.id);
    if (!previous) return node;
    const count = node.count ?? previous.count;
    const children = node.children ? keepKnownCounts(previous.children, node.children) : node.children;
    if (count === node.count && children === node.children) return node;
    return { ...node, count, ...(children && { children }) };
  });
}
