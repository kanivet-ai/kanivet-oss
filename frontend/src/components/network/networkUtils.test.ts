import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';
import type { Hop, Target } from '../../types/network';

vi.mock('../ResourceLink', () => ({ default: ({ children }: any) => createElement('a', null, children) }));
vi.mock('./openNetwork', () => ({ openNetworkPage: vi.fn() }));

import PathDiagram from './PathDiagram';
import { reachCommandResult } from './networkCommands';
import {
  filterTargets,
  formatPct,
  formatRate,
  matchTarget,
  parseEndpoint,
  parseReachQuery,
  setNetworkIntent,
  takeNetworkIntent,
} from './networkUtils';

const targets: Target[] = [
  { ref: { kind: 'Deployment', namespace: 'web', name: 'frontend' }, pods: 2 },
  { ref: { kind: 'Service', namespace: 'prod', name: 'api' }, pods: 2, ports: [80], portNames: ['http'] },
  { ref: { kind: 'Deployment', namespace: 'prod', name: 'api' }, pods: 2, ports: [8080] },
  { ref: { kind: 'Pod', namespace: 'prod', name: 'api-abc-1' }, pods: 1 },
];

describe('parseEndpoint', () => {
  it('reads kind/ns/name, ns/name, svc DNS names, IPs and ports', () => {
    expect(parseEndpoint('svc/prod/api:80')).toEqual({ kind: 'Service', namespace: 'prod', name: 'api', port: 80 });
    expect(parseEndpoint('prod/api')).toEqual({ namespace: 'prod', name: 'api' });
    expect(parseEndpoint('deploy/frontend')).toEqual({ kind: 'Deployment', name: 'frontend' });
    expect(parseEndpoint('api.prod.svc.cluster.local:443')).toEqual({ kind: 'Service', namespace: 'prod', name: 'api', port: 443 });
    expect(parseEndpoint('8.8.8.8:53')).toEqual({ kind: 'IP', name: '8.8.8.8', ip: '8.8.8.8', port: 53 });
    expect(parseEndpoint('frontend?')).toEqual({ name: 'frontend' });
  });
});

describe('parseReachQuery', () => {
  it('understands the palette phrasing', () => {
    const q = parseReachQuery('can frontend reach prod/api on port 80?');
    expect(q?.source.name).toBe('frontend');
    expect(q?.destination).toMatchObject({ namespace: 'prod', name: 'api' });
    expect(q?.port).toBe(80);
    expect(parseReachQuery('> can web talk to api:8080')?.port).toBe(8080);
    expect(parseReachQuery('frontend api')).toBeNull();
  });
});

describe('matchTarget', () => {
  it('prefers Services for destinations and workloads for sources', () => {
    expect(matchTarget(targets, { name: 'api' }, 'destination')).toEqual({ kind: 'Service', namespace: 'prod', name: 'api' });
    expect(matchTarget(targets, { name: 'api' }, 'source')).toEqual({ kind: 'Deployment', namespace: 'prod', name: 'api' });
    expect(matchTarget(targets, { name: 'nope' }, 'source')).toBeNull();
    expect(matchTarget(targets, { name: '1.2.3.4', ip: '1.2.3.4', kind: 'IP' }, 'destination')).toEqual({ kind: 'IP', ip: '1.2.3.4' });
  });

  it('filters the picker by every term and hides Services as sources', () => {
    expect(filterTargets(targets, 'prod api', 'destination').length).toBe(3);
    expect(filterTargets(targets, 'prod api', 'source').every((t) => t.ref.kind !== 'Service')).toBe(true);
  });
});

describe('formatting', () => {
  it('formats rates and percentages', () => {
    expect(formatRate(250)).toBe('250/s');
    expect(formatRate(2.345)).toBe('2.3/s');
    expect(formatRate(0.05)).toBe('3.0/min');
    expect(formatPct(0.05)).toBe('5.0%');
    expect(formatPct(0.0001)).toBe('<0.1%');
  });
});

describe('intent hand-off', () => {
  it('is taken once', () => {
    setNetworkIntent({ view: 'coverage', namespace: 'prod' });
    expect(takeNetworkIntent()).toEqual({ view: 'coverage', namespace: 'prod' });
    expect(takeNetworkIntent()).toBeNull();
  });

  it('turns typed questions into a palette command', () => {
    expect(reachCommandResult('can frontend reach api:80')?.resource.name).toBe('Can frontend reach api:80?');
    expect(reachCommandResult('frontend')).toBeNull();
  });
});

describe('PathDiagram', () => {
  it('marks hops after the first failure as unreached and expands the failing one', () => {
    const hops: Hop[] = [
      { id: 'source', kind: 'source', title: 'Deployment web/frontend', status: 'pass', detail: '2 pods' },
      { id: 'egress', kind: 'egress', title: 'Egress', status: 'pass', detail: 'open' },
      { id: 'ingress', kind: 'ingress', title: 'Ingress', status: 'fail', detail: 'Blocked: default-deny ingress in prod', reasons: ['deny selects api'] },
      { id: 'destination', kind: 'destination', title: 'Service prod/api', status: 'pass', detail: '' },
    ];
    const html = renderToStaticMarkup(createElement(PathDiagram, { hops, cluster: 'c' }));
    expect(html).toContain('default-deny ingress in prod');
    expect(html).toContain('deny selects api');
    expect(html.match(/net-hop--unreached/g)?.length).toBe(1);
  });
});
