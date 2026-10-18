/**
 * How current the cluster overview is. The backend flags an update stale when
 * a section was carried forward from an earlier one or computed from pods its
 * cache no longer keeps current, and sends a frame with an error (and no data)
 * when the cluster cannot be read at all; the overview keeps what it shows in
 * both cases and says how old it is.
 */
export interface DashboardFreshness {
  stale: boolean;
  /** Why the sections are stale, from the backend. */
  reason?: string;
  /** Set while the cluster cannot be read at all. */
  error?: string;
  /** Unix ms the data shown is as of: when its stalest section was last fresh. */
  asOf?: number;
  /** The stale sections, by their payload keys. */
  sections: string[];
}

export const DASHBOARD_SECTIONS = [
  'resourceCounts',
  'podStatus',
  'nodeStatus',
  'workloadStatus',
  'events',
  'criticalAlerts',
  'resourceCapacity',
] as const;

export const FRESH_DASHBOARD: DashboardFreshness = { stale: false, sections: [] };

const messageTime = (msg: any, now: number): number => {
  const t = msg?.timestamp ? Date.parse(msg.timestamp) : NaN;
  return Number.isFinite(t) ? t : now;
};

/** The freshness after a dashboard message, given the one before it. */
export function nextDashboardFreshness(prev: DashboardFreshness, msg: any, now = Date.now()): DashboardFreshness {
  if (msg?.error) {
    // Nothing was read: everything shown stays as old as it already was.
    return {
      stale: true,
      error: String(msg.error),
      reason: prev.reason,
      asOf: prev.asOf,
      sections: [...DASHBOARD_SECTIONS],
    };
  }
  const d = msg?.data;
  if (!d) return prev;
  if (d.stale || (Array.isArray(d.staleSections) && d.staleSections.length > 0)) {
    return {
      stale: true,
      reason: d.staleReason || undefined,
      asOf: typeof d.staleSince === 'number' && d.staleSince > 0 ? d.staleSince : prev.asOf,
      sections: Array.isArray(d.staleSections) ? d.staleSections : [],
    };
  }
  return { stale: false, asOf: messageTime(msg, now), sections: [] };
}

export const isSectionStale = (f: DashboardFreshness, section: string): boolean =>
  f.stale && f.sections.includes(section);

/** "14:02" today, "Oct 17, 14:02" on another day. */
export function formatAsOf(ms: number | undefined, now = Date.now()): string {
  if (!ms) return 'an earlier update';
  const d = new Date(ms);
  const time = d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
  if (new Date(now).toDateString() === d.toDateString()) return time;
  return `${d.toLocaleDateString([], { month: 'short', day: 'numeric' })}, ${time}`;
}

/** The banner text: what is stale, since when and why. */
export function dashboardStaleMessage(f: DashboardFreshness, now = Date.now()): string {
  const asOf = `as of ${formatAsOf(f.asOf, now)}`;
  if (f.error) return `Cluster unreachable — showing data ${asOf}: ${f.error}`;
  const why = f.reason ? `: ${f.reason}` : '';
  return `Some sections are not updating — showing data ${asOf}${why}`;
}
