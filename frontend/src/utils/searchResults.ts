/**
 * Turns a search result (or a recent-search entry) into the resource the rest
 * of the app works with: a plural resource name for the list tab, the Kind as
 * the API server spells it for detail views and titles, and the tree node the
 * list should open under.
 *
 * The backend fills in `resource` from discovery; documents written by older
 * releases may instead carry the plural name in `kind`, so both spellings are
 * accepted and normalised here.
 */

import type { TreeNode } from '../store/types';
import type { SearchResult } from '../types/search';
import { getKindName, getResourceDefinition } from './k8sResources';
import { isResourceName, pluralize, singularize } from './pluralization';
import { getResourceCategory, parseApiVersion } from './resourceUtils';

export interface SearchResourceLike {
  cluster: string;
  kind: string;
  name: string;
  namespace?: string;
  apiVersion?: string;
  group?: string;
  version?: string;
  resource?: string;
  labels?: Record<string, string>;
}

export interface ResolvedResource {
  cluster: string;
  group: string;
  version: string;
  apiVersion: string;
  /** Plural API resource name, e.g. deployments. */
  resourceName: string;
  /** Kind as the API server spells it, e.g. Deployment. */
  kind: string;
  namespaced: boolean;
  namespace?: string;
  name: string;
  /** Sidebar category the resource type lives under. */
  categoryId: string;
}

const capitalize = (value: string): string =>
  value ? value.charAt(0).toUpperCase() + value.slice(1) : value;

const groupAndVersion = (
  r: SearchResourceLike,
): { group: string; version: string } => {
  const parsed = parseApiVersion(r.apiVersion || '');
  const group =
    r.group !== undefined && r.group !== null ? r.group : parsed.group;
  const version = r.version || parsed.version || 'v1';
  return { group, version };
};

const apiVersionOf = (group: string, version: string): string =>
  group ? `${group}/${version}` : version;

/** Resolve a regular (non kind-definition) search result. */
export function describeSearchResource(
  r: SearchResourceLike,
): ResolvedResource {
  const { group, version } = groupAndVersion(r);
  const rawKind = (r.kind || '').trim();
  const resourceName =
    r.resource || r.labels?.['resource-name'] || pluralize(rawKind);
  let kind = rawKind;
  if (!kind || isResourceName(kind)) {
    kind = getKindName(resourceName) || capitalize(singularize(resourceName));
  }
  return {
    cluster: r.cluster,
    group,
    version,
    apiVersion: apiVersionOf(group, version),
    resourceName,
    kind,
    namespaced: !!r.namespace,
    namespace: r.namespace || undefined,
    name: r.name,
    categoryId: getResourceCategory(group, resourceName),
  };
}

/**
 * Resolve a kind-definition result, whose `name` is the Kind. Built-in kinds
 * take their coordinates from the registry when the result lacks them.
 */
export function describeKindDefinition(
  r: SearchResourceLike,
): ResolvedResource {
  const kind = r.name;
  const definition = getResourceDefinition(kind);
  const fromResult = groupAndVersion(r);
  const group =
    r.group !== undefined && r.group !== null
      ? r.group
      : (definition?.group ?? fromResult.group);
  const version = r.version || definition?.version || fromResult.version;
  const resourceName =
    r.resource ||
    r.labels?.['resource-name'] ||
    definition?.plural ||
    pluralize(kind);
  const namespaced =
    r.labels?.['namespaced'] !== undefined
      ? r.labels['namespaced'] !== 'false'
      : (definition?.namespaced ?? true);
  return {
    cluster: r.cluster,
    group,
    version,
    apiVersion: apiVersionOf(group, version),
    resourceName,
    kind,
    namespaced,
    name: kind,
    categoryId: getResourceCategory(group, resourceName),
  };
}

/**
 * Identity of the object a result points at, independent of which spelling
 * of its type the document carries. Two results with the same identity are
 * the same object.
 */
export function resourceIdentity(r: SearchResourceLike): string {
  if (r.kind === 'KindDefinition') {
    const { group } = groupAndVersion(r);
    return `kind|${r.cluster}|${group}|${(r.name || '').toLowerCase()}`;
  }
  const d = describeSearchResource(r);
  return `${d.cluster}|${d.group}|${d.resourceName}|${d.namespace || ''}|${d.name}`;
}

/** Keeps the first item for each identity, preserving order. */
export function dedupeByIdentity<T>(
  items: T[],
  identity: (item: T) => string,
): T[] {
  const seen = new Set<string>();
  const out: T[] = [];
  for (const item of items) {
    const key = identity(item);
    if (seen.has(key)) continue;
    seen.add(key);
    out.push(item);
  }
  return out;
}

/** Drops results that point at an object an earlier result already covers. */
export function dedupeSearchResults(results: SearchResult[]): SearchResult[] {
  return dedupeByIdentity(results, (result) =>
    resourceIdentity(result.resource),
  );
}

/**
 * The tree node a resolved resource's list opens under, shaped like the nodes
 * the sidebar builds so tabs, selection and realtime updates line up.
 */
export function resourceListNode(d: ResolvedResource): TreeNode {
  return {
    id: `${d.categoryId}-${d.group || 'core'}-${d.version}-${d.resourceName}`,
    label: d.resourceName,
    type: 'resource',
    data: {
      name: d.resourceName,
      group: d.group,
      version: d.version,
      kind: d.kind,
      namespaced: d.namespaced,
    },
  };
}

/**
 * The tree's own node for a resource type, if the tree has loaded it. The
 * tree's node carries what discovery reported (exact kind, scope, version),
 * so it is preferred over a node built from a search result. With
 * `anyVersion`, a node for another served version of the same resource is
 * accepted when none matches exactly.
 */
export function findTreeResourceNode(
  nodes: TreeNode[],
  group: string,
  version: string,
  resourceName: string,
  options: { anyVersion?: boolean } = {},
): TreeNode | undefined {
  const matches = (node: TreeNode, checkVersion: boolean): boolean =>
    node.type === 'resource' &&
    !!node.data &&
    (node.data.group || '') === (group || '') &&
    node.data.name === resourceName &&
    (!checkVersion || node.data.version === version);
  const walk = (
    list: TreeNode[],
    checkVersion: boolean,
  ): TreeNode | undefined => {
    for (const node of list) {
      if (matches(node, checkVersion)) return node;
      if (node.children) {
        const found = walk(node.children, checkVersion);
        if (found) return found;
      }
    }
    return undefined;
  };
  return (
    walk(nodes, true) ?? (options.anyVersion ? walk(nodes, false) : undefined)
  );
}
