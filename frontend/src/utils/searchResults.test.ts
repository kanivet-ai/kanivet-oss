import { describe, expect, it } from 'vitest';
import type { SearchResult } from '../types/search';
import {
  dedupeSearchResults,
  describeKindDefinition,
  describeSearchResource,
  findTreeResourceNode,
  resourceIdentity,
  resourceListNode,
} from './searchResults';

const result = (
  overrides: Partial<SearchResult['resource']>,
): SearchResult => ({
  resource: {
    id: 'id',
    cluster: 'c',
    kind: 'Deployment',
    apiVersion: 'apps/v1',
    group: 'apps',
    version: 'v1',
    name: 'web',
    namespace: 'ns',
    category: 'Workloads',
    createdAt: '',
    updatedAt: '',
    ...overrides,
  },
  score: 1,
  matches: [],
});

describe('describeSearchResource', () => {
  it('uses the resource name the backend supplies and keeps the Kind', () => {
    const d = describeSearchResource(
      result({ resource: 'deployments' }).resource,
    );
    expect(d.resourceName).toBe('deployments');
    expect(d.kind).toBe('Deployment');
    expect(d.categoryId).toBe('workloads');
    expect(d.apiVersion).toBe('apps/v1');
    expect(d.namespaced).toBe(true);
  });

  it('recovers the Kind from documents that stored the plural resource name as kind', () => {
    const d = describeSearchResource(
      result({ kind: 'deployments', resource: undefined }).resource,
    );
    expect(d.kind).toBe('Deployment');
    expect(d.resourceName).toBe('deployments');
    const hpa = describeSearchResource(
      result({
        kind: 'horizontalpodautoscalers',
        group: 'autoscaling',
        version: 'v2',
        apiVersion: 'autoscaling/v2',
      }).resource,
    );
    expect(hpa.kind).toBe('HorizontalPodAutoscaler');
  });

  it('pluralizes kinds the backend has no resource name for', () => {
    const d = describeSearchResource(
      result({
        kind: 'Gateway',
        group: 'gateway.networking.k8s.io',
        version: 'v1',
        apiVersion: 'gateway.networking.k8s.io/v1',
      }).resource,
    );
    expect(d.resourceName).toBe('gateways');
    expect(d.kind).toBe('Gateway');
  });

  it('derives group and version from apiVersion when the result lacks them', () => {
    const d = describeSearchResource({
      cluster: 'c',
      kind: 'Pod',
      name: 'p',
      namespace: 'ns',
      apiVersion: 'v1',
    });
    expect(d.group).toBe('');
    expect(d.version).toBe('v1');
    expect(d.resourceName).toBe('pods');
    const scoped = describeSearchResource({
      cluster: 'c',
      kind: 'Node',
      name: 'n',
      apiVersion: 'v1',
    });
    expect(scoped.namespaced).toBe(false);
  });
});

describe('describeKindDefinition', () => {
  it('fills coordinates from the registry for built-in kinds', () => {
    const d = describeKindDefinition({
      cluster: 'c',
      kind: 'KindDefinition',
      name: 'Deployment',
      group: 'apps',
      version: 'v1',
    });
    expect(d.resourceName).toBe('deployments');
    expect(d.kind).toBe('Deployment');
    expect(d.namespaced).toBe(true);
    expect(d.categoryId).toBe('workloads');
  });

  it('prefers the resource name and scope the backend reports for custom kinds', () => {
    const d = describeKindDefinition({
      cluster: 'c',
      kind: 'KindDefinition',
      name: 'Redis',
      group: 'cache.example.com',
      version: 'v1',
      resource: 'redis',
      labels: { namespaced: 'false' },
    });
    expect(d.resourceName).toBe('redis');
    expect(d.namespaced).toBe(false);
  });
});

describe('dedupeSearchResults', () => {
  it('collapses the same object indexed under both kind spellings', () => {
    const results = [
      result({ id: 'a', kind: 'Deployment' }),
      result({ id: 'b', kind: 'deployments' }),
      result({ id: 'c', kind: 'Deployment', name: 'web-canary' }),
      result({ id: 'd', kind: 'Deployment', cluster: 'other' }),
    ];
    expect(dedupeSearchResults(results).map((r) => r.resource.id)).toEqual([
      'a',
      'c',
      'd',
    ]);
  });

  it('collapses kind definitions repeated per served version', () => {
    const results = [
      result({
        id: 'k1',
        kind: 'KindDefinition',
        name: 'HorizontalPodAutoscaler',
        group: 'autoscaling',
        version: 'v2',
        namespace: '',
      }),
      result({
        id: 'k2',
        kind: 'KindDefinition',
        name: 'HorizontalPodAutoscaler',
        group: 'autoscaling',
        version: 'v1',
        namespace: '',
      }),
    ];
    expect(dedupeSearchResults(results)).toHaveLength(1);
    expect(resourceIdentity(results[0].resource)).toBe(
      resourceIdentity(results[1].resource),
    );
  });
});

describe('tree nodes', () => {
  const tree = [
    {
      id: 'workloads',
      label: 'Workloads',
      type: 'category',
      children: [
        {
          id: 'workloads-apps-v1-deployments',
          label: 'deployments',
          type: 'resource',
          data: {
            name: 'deployments',
            group: 'apps',
            version: 'v1',
            kind: 'Deployment',
            namespaced: true,
          },
        },
      ],
    },
    {
      id: 'custom',
      label: 'Custom Resources',
      type: 'category',
      children: [
        {
          id: 'custom-cache.example.com-v1beta1',
          label: 'cache.example.com/v1beta1',
          type: 'apiVersion',
          children: [
            {
              id: 'custom-cache.example.com-v1beta1-redis',
              label: 'Redis',
              type: 'resource',
              data: {
                name: 'redis',
                group: 'cache.example.com',
                version: 'v1beta1',
                kind: 'Redis',
                namespaced: true,
              },
            },
          ],
        },
      ],
    },
  ];

  it('finds the tree node for a resource type, exact version first', () => {
    expect(findTreeResourceNode(tree, 'apps', 'v1', 'deployments')?.id).toBe(
      'workloads-apps-v1-deployments',
    );
    expect(
      findTreeResourceNode(tree, 'cache.example.com', 'v1', 'redis'),
    ).toBeUndefined();
    expect(
      findTreeResourceNode(tree, 'cache.example.com', 'v1', 'redis', {
        anyVersion: true,
      })?.id,
    ).toBe('custom-cache.example.com-v1beta1-redis');
  });

  it('builds a node shaped like the sidebar when the tree has none', () => {
    const node = resourceListNode(
      describeSearchResource(result({ resource: 'deployments' }).resource),
    );
    expect(node.id).toBe('workloads-apps-v1-deployments');
    expect(node.data).toEqual({
      name: 'deployments',
      group: 'apps',
      version: 'v1',
      kind: 'Deployment',
      namespaced: true,
    });
  });
});
