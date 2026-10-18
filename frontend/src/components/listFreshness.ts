import { formatAgeSince } from '../utils/formatters';

// When the rows on screen were last known complete, for a sentence such as
// "Showing data from 5m ago".
export function shownDataAge(lastSyncAt: number | undefined, now: number): string {
  if (!lastSyncAt || lastSyncAt > now + 60_000) return 'an earlier session';
  if (now - lastSyncAt < 60_000) return 'less than a minute ago';
  return `${formatAgeSince(lastSyncAt, now)} ago`;
}

const sentence = (text: string) => text.trim().replace(/[.\s]+$/, '');

// The banner over rows the backend has stopped keeping current.
export function notUpdatingText(error: string | null | undefined, lastSyncAt: number | undefined, now: number): string {
  const reason = error ? ` — ${sentence(error)}` : '';
  return `Not updating${reason}. Showing data from ${shownDataAge(lastSyncAt, now)}. Retrying…`;
}

// The banner over rows from a cache or an earlier visit while the live list
// is on its way.
export function syncingText(lastSyncAt: number | undefined, now: number): string {
  return `Syncing — showing data from ${shownDataAge(lastSyncAt, now)} until the live list arrives.`;
}

export type FreshnessBanner = { kind: 'stale' | 'syncing'; text: string } | null;

// What to show over the rows of a list, if anything. Rows only: an empty list
// shows its loading or error state instead.
export function freshnessBanner(opts: {
  rowCount: number;
  hasReceivedData: boolean;
  loadError: string | null | undefined;
  lastSyncAt?: number;
  now: number;
}): FreshnessBanner {
  if (opts.rowCount === 0) return null;
  if (opts.loadError) return { kind: 'stale', text: notUpdatingText(opts.loadError, opts.lastSyncAt, opts.now) };
  if (!opts.hasReceivedData) return { kind: 'syncing', text: syncingText(opts.lastSyncAt, opts.now) };
  return null;
}
