import { describe, expect, it } from 'vitest';
import { isResourceName, pluralize, singularize } from './pluralization';

describe('pluralize', () => {
  it('spells built-in kinds the way the API server does', () => {
    const cases: Record<string, string> = {
      Pod: 'pods',
      Deployment: 'deployments',
      CronJob: 'cronjobs',
      Ingress: 'ingresses',
      IngressClass: 'ingressclasses',
      StorageClass: 'storageclasses',
      PriorityClass: 'priorityclasses',
      NetworkPolicy: 'networkpolicies',
      Endpoints: 'endpoints',
      EndpointSlice: 'endpointslices',
      Gateway: 'gateways',
      GatewayClass: 'gatewayclasses',
      HTTPRoute: 'httproutes',
      Lease: 'leases',
      ComponentStatus: 'componentstatuses',
      HorizontalPodAutoscaler: 'horizontalpodautoscalers',
      CustomResourceDefinition: 'customresourcedefinitions',
    };
    for (const [kind, plural] of Object.entries(cases)) {
      expect(pluralize(kind), kind).toBe(plural);
    }
  });

  it('applies English rules to kinds discovery has not described', () => {
    expect(pluralize('Prometheus')).toBe('prometheuses');
    expect(pluralize('IPAddress')).toBe('ipaddresses');
    expect(pluralize('Certificate')).toBe('certificates');
    expect(pluralize('Composition')).toBe('compositions');
    expect(pluralize('FooBarPolicy')).toBe('foobarpolicies');
    expect(pluralize('Relay')).toBe('relays');
    expect(pluralize('Patch')).toBe('patches');
    expect(pluralize('Index')).toBe('indexes');
  });

  it('leaves resource names unchanged', () => {
    for (const name of [
      'pods',
      'ingresses',
      'gateways',
      'networkpolicies',
      'prometheuses',
      'endpoints',
      'leases',
    ]) {
      expect(pluralize(name), name).toBe(name);
    }
  });
});

describe('singularize', () => {
  it('recovers the lowercase kind from a resource name', () => {
    const cases: Record<string, string> = {
      pods: 'pod',
      ingresses: 'ingress',
      storageclasses: 'storageclass',
      networkpolicies: 'networkpolicy',
      leases: 'lease',
      componentstatuses: 'componentstatus',
      endpoints: 'endpoints',
      endpointslices: 'endpointslice',
      gateways: 'gateway',
      prometheuses: 'prometheus',
      releases: 'release',
      patches: 'patch',
    };
    for (const [plural, kind] of Object.entries(cases)) {
      expect(singularize(plural), plural).toBe(kind);
    }
  });

  it('leaves kinds unchanged apart from case', () => {
    expect(singularize('Ingress')).toBe('ingress');
    expect(singularize('Deployment')).toBe('deployment');
    expect(singularize('Endpoints')).toBe('endpoints');
  });

  it('round-trips with pluralize for built-in kinds', () => {
    for (const kind of [
      'Pod',
      'Ingress',
      'NetworkPolicy',
      'Gateway',
      'Lease',
      'StorageClass',
      'EndpointSlice',
      'HorizontalPodAutoscaler',
    ]) {
      expect(singularize(pluralize(kind)), kind).toBe(kind.toLowerCase());
    }
  });
});

describe('isResourceName', () => {
  it('tells resource names from kinds', () => {
    expect(isResourceName('deployments')).toBe(true);
    expect(isResourceName('ingresses')).toBe(true);
    expect(isResourceName('Deployment')).toBe(false);
    expect(isResourceName('deployment')).toBe(false);
    expect(isResourceName('Deployments')).toBe(false);
    expect(isResourceName('')).toBe(false);
  });
});
