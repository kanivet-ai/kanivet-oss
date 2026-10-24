import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import type { WorkloadReport } from '../../types/rightsizing';
import { LastCalculatedBadge } from '../finops/LastCalculatedBadge';
import { carriedMessage, podsStaleMessage } from './rightsizingView';

const now = Date.UTC(2026, 9, 18, 12, 0, 0);
const hoursAgo = (h: number) => new Date(now - h * 3600_000).toISOString();

describe('stale markers', () => {
  it('says when a carried workload was computed', () => {
    const w = { carried: true, computedAt: hoursAgo(3) } as WorkloadReport;
    expect(carriedMessage(w, now)).toContain('from 3 h ago');
  });

  it('says since when the pod watch fails', () => {
    expect(podsStaleMessage(hoursAgo(2), now)).toMatch(/^The cluster's pod watch started failing 2 h ago/);
    expect(podsStaleMessage(undefined, now)).toMatch(/^The cluster's pod watch is failing:/);
  });

  it('shows the pods badge on FinOps only while pods are stale', () => {
    const render = (podsStale: boolean) =>
      renderToStaticMarkup(
        createElement(LastCalculatedBadge, { lastUpdated: new Date().toISOString(), podsStale, podsStaleSince: hoursAgo(1) }),
      );
    expect(render(true)).toContain('pods not live');
    expect(render(false)).not.toContain('pods not live');
  });
});
