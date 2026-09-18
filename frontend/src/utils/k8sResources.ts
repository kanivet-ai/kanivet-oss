/**
 * Built-in Kubernetes resource metadata: kind, plural resource name, API
 * group and version, and scope, looked up by either spelling. Discovery is
 * authoritative for a live cluster and the tree carries what it reports; this
 * registry answers for owner references, links and search results before the
 * tree has loaded, and for kinds it lists but the cluster does not serve.
 */

import { pluralize, singularize } from './pluralization';

export interface ResourceDefinition {
  /** Kind as the API server spells it, e.g. Deployment. */
  kind: string;
  /** Plural resource name, e.g. deployments. */
  plural: string;
  group: string;
  version: string;
  namespaced: boolean;
  navigable: boolean;
}

const def = (
  kind: string,
  plural: string,
  group: string,
  version: string,
  namespaced: boolean,
): ResourceDefinition => ({
  kind,
  plural,
  group,
  version,
  namespaced,
  navigable: true,
});

const DEFINITIONS: ResourceDefinition[] = [
  // Core
  def('Pod', 'pods', '', 'v1', true),
  def('Service', 'services', '', 'v1', true),
  def('ConfigMap', 'configmaps', '', 'v1', true),
  def('Secret', 'secrets', '', 'v1', true),
  def('Namespace', 'namespaces', '', 'v1', false),
  def('Node', 'nodes', '', 'v1', false),
  def('ServiceAccount', 'serviceaccounts', '', 'v1', true),
  def('PersistentVolume', 'persistentvolumes', '', 'v1', false),
  def('PersistentVolumeClaim', 'persistentvolumeclaims', '', 'v1', true),
  def('Endpoints', 'endpoints', '', 'v1', true),
  def('Event', 'events', '', 'v1', true),
  def('LimitRange', 'limitranges', '', 'v1', true),
  def('ResourceQuota', 'resourcequotas', '', 'v1', true),
  def('ReplicationController', 'replicationcontrollers', '', 'v1', true),
  def('PodTemplate', 'podtemplates', '', 'v1', true),
  def('ComponentStatus', 'componentstatuses', '', 'v1', false),
  // Apps
  def('Deployment', 'deployments', 'apps', 'v1', true),
  def('ReplicaSet', 'replicasets', 'apps', 'v1', true),
  def('StatefulSet', 'statefulsets', 'apps', 'v1', true),
  def('DaemonSet', 'daemonsets', 'apps', 'v1', true),
  def('ControllerRevision', 'controllerrevisions', 'apps', 'v1', true),
  // Batch
  def('Job', 'jobs', 'batch', 'v1', true),
  def('CronJob', 'cronjobs', 'batch', 'v1', true),
  // Networking
  def('Ingress', 'ingresses', 'networking.k8s.io', 'v1', true),
  def('IngressClass', 'ingressclasses', 'networking.k8s.io', 'v1', false),
  def('NetworkPolicy', 'networkpolicies', 'networking.k8s.io', 'v1', true),
  def('EndpointSlice', 'endpointslices', 'discovery.k8s.io', 'v1', true),
  def(
    'GatewayClass',
    'gatewayclasses',
    'gateway.networking.k8s.io',
    'v1',
    false,
  ),
  def('Gateway', 'gateways', 'gateway.networking.k8s.io', 'v1', true),
  def('HTTPRoute', 'httproutes', 'gateway.networking.k8s.io', 'v1', true),
  def('GRPCRoute', 'grpcroutes', 'gateway.networking.k8s.io', 'v1', true),
  // RBAC
  def('Role', 'roles', 'rbac.authorization.k8s.io', 'v1', true),
  def('RoleBinding', 'rolebindings', 'rbac.authorization.k8s.io', 'v1', true),
  def('ClusterRole', 'clusterroles', 'rbac.authorization.k8s.io', 'v1', false),
  def(
    'ClusterRoleBinding',
    'clusterrolebindings',
    'rbac.authorization.k8s.io',
    'v1',
    false,
  ),
  // Storage
  def('StorageClass', 'storageclasses', 'storage.k8s.io', 'v1', false),
  def('VolumeAttachment', 'volumeattachments', 'storage.k8s.io', 'v1', false),
  def('CSIDriver', 'csidrivers', 'storage.k8s.io', 'v1', false),
  def('CSINode', 'csinodes', 'storage.k8s.io', 'v1', false),
  // Autoscaling and policy
  def(
    'HorizontalPodAutoscaler',
    'horizontalpodautoscalers',
    'autoscaling',
    'v2',
    true,
  ),
  def('PodDisruptionBudget', 'poddisruptionbudgets', 'policy', 'v1', true),
  // Cluster
  def('PriorityClass', 'priorityclasses', 'scheduling.k8s.io', 'v1', false),
  def('RuntimeClass', 'runtimeclasses', 'node.k8s.io', 'v1', false),
  def('Lease', 'leases', 'coordination.k8s.io', 'v1', true),
  def(
    'CertificateSigningRequest',
    'certificatesigningrequests',
    'certificates.k8s.io',
    'v1',
    false,
  ),
  def('APIService', 'apiservices', 'apiregistration.k8s.io', 'v1', false),
  def(
    'MutatingWebhookConfiguration',
    'mutatingwebhookconfigurations',
    'admissionregistration.k8s.io',
    'v1',
    false,
  ),
  def(
    'ValidatingWebhookConfiguration',
    'validatingwebhookconfigurations',
    'admissionregistration.k8s.io',
    'v1',
    false,
  ),
  def(
    'CustomResourceDefinition',
    'customresourcedefinitions',
    'apiextensions.k8s.io',
    'v1',
    false,
  ),
];

const BY_KIND = new Map(DEFINITIONS.map((d) => [d.kind.toLowerCase(), d]));
const BY_PLURAL = new Map(DEFINITIONS.map((d) => [d.plural, d]));

/**
 * Definition of a built-in resource by kind (`Deployment`, `deployment`) or by
 * resource name (`deployments`), or undefined for anything else.
 */
export function getResourceDefinition(
  kindOrResource: string,
): ResourceDefinition | undefined {
  const lower = kindOrResource.trim().toLowerCase();
  if (!lower) return undefined;
  return (
    BY_KIND.get(lower) ??
    BY_PLURAL.get(lower) ??
    BY_KIND.get(singularize(lower))
  );
}

/** Kind as the API server spells it for a built-in kind or resource name. */
export function getKindName(kindOrResource: string): string | undefined {
  return getResourceDefinition(kindOrResource)?.kind;
}

function getResourceMetadata(kind: string): ResourceDefinition {
  return (
    getResourceDefinition(kind) || {
      kind,
      plural: pluralize(kind),
      group: '',
      version: 'v1',
      namespaced: true,
      navigable: false,
    }
  );
}

/** API group for a resource kind. */
export function getApiGroup(kind: string): string {
  return getResourceMetadata(kind).group;
}

/** API version for a resource kind. */
export function getApiVersion(kind: string): string {
  return getResourceMetadata(kind).version;
}

/** Whether a resource kind is namespaced. */
export function isNamespaced(kind: string): boolean {
  return getResourceMetadata(kind).namespaced;
}

/** Whether a resource kind can be navigated to in the UI. */
export function isNavigable(kind: string): boolean {
  return getResourceMetadata(kind).navigable;
}

/** Plural resource name for a kind or resource name. */
export function getPluralKind(kind: string): string {
  return getResourceMetadata(kind).plural;
}
