import { FocusArea } from '../types';
import { TreeNode, TabState, Tab, MonitoringSettings } from './types';

export const isRolloutComplete = (item: any): boolean => {
  const replicas = item.replicas ?? item.desiredNumberScheduled ?? 0;
  if (replicas === 0) return false;
  const statusReplicas = item.statusReplicas ?? replicas;
  const ready = item.readyReplicas ?? item.numberReady ?? 0;
  const updated = item.updatedReplicas ?? item.updatedNumberScheduled ?? replicas;
  const available = item.availableReplicas ?? item.numberAvailable;
  return statusReplicas <= replicas && updated >= replicas && ready >= replicas && (available === undefined || available >= replicas);
};

export const createInitialTabState = (): TabState => ({
  treeData: [],
  selectedNode: null,
  listItems: [],
  selectedItem: null,
  detailData: null,
  searchQuery: '',
  searchMatches: [],
  focusArea: 'tree' as FocusArea,
  isDetailsPanelCollapsed: false,
  expandedNodes: new Set<string>(),
  pinnedDetails: [],
  namespaces: [],
  selectedNamespace: 'all',
  selectedNamespaces: [],
  sortBy: 'age',
  sortOrder: 'desc',
  rolloutStatuses: new Map(),
  rolloutRequests: new Map(),
  detailTabs: [],
  activeDetailTab: null,
  resourceListTabs: [],
  activeResourceListTab: null,
  activeResourceListTabByPane: {},
  focusedCenterPaneId: 'root',
  isLoadingListItems: false,
  hasReceivedInitialListData: false,
  loadError: undefined,
  scrollPositions: {},
});

export const loadMonitoringSettings = (): MonitoringSettings => {
  try {
    const saved = localStorage.getItem('kanivet.monitoringSettings');
    if (saved) return JSON.parse(saved);
  } catch {}
  return { preferredProvider: 'auto', autoRefreshInterval: 30, showMetricsPanel: true };
};

export const rebuildTabIndex = (tabs: Tab[]): Map<string, number> => {
  const map = new Map<string, number>();
  tabs.forEach((tab, idx) => map.set(tab.id, idx));
  return map;
};

export const updateTreeNode = (
  tree: TreeNode[],
  nodeId: string,
  children: TreeNode[],
  expandedNodes?: Set<string>,
): TreeNode[] => {
  return tree.map((node: TreeNode) => {
    if (node.id === nodeId) {
      const shouldExpand = expandedNodes ? expandedNodes.has(nodeId) : true;
      return { ...node, children, expanded: shouldExpand };
    }
    if (node.children) {
      return { ...node, children: updateTreeNode(node.children, nodeId, children, expandedNodes) };
    }
    return node;
  });
};

export const findNodeById = (nodes: TreeNode[], id: string): TreeNode | null => {
  for (const node of nodes) {
    if (node.id === id) return node;
    if (node.children) {
      const found = findNodeById(node.children, id);
      if (found) return found;
    }
  }
  return null;
};

export const predefinedCategories: Record<string, any[]> = {
  workloads: [
    { name: 'pods', group: '', version: 'v1', kind: 'Pod', namespaced: true },
    { name: 'deployments', group: 'apps', version: 'v1', kind: 'Deployment', namespaced: true },
    { name: 'statefulsets', group: 'apps', version: 'v1', kind: 'StatefulSet', namespaced: true },
    { name: 'daemonsets', group: 'apps', version: 'v1', kind: 'DaemonSet', namespaced: true },
    { name: 'jobs', group: 'batch', version: 'v1', kind: 'Job', namespaced: true },
    { name: 'cronjobs', group: 'batch', version: 'v1', kind: 'CronJob', namespaced: true },
  ],
  networking: [
    { name: 'services', group: '', version: 'v1', kind: 'Service', namespaced: true },
    { name: 'endpoints', group: '', version: 'v1', kind: 'Endpoints', namespaced: true },
    { name: 'ingresses', group: 'networking.k8s.io', version: 'v1', kind: 'Ingress', namespaced: true },
    { name: 'ingressclasses', group: 'networking.k8s.io', version: 'v1', kind: 'IngressClass', namespaced: false },
    { name: 'gatewayclasses', group: 'gateway.networking.k8s.io', version: 'v1', kind: 'GatewayClass', namespaced: false },
    { name: 'gateways', group: 'gateway.networking.k8s.io', version: 'v1', kind: 'Gateway', namespaced: true },
    { name: 'httproutes', group: 'gateway.networking.k8s.io', version: 'v1', kind: 'HTTPRoute', namespaced: true },
    { name: 'grpcroutes', group: 'gateway.networking.k8s.io', version: 'v1', kind: 'GRPCRoute', namespaced: true },
    { name: 'networkpolicies', group: 'networking.k8s.io', version: 'v1', kind: 'NetworkPolicy', namespaced: true },
    { name: 'endpointslices', group: 'discovery.k8s.io', version: 'v1', kind: 'EndpointSlice', namespaced: true },
  ],
  config: [
    { name: 'configmaps', group: '', version: 'v1', kind: 'ConfigMap', namespaced: true },
    { name: 'secrets', group: '', version: 'v1', kind: 'Secret', namespaced: true },
    { name: 'serviceaccounts', group: '', version: 'v1', kind: 'ServiceAccount', namespaced: true },
    { name: 'limitranges', group: '', version: 'v1', kind: 'LimitRange', namespaced: true },
    { name: 'resourcequotas', group: '', version: 'v1', kind: 'ResourceQuota', namespaced: true },
    { name: 'horizontalpodautoscalers', group: 'autoscaling', version: 'v2', kind: 'HorizontalPodAutoscaler', namespaced: true },
  ],
  storage: [
    { name: 'persistentvolumes', group: '', version: 'v1', kind: 'PersistentVolume', namespaced: false },
    { name: 'persistentvolumeclaims', group: '', version: 'v1', kind: 'PersistentVolumeClaim', namespaced: true },
    { name: 'storageclasses', group: 'storage.k8s.io', version: 'v1', kind: 'StorageClass', namespaced: false },
    { name: 'volumeattachments', group: 'storage.k8s.io', version: 'v1', kind: 'VolumeAttachment', namespaced: false },
  ],
  rbac: [
    { name: 'roles', group: 'rbac.authorization.k8s.io', version: 'v1', kind: 'Role', namespaced: true },
    { name: 'rolebindings', group: 'rbac.authorization.k8s.io', version: 'v1', kind: 'RoleBinding', namespaced: true },
    { name: 'clusterroles', group: 'rbac.authorization.k8s.io', version: 'v1', kind: 'ClusterRole', namespaced: false },
    { name: 'clusterrolebindings', group: 'rbac.authorization.k8s.io', version: 'v1', kind: 'ClusterRoleBinding', namespaced: false },
  ],
  cluster: [
    { name: 'nodes', group: '', version: 'v1', kind: 'Node', namespaced: false },
    { name: 'namespaces', group: '', version: 'v1', kind: 'Namespace', namespaced: false },
    { name: 'persistentvolumes', group: '', version: 'v1', kind: 'PersistentVolume', namespaced: false },
    { name: 'storageclasses', group: 'storage.k8s.io', version: 'v1', kind: 'StorageClass', namespaced: false },
    { name: 'priorityclasses', group: 'scheduling.k8s.io', version: 'v1', kind: 'PriorityClass', namespaced: false },
  ],
};

export const kindSpecificColumns: Record<string, string[]> = {
  pod: ['CONTAINERS', 'STATUS', 'READY', 'RESTARTS', 'AGE'],
  pods: ['CONTAINERS', 'STATUS', 'READY', 'RESTARTS', 'AGE'],
  application: ['SYNC', 'HEALTH', 'PROJECT', 'TARGET NAMESPACE', 'AGE'],
  applications: ['SYNC', 'HEALTH', 'PROJECT', 'TARGET NAMESPACE', 'AGE'],
  applicationset: ['HEALTH', 'AGE'],
  applicationsets: ['HEALTH', 'AGE'],
  appproject: ['DESCRIPTION', 'AGE'],
  appprojects: ['DESCRIPTION', 'AGE'],
  deployment: ['READY', 'REPLICAS', 'ROLLOUT', 'AGE'],
  deployments: ['READY', 'REPLICAS', 'ROLLOUT', 'AGE'],
  replicaset: ['DESIRED', 'CURRENT', 'READY', 'AGE'],
  replicasets: ['DESIRED', 'CURRENT', 'READY', 'AGE'],
  statefulset: ['READY', 'ROLLOUT', 'AGE'],
  statefulsets: ['READY', 'ROLLOUT', 'AGE'],
  daemonset: ['DESIRED', 'CURRENT', 'READY', 'UP-TO-DATE', 'AVAILABLE', 'ROLLOUT', 'AGE'],
  daemonsets: ['DESIRED', 'CURRENT', 'READY', 'UP-TO-DATE', 'AVAILABLE', 'ROLLOUT', 'AGE'],
  service: ['TYPE', 'CLUSTER-IP', 'EXTERNAL-IP', 'PORT(S)', 'AGE'],
  services: ['TYPE', 'CLUSTER-IP', 'EXTERNAL-IP', 'PORT(S)', 'AGE'],
  configmap: ['AGE'],
  configmaps: ['AGE'],
  secret: ['TYPE', 'AGE'],
  secrets: ['TYPE', 'AGE'],
  persistentvolume: ['CAPACITY', 'ACCESS MODES', 'RECLAIM POLICY', 'STATUS', 'STORAGE CLASS', 'AGE'],
  persistentvolumes: ['CAPACITY', 'ACCESS MODES', 'RECLAIM POLICY', 'STATUS', 'STORAGE CLASS', 'AGE'],
  persistentvolumeclaim: ['STATUS', 'VOLUME', 'CAPACITY', 'ACCESS MODES', 'STORAGE CLASS', 'AGE'],
  persistentvolumeclaims: ['STATUS', 'VOLUME', 'CAPACITY', 'ACCESS MODES', 'STORAGE CLASS', 'AGE'],
  ingress: ['CLASS', 'HOSTS', 'ADDRESS', 'PORTS', 'AGE'],
  ingresses: ['CLASS', 'HOSTS', 'ADDRESS', 'PORTS', 'AGE'],
  ingressclass: ['CONTROLLER', 'PARAMETERS', 'AGE'],
  ingressclasses: ['CONTROLLER', 'PARAMETERS', 'AGE'],
  gatewayclass: ['CONTROLLER', 'ACCEPTED', 'AGE'],
  gatewayclasses: ['CONTROLLER', 'ACCEPTED', 'AGE'],
  gateway: ['CLASS', 'ADDRESS', 'PROGRAMMED', 'AGE'],
  gateways: ['CLASS', 'ADDRESS', 'PROGRAMMED', 'AGE'],
  httproute: ['HOSTNAMES', 'AGE'],
  httproutes: ['HOSTNAMES', 'AGE'],
  grpcroute: ['HOSTNAMES', 'AGE'],
  grpcroutes: ['HOSTNAMES', 'AGE'],
  job: ['COMPLETIONS', 'DURATION', 'AGE'],
  jobs: ['COMPLETIONS', 'DURATION', 'AGE'],
  cronjob: ['SCHEDULE', 'SUSPEND', 'ACTIVE', 'LAST SCHEDULE', 'AGE'],
  cronjobs: ['SCHEDULE', 'SUSPEND', 'ACTIVE', 'LAST SCHEDULE', 'AGE'],
  namespace: ['STATUS', 'AGE'],
  namespaces: ['STATUS', 'AGE'],
  event: ['TYPE', 'MESSAGE', 'INVOLVED OBJECT', 'SOURCE', 'COUNT', 'AGE', 'LAST SEEN'],
  events: ['TYPE', 'MESSAGE', 'INVOLVED OBJECT', 'SOURCE', 'COUNT', 'AGE', 'LAST SEEN'],
  node: ['STATUS', 'ROLES', 'AGE', 'VERSION'],
  nodes: ['STATUS', 'ROLES', 'AGE', 'VERSION'],
  serviceaccount: ['SECRETS', 'AGE'],
  serviceaccounts: ['SECRETS', 'AGE'],
  runtimeclass: ['HANDLER', 'AGE'],
  runtimeclasses: ['HANDLER', 'AGE'],
  lease: ['HOLDER', 'AGE'],
  leases: ['HOLDER', 'AGE'],
  certificatesigningrequest: ['SIGNER', 'REQUESTOR', 'CONDITION', 'AGE'],
  certificatesigningrequests: ['SIGNER', 'REQUESTOR', 'CONDITION', 'AGE'],
  apiservice: ['SERVICE', 'AVAILABLE', 'AGE'],
  apiservices: ['SERVICE', 'AVAILABLE', 'AGE'],
  mutatingwebhookconfiguration: ['WEBHOOKS', 'AGE'],
  mutatingwebhookconfigurations: ['WEBHOOKS', 'AGE'],
  validatingwebhookconfiguration: ['WEBHOOKS', 'AGE'],
  validatingwebhookconfigurations: ['WEBHOOKS', 'AGE'],
  poddisruptionbudget: ['MIN AVAILABLE', 'MAX UNAVAILABLE', 'ALLOWED DISRUPTIONS', 'AGE'],
  poddisruptionbudgets: ['MIN AVAILABLE', 'MAX UNAVAILABLE', 'ALLOWED DISRUPTIONS', 'AGE'],
  role: ['AGE'],
  roles: ['AGE'],
  rolebinding: ['ROLE', 'AGE'],
  rolebindings: ['ROLE', 'AGE'],
  clusterrole: ['AGE'],
  clusterroles: ['AGE'],
  clusterrolebinding: ['ROLE', 'AGE'],
  clusterrolebindings: ['ROLE', 'AGE'],
  endpoints: ['ENDPOINTS', 'AGE'],
  endpointslice: ['ADDRESSTYPE', 'PORTS', 'ENDPOINTS', 'AGE'],
  endpointslices: ['ADDRESSTYPE', 'PORTS', 'ENDPOINTS', 'AGE'],
  helmrelease: ['CHART', 'VERSION', 'STATUS', 'REVISION', 'UPDATED'],
  helmreleases: ['CHART', 'VERSION', 'STATUS', 'REVISION', 'UPDATED'],
};
