import { createElement } from 'react';
import { renderToString } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';

vi.mock('../store', () => ({
  useStore: (select: (state: unknown) => unknown) =>
    select({ openDetailTab: () => {} }),
}));
vi.mock('./ResourceLink', () => ({ default: () => null }));
vi.mock('../hooks/useVisibleInterval', () => ({ useVisibleInterval: () => {} }));

vi.mock('../services/api', () => ({
  default: {
    getNatsDetection: vi.fn(async () => ({ installed: true, hasMonitor: true })),
    getNatsOverview: vi.fn(async () => ({})),
  },
}));

const { default: NatsOverview, natsOverviewSnapshots } = await import(
  './NatsOverview'
);

const render = (cluster: string) =>
  renderToString(createElement(NatsOverview, { cluster }));

describe('NatsOverview when its tab is shown again', () => {
  it('shows a loading screen the first time a cluster is opened', () => {
    expect(render('first')).toContain('Loading NATS status');
  });

  it('starts from what the cluster last showed instead of a loading screen', () => {
    natsOverviewSnapshots.set('seen', {
      detection: {
        installed: true,
        hasMonitor: true,
        namespace: 'nats',
      } as any,
      overview: { varz: { version: '2.10.0' }, jsz: { account_details: [] } } as any,
      history: [],
      expandedStreams: new Set(),
    });

    const html = render('seen');

    expect(html).not.toContain('Loading NATS status');
    expect(html).toContain('2.10.0');
  });
});
