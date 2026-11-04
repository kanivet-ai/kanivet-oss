import { describe, expect, it } from 'vitest';
import { back, breadcrumb, defaultLocation, forward, historyAt, navigate, type TopoLocation } from './topoLocation';

const dep: TopoLocation = { view: 'focus', ref: { kind: 'Deployment', namespace: 'shop', name: 'web', group: 'apps', version: 'v1' }, namespace: 'shop' };

describe('topology location', () => {
  it('defaults to the namespace app view, else the cluster', () => {
    expect(defaultLocation({ selectedNamespace: 'shop' })).toEqual({ view: 'app', namespace: 'shop' });
    expect(defaultLocation({ selectedNamespace: 'all' })).toEqual({ view: 'cluster' });
    expect(defaultLocation({})).toEqual({ view: 'cluster' });
    expect(defaultLocation({ focus: dep.view === 'focus' ? dep.ref : undefined }).view).toBe('focus');
  });

  it('walks back and forward through refocuses', () => {
    let h = historyAt({ view: 'cluster' });
    h = navigate(h, { view: 'app', namespace: 'shop' });
    h = navigate(h, dep);
    h = navigate(h, dep); // same place: no new step
    expect(h.past).toHaveLength(2);
    h = back(h);
    expect(h.present).toEqual({ view: 'app', namespace: 'shop' });
    h = back(h);
    expect(h.present).toEqual({ view: 'cluster' });
    expect(back(h)).toBe(h);
    h = forward(h);
    expect(h.present).toEqual({ view: 'app', namespace: 'shop' });
    h = navigate(h, { view: 'app', namespace: 'other' });
    expect(h.future).toHaveLength(0);
  });

  it('builds a clickable breadcrumb', () => {
    const c = breadcrumb('prod', dep);
    expect(c.map((x) => x.label)).toEqual(['prod', 'shop', 'web']);
    expect(c[0].to).toEqual({ view: 'cluster' });
    expect(c[1].to).toEqual({ view: 'app', namespace: 'shop' });
    expect(c[2].to).toBeUndefined();
    expect(breadcrumb('prod', { view: 'cluster' })[0].to).toBeUndefined();
  });
});
