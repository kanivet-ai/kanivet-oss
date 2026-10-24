import { describe, expect, it } from 'vitest';
import {
  FRESH_DASHBOARD,
  dashboardStaleMessage,
  isSectionStale,
  nextDashboardFreshness,
} from './dashboardFreshness';

const T0 = Date.parse('2026-10-18T12:00:00Z');

describe('nextDashboardFreshness', () => {
  it('a fresh update is current as of its timestamp', () => {
    const f = nextDashboardFreshness(FRESH_DASHBOARD, {
      type: 'dashboard',
      timestamp: new Date(T0).toISOString(),
      data: { stale: false, resourceCounts: { ':pods': 3 } },
    });
    expect(f).toEqual({ stale: false, asOf: T0, sections: [] });
  });

  it('an error frame keeps the data and marks everything stale as of the last update', () => {
    const fresh = nextDashboardFreshness(FRESH_DASHBOARD, {
      timestamp: new Date(T0).toISOString(),
      data: { stale: false },
    });
    const down = nextDashboardFreshness(fresh, { type: 'dashboard', error: 'cluster unreachable: connection refused', data: null });
    expect(down.stale).toBe(true);
    expect(down.error).toContain('connection refused');
    expect(down.asOf).toBe(T0);
    expect(isSectionStale(down, 'podStatus')).toBe(true);
    expect(dashboardStaleMessage(down, T0)).toContain('Cluster unreachable');
    expect(dashboardStaleMessage(down, T0)).toContain('connection refused');
  });

  it('a stale update lists its sections and the time they were last fresh', () => {
    const f = nextDashboardFreshness(FRESH_DASHBOARD, {
      timestamp: new Date(T0 + 60_000).toISOString(),
      data: {
        stale: true,
        staleReason: 'pods watch failing: connection refused',
        staleSince: T0,
        staleSections: ['podStatus', 'criticalAlerts'],
      },
    });
    expect(f.asOf).toBe(T0);
    expect(isSectionStale(f, 'podStatus')).toBe(true);
    expect(isSectionStale(f, 'nodeStatus')).toBe(false);
    expect(dashboardStaleMessage(f, T0)).toContain('pods watch failing');
  });

  it('recovers once an update is fresh again', () => {
    const stale = nextDashboardFreshness(FRESH_DASHBOARD, { error: 'boom' });
    const back = nextDashboardFreshness(stale, { timestamp: new Date(T0).toISOString(), data: { stale: false } });
    expect(back.stale).toBe(false);
    expect(back.error).toBeUndefined();
  });
});
