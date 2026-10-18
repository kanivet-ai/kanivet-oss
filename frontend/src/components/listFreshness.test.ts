import { describe, expect, it } from 'vitest';
import { freshnessBanner, notUpdatingText, shownDataAge, syncingText } from './listFreshness';

const NOW = 1_700_000_000_000;

describe('list freshness', () => {
  it('says how old the shown data is', () => {
    expect(shownDataAge(undefined, NOW)).toBe('an earlier session');
    expect(shownDataAge(0, NOW)).toBe('an earlier session');
    expect(shownDataAge(NOW - 5_000, NOW)).toBe('less than a minute ago');
    expect(shownDataAge(NOW - 5 * 60_000, NOW)).toBe('5m ago');
    expect(shownDataAge(NOW - 3 * 3_600_000, NOW)).toBe('3h ago');
  });

  it('names the error and the age of the rows of a list that is not updating', () => {
    expect(notUpdatingText('pods is forbidden.', NOW - 120_000, NOW)).toBe(
      'Not updating — pods is forbidden. Showing data from 2m ago. Retrying…',
    );
    expect(notUpdatingText(undefined, undefined, NOW)).toBe('Not updating. Showing data from an earlier session. Retrying…');
  });

  it('shows a banner over rows that failed or are not live yet, never over an empty list', () => {
    const base = { now: NOW, lastSyncAt: NOW - 120_000 };
    expect(freshnessBanner({ ...base, rowCount: 0, hasReceivedData: false, loadError: 'x' })).toBeNull();
    expect(freshnessBanner({ ...base, rowCount: 3, hasReceivedData: true, loadError: null })).toBeNull();
    expect(freshnessBanner({ ...base, rowCount: 3, hasReceivedData: true, loadError: 'down' })).toEqual({
      kind: 'stale',
      text: notUpdatingText('down', base.lastSyncAt, NOW),
    });
    expect(freshnessBanner({ ...base, rowCount: 3, hasReceivedData: false, loadError: null })).toEqual({
      kind: 'syncing',
      text: syncingText(base.lastSyncAt, NOW),
    });
  });
});
