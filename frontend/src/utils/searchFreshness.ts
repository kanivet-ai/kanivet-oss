import type { SearchResult } from '../types/search';
import { formatAgeSince } from './formatters';

/**
 * The marker shown on a search result the backend flagged stale: it comes
 * from the persisted index of an earlier session and its cluster has not
 * been refreshed yet, so the object may have changed or gone. Returns null
 * for a current result.
 */
export function staleResultLabel(
  result: Pick<SearchResult, 'stale' | 'indexedAt'>,
  now: number = Date.now(),
): { text: string; title: string } | null {
  if (!result.stale) return null;
  if (!result.indexedAt) {
    return { text: 'not refreshed', title: 'From an earlier session; not refreshed yet' };
  }
  const when = new Date(result.indexedAt).toLocaleString();
  return {
    text: `indexed ${formatAgeSince(result.indexedAt, now)} ago`,
    title: `Indexed ${when}, in an earlier session; not refreshed yet`,
  };
}
