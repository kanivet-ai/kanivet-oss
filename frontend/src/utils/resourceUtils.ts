import { getResourceDefinition } from './k8sResources';
import { pluralize } from './pluralization';

/**
 * Plural resource name for a Kubernetes kind, e.g. Deployment -> deployments.
 * Accepts a resource name too and returns it unchanged.
 */
export const kindToResource = (kind: string): string => pluralize(kind);

/**
 * API group, version and scope of a built-in kind (or resource name), or
 * undefined for kinds the app does not know without discovery.
 */
export const kindToResourceDef = (
  kind: string,
): { group: string; version: string; namespaced: boolean } | undefined => {
  const definition = getResourceDefinition(kind);
  if (!definition) return undefined;
  const { group, version, namespaced } = definition;
  return { group, version, namespaced };
};

/**
 * Determine which category a resource belongs to
 */
export const getResourceCategory = (
  apiGroup: string,
  resourceName: string,
): string => {
  // Check if it's an Argo CD resource
  if (apiGroup === 'argoproj.io' || apiGroup.endsWith('.argoproj.io')) {
    return 'argocd';
  }

  // Check if it's a Crossplane resource
  if (apiGroup.includes('crossplane.io')) {
    return 'crossplane';
  }

  // Check if it's a custom resource (has a group that's not k8s.io)
  if (
    apiGroup &&
    !apiGroup.includes('k8s.io') &&
    !apiGroup.includes('kubernetes.io') &&
    apiGroup !== 'apps' && // apps is a standard Kubernetes API group
    apiGroup !== 'batch' && // batch is a standard Kubernetes API group
    apiGroup !== 'autoscaling' && // autoscaling is a standard Kubernetes API group
    apiGroup !== 'policy' // policy is a standard Kubernetes API group
  ) {
    // Could be a Crossplane claim or other custom resource
    if (
      apiGroup.includes('.io') ||
      apiGroup.includes('.com') ||
      apiGroup.includes('.org')
    ) {
      return 'crossplane'; // Likely a Crossplane claim
    }
    return 'custom';
  }

  // Map standard resources to categories
  const categoryMap: Record<string, string[]> = {
    workloads: [
      'pods',
      'deployments',
      'statefulsets',
      'daemonsets',
      'jobs',
      'cronjobs',
      'replicasets',
    ],
    networking: [
      'services',
      'ingresses',
      'networkpolicies',
      'endpoints',
      'endpointslices',
    ],
    storage: [
      'persistentvolumes',
      'persistentvolumeclaims',
      'storageclasses',
      'volumeattachments',
    ],
    config: ['configmaps', 'secrets'],
    rbac: [
      'roles',
      'rolebindings',
      'clusterroles',
      'clusterrolebindings',
      'serviceaccounts',
    ],
    cluster: [
      'nodes',
      'namespaces',
      'persistentvolumes',
      'storageclasses',
      'priorityclasses',
      'customresourcedefinitions',
      'mutatingwebhookconfigurations',
      'validatingwebhookconfigurations',
    ],
  };

  for (const [category, resources] of Object.entries(categoryMap)) {
    if (resources.includes(resourceName)) {
      return category;
    }
  }

  return 'custom';
};

/**
 * Parse apiVersion into group and version
 */
export const parseApiVersion = (
  apiVersion: string,
): { group: string; version: string } => {
  if (apiVersion.includes('/')) {
    const [group, version] = apiVersion.split('/');
    return { group, version };
  }
  return { group: '', version: apiVersion };
};
