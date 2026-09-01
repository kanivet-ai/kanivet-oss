/**
 * Kubernetes resource metadata and helpers
 * Centralized configuration for all K8s resource types
 */

import { pluralize, singularize } from './pluralization';

interface ResourceMetadata {
  group: string;
  version: string;
  namespaced: boolean;
  navigable?: boolean;
}

// Resource metadata registry
const resourceRegistry: Record<string, ResourceMetadata> = {
  // Core resources (empty group)
  pod: { group: '', version: 'v1', namespaced: true, navigable: true },
  service: { group: '', version: 'v1', namespaced: true, navigable: true },
  configmap: { group: '', version: 'v1', namespaced: true, navigable: true },
  secret: { group: '', version: 'v1', namespaced: true, navigable: true },
  namespace: { group: '', version: 'v1', namespaced: false, navigable: true },
  node: { group: '', version: 'v1', namespaced: false, navigable: true },
  serviceaccount: {
    group: '',
    version: 'v1',
    namespaced: true,
    navigable: true,
  },
  persistentvolume: {
    group: '',
    version: 'v1',
    namespaced: false,
    navigable: true,
  },
  persistentvolumeclaim: {
    group: '',
    version: 'v1',
    namespaced: true,
    navigable: true,
  },

  // Apps resources
  deployment: {
    group: 'apps',
    version: 'v1',
    namespaced: true,
    navigable: true,
  },
  replicaset: {
    group: 'apps',
    version: 'v1',
    namespaced: true,
    navigable: true,
  },
  statefulset: {
    group: 'apps',
    version: 'v1',
    namespaced: true,
    navigable: true,
  },
  daemonset: {
    group: 'apps',
    version: 'v1',
    namespaced: true,
    navigable: true,
  },

  // Batch resources
  job: { group: 'batch', version: 'v1', namespaced: true, navigable: true },
  cronjob: { group: 'batch', version: 'v1', namespaced: true, navigable: true },

  // Networking resources
  ingress: {
    group: 'networking.k8s.io',
    version: 'v1',
    namespaced: true,
    navigable: true,
  },
  networkpolicy: {
    group: 'networking.k8s.io',
    version: 'v1',
    namespaced: true,
    navigable: true,
  },

  // RBAC resources
  role: {
    group: 'rbac.authorization.k8s.io',
    version: 'v1',
    namespaced: true,
    navigable: true,
  },
  rolebinding: {
    group: 'rbac.authorization.k8s.io',
    version: 'v1',
    namespaced: true,
    navigable: true,
  },
  clusterrole: {
    group: 'rbac.authorization.k8s.io',
    version: 'v1',
    namespaced: false,
    navigable: true,
  },
  clusterrolebinding: {
    group: 'rbac.authorization.k8s.io',
    version: 'v1',
    namespaced: false,
    navigable: true,
  },

  // Storage resources
  storageclass: {
    group: 'storage.k8s.io',
    version: 'v1',
    namespaced: false,
    navigable: true,
  },

  // Autoscaling resources
  horizontalpodautoscaler: {
    group: 'autoscaling',
    version: 'v2',
    namespaced: true,
    navigable: true,
  },

  // Policy resources
  poddisruptionbudget: {
    group: 'policy',
    version: 'v1',
    namespaced: true,
    navigable: true,
  },
};

function getResourceMetadata(kind: string): ResourceMetadata {
  const key = singularize(kind.toLowerCase());
  return (
    resourceRegistry[key] || {
      group: '',
      version: 'v1',
      namespaced: true,
      navigable: false,
    }
  );
}

/**
 * Get API group for a resource kind
 */
export function getApiGroup(kind: string): string {
  return getResourceMetadata(kind).group;
}

/**
 * Get API version for a resource kind
 */
export function getApiVersion(kind: string): string {
  return getResourceMetadata(kind).version;
}

/**
 * Check if a resource kind is namespaced
 */
export function isNamespaced(kind: string): boolean {
  return getResourceMetadata(kind).namespaced;
}

/**
 * Check if a resource kind is navigable in the UI
 */
export function isNavigable(kind: string): boolean {
  return getResourceMetadata(kind).navigable || false;
}

/**
 * Get the plural form of a resource kind
 */
export function getPluralKind(kind: string): string {
  return pluralize(kind);
}
