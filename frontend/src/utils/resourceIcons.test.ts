import { isValidElement } from 'react';
import { describe, expect, it } from 'vitest';
import * as K from '../components/icons/kube';
import { getCategoryIcon, getResourceIcon } from './resourceIcons';

const glyph = (node: unknown) => (isValidElement(node) ? node.type : undefined);

describe('getResourceIcon', () => {
  it('matches built-in kinds by singular kind, plural resource name and alias', () => {
    expect(glyph(getResourceIcon('Deployment'))).toBe(K.DeploymentIcon);
    expect(glyph(getResourceIcon('deployments'))).toBe(K.DeploymentIcon);
    expect(glyph(getResourceIcon('hpa'))).toBe(K.HorizontalPodAutoscalerIcon);
    expect(glyph(getResourceIcon('networkpolicies'))).toBe(K.NetworkPolicyIcon);
    expect(glyph(getResourceIcon('ingresses'))).toBe(K.IngressIcon);
  });

  it('covers Gateway API and other kinds the tree discovers from the cluster', () => {
    expect(glyph(getResourceIcon('ingressclasses'))).toBe(K.LayersIcon);
    expect(glyph(getResourceIcon('gatewayclasses'))).toBe(K.LayersIcon);
    expect(glyph(getResourceIcon('gateways'))).toBe(K.GatewayIcon);
    expect(glyph(getResourceIcon('httproutes'))).toBe(K.RouteIcon);
    expect(glyph(getResourceIcon('grpcroutes'))).toBe(K.RouteIcon);
    expect(glyph(getResourceIcon('leases'))).toBe(K.ClockIcon);
    expect(glyph(getResourceIcon('csidrivers'))).toBe(K.StorageIcon);
    expect(glyph(getResourceIcon('ServiceMonitor'))).toBe(K.MonitorIcon);
  });

  it('infers a fitting glyph for unknown kinds from their name', () => {
    expect(glyph(getResourceIcon('FooBarPolicy'))).toBe(K.NetworkPolicyIcon);
    expect(glyph(getResourceIcon('widgetclasses'))).toBe(K.LayersIcon);
    expect(glyph(getResourceIcon('acmebackups'))).toBe(K.ArchiveIcon);
    expect(glyph(getResourceIcon('ThingMonitor'))).toBe(K.MonitorIcon);
    expect(glyph(getResourceIcon('vendorsecrets'))).toBe(K.SecretIcon);
  });

  it('falls back to the manifest glyph when nothing matches', () => {
    expect(glyph(getResourceIcon('Zorble'))).toBe(K.ResourceIcon);
  });
});

describe('getCategoryIcon', () => {
  it('knows every category name the backend sends', () => {
    const names = [
      'Workloads',
      'Networking',
      'Configuration',
      'Storage',
      'RBAC',
      'Cluster',
      'Crossplane',
      'Argo CD',
      'Custom Resources',
    ];
    for (const name of names)
      expect(glyph(getCategoryIcon(name))).not.toBe(K.ResourceIcon);
  });
});
