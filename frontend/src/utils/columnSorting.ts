import { formatStatus } from './formatters';

type SortOrder = 'asc' | 'desc';

interface SortConfig {
  sortBy: string;
  sortOrder: SortOrder;
}

const getSortValue = (item: any, column: string): any => {
  switch (column) {
    case 'name':
      return item.name || '';
    case 'namespace':
      return item.namespace || '';
    case 'deleted':
      // Items with deletionTimestamp are pending deletion (deleted = true)
      // Sort: deleted items first (1) when ascending, non-deleted first (0) when ascending
      return item.deletionTimestamp ? 1 : 0;
    case 'age':
      return new Date(item.creationTimestamp || 0).getTime();
    case 'status':
      return formatStatus(item);
    case 'ready':
      if (item.readyReplicas !== undefined && item.replicas !== undefined) {
        return item.readyReplicas;
      } else if (item.conditions) {
        const readyCondition = Array.isArray(item.conditions)
          ? item.conditions.find((c: any) => c.type === 'Ready')
          : null;
        return readyCondition?.status === 'True' ? 1 : 0;
      }
      return 0;
    case 'phase':
      return item.phase || '';
    case 'restarts':
      return typeof item.restarts === 'number' ? item.restarts : 0;
    case 'node':
      return item.nodeName || item.spec?.nodeName || '';
    case 'replicas':
      return item.replicas !== undefined ? item.replicas : 0;
    case 'type':
      return item.type || item.secretType || '';
    case 'cluster-ip':
      return item.clusterIP || '';
    case 'capacity':
      return item.capacity
        ? parseInt(item.capacity.replace(/\D/g, '') || '0')
        : 0;
    default:
      return item[column] || '';
  }
};

export const sortItems = <T>(
  items: T[],
  { sortBy, sortOrder }: SortConfig,
): T[] => {
  if (!sortBy) return items;

  const dir = sortOrder === 'asc' ? 1 : -1;
  const keyed = items.map((item) => {
    const v = getSortValue(item, sortBy);
    return { item, v, s: typeof v === 'string' ? v.toLowerCase() : null };
  });
  keyed.sort((a, b) => {
    if (a.s !== null && b.s !== null) return dir * a.s.localeCompare(b.s);
    if (typeof a.v === 'number' && typeof b.v === 'number') return dir * (a.v - b.v);
    return dir * String(a.v).localeCompare(String(b.v));
  });
  return keyed.map((k) => k.item);
};

export const getSortIndicator = (
  column: string,
  currentSortBy: string,
  currentSortOrder: SortOrder,
): string => {
  const columnKey = column.toLowerCase();
  if (currentSortBy !== columnKey) return '';
  return currentSortOrder === 'asc' ? ' ↑' : ' ↓';
};

export const getNextSortOrder = (
  column: string,
  currentSortBy: string,
  currentSortOrder: SortOrder,
): SortOrder => {
  const columnKey = column.toLowerCase();
  if (currentSortBy === columnKey) {
    return currentSortOrder === 'asc' ? 'desc' : 'asc';
  }
  return 'asc';
};
