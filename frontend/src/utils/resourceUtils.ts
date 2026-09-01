/**
 * Convert a Kubernetes Kind to its plural resource name
 * This handles common Kubernetes naming patterns
 */
export const kindToResource = (kind: string): string => {
  // Handle special cases first
  const specialCases: Record<string, string> = {
    Pod: 'pods',
    Service: 'services',
    Ingress: 'ingresses',
    NetworkPolicy: 'networkpolicies',
    Endpoints: 'endpoints',
    EndpointSlice: 'endpointslices',
    ConfigMap: 'configmaps',
    Secret: 'secrets',
    ServiceAccount: 'serviceaccounts',
    PersistentVolume: 'persistentvolumes',
    PersistentVolumeClaim: 'persistentvolumeclaims',
    StorageClass: 'storageclasses',
    VolumeAttachment: 'volumeattachments',
    Namespace: 'namespaces',
    Node: 'nodes',
    Role: 'roles',
    ClusterRole: 'clusterroles',
    RoleBinding: 'rolebindings',
    ClusterRoleBinding: 'clusterrolebindings',
    Deployment: 'deployments',
    StatefulSet: 'statefulsets',
    DaemonSet: 'daemonsets',
    ReplicaSet: 'replicasets',
    Job: 'jobs',
    CronJob: 'cronjobs',
    HorizontalPodAutoscaler: 'horizontalpodautoscalers',
    PodDisruptionBudget: 'poddisruptionbudgets',
    LimitRange: 'limitranges',
    ResourceQuota: 'resourcequotas',
    PriorityClass: 'priorityclasses',
    CustomResourceDefinition: 'customresourcedefinitions',
    MutatingWebhookConfiguration: 'mutatingwebhookconfigurations',
    ValidatingWebhookConfiguration: 'validatingwebhookconfigurations',
    Certificate: 'certificates',
    Issuer: 'issuers',
    ClusterIssuer: 'clusterissuers',
    // Crossplane specific
    Provider: 'providers',
    ProviderConfig: 'providerconfigs',
    Composition: 'compositions',
    CompositeResourceDefinition: 'compositeresourcedefinitions',
    Configuration: 'configurations',
    ConfigurationRevision: 'configurationrevisions',
  };

  // Check special cases
  if (specialCases[kind]) {
    return specialCases[kind];
  }

  // Convert to lowercase for processing
  let resource = kind.toLowerCase();

  // Handle common suffixes
  if (resource.endsWith('y')) {
    // Check if the 'y' is preceded by a vowel
    const beforeY = resource[resource.length - 2];
    if (['a', 'e', 'i', 'o', 'u'].includes(beforeY)) {
      // key -> keys, day -> days
      return resource + 's';
    } else {
      // policy -> policies, entity -> entities
      return resource.slice(0, -1) + 'ies';
    }
  } else if (
    resource.endsWith('s') ||
    resource.endsWith('ss') ||
    resource.endsWith('x') ||
    resource.endsWith('ch') ||
    resource.endsWith('sh') ||
    resource.endsWith('z')
  ) {
    // class -> classes, index -> indexes, buzz -> buzzes
    return resource + 'es';
  } else {
    // default: just add 's'
    return resource + 's';
  }
};

export const kindToResourceDef = (kind: string) => {
  const defs: Record<
    string,
    { group: string; version: string; namespaced: boolean }
  > = {
    Pod: { group: '', version: 'v1', namespaced: true },
    Service: { group: '', version: 'v1', namespaced: true },
    Ingress: { group: 'networking.k8s.io', version: 'v1', namespaced: true },
    NetworkPolicy: {
      group: 'networking.k8s.io',
      version: 'v1',
      namespaced: true,
    },
    Endpoints: { group: '', version: 'v1', namespaced: true },
    EndpointSlice: {
      group: 'discovery.k8s.io',
      version: 'v1',
      namespaced: true,
    },
    ConfigMap: { group: '', version: 'v1', namespaced: true },
    Secret: { group: '', version: 'v1', namespaced: true },
    ServiceAccount: { group: '', version: 'v1', namespaced: true },
    PersistentVolume: { group: '', version: 'v1', namespaced: false },
    PersistentVolumeClaim: { group: '', version: 'v1', namespaced: true },
    StorageClass: { group: 'storage.k8s.io', version: 'v1', namespaced: false },
    VolumeAttachment: {
      group: 'storage.k8s.io',
      version: 'v1',
      namespaced: false,
    },
    Namespace: { group: '', version: 'v1', namespaced: false },
    Node: { group: '', version: 'v1', namespaced: false },
    Role: {
      group: 'rbac.authorization.k8s.io',
      version: 'v1',
      namespaced: true,
    },
    ClusterRole: {
      group: 'rbac.authorization.k8s.io',
      version: 'v1',
      namespaced: false,
    },
    RoleBinding: {
      group: 'rbac.authorization.k8s.io',
      version: 'v1',
      namespaced: true,
    },
    ClusterRoleBinding: {
      group: 'rbac.authorization.k8s.io',
      version: 'v1',
      namespaced: false,
    },
    Deployment: { group: 'apps', version: 'v1', namespaced: true },
    StatefulSet: { group: 'apps', version: 'v1', namespaced: true },
    DaemonSet: { group: 'apps', version: 'v1', namespaced: true },
    ReplicaSet: { group: 'apps', version: 'v1', namespaced: true },
    Job: { group: 'batch', version: 'v1', namespaced: true },
    CronJob: { group: 'batch', version: 'v1', namespaced: true },
  };
  return defs[kind];
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
