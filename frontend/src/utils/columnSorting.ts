import { formatStatus } from './formatters';
import { printerCellValue } from './resourceListColumns';

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
    default: {
      const fromPrinter = printerCellValue(item, column);
      if (fromPrinter !== undefined && fromPrinter !== '') return fromPrinter;
      return item[column] || '';
    }
  }
};

type SortKey = { v: any; s: string | null };

// Sort keys per column, cached per item. Items are replaced on change, never
// edited in place, so a key stays valid for the life of its item and a list
// that changed in a few rows is re-sorted without re-reading every row.
const sortKeys = new Map<string, WeakMap<object, SortKey>>();

const sortKeyOf = (
  item: any,
  column: string,
  cache: WeakMap<object, SortKey>,
): SortKey => {
  const cacheable = typeof item === 'object' && item !== null;
  let key = cacheable ? cache.get(item) : undefined;
  if (!key) {
    const v = getSortValue(item, column);
    key = { v, s: typeof v === 'string' ? v.toLowerCase() : null };
    if (cacheable) cache.set(item, key);
  }
  return key;
};

export const sortItems = <T>(
  items: T[],
  { sortBy, sortOrder }: SortConfig,
): T[] => {
  if (!sortBy) return items;

  const dir = sortOrder === 'asc' ? 1 : -1;
  let cache = sortKeys.get(sortBy);
  if (!cache) {
    cache = new WeakMap();
    sortKeys.set(sortBy, cache);
  }
  const keyed = items.map((item) => {
    const { v, s } = sortKeyOf(item, sortBy, cache!);
    return { item, v, s };
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
