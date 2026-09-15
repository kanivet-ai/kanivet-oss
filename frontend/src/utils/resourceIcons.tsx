import type { ReactNode } from 'react';
import {
  CertificateIcon,
  ClusterIcon,
  ClusterIssuerIcon,
  ClusterRoleBindingIcon,
  ClusterRoleIcon,
  ConfigIcon,
  ConfigMapIcon,
  CronJobIcon,
  CustomResourceDefinitionIcon,
  DaemonSetIcon,
  DeploymentIcon,
  EndpointSliceIcon,
  EndpointsIcon,
  EventIcon,
  FinOpsIcon,
  FolderIcon,
  HorizontalPodAutoscalerIcon,
  IngressIcon,
  IssuerIcon,
  JobIcon,
  LimitRangeIcon,
  MutatingWebhookIcon,
  NamespaceIcon,
  NetworkIcon,
  NetworkPolicyIcon,
  NodeIcon,
  OverviewIcon,
  PackageIcon,
  PersistentVolumeClaimIcon,
  PersistentVolumeIcon,
  PodDisruptionBudgetIcon,
  PodIcon,
  PriorityClassIcon,
  RbacIcon,
  ReplicaSetIcon,
  ResourceIcon,
  ResourceQuotaIcon,
  RoleBindingIcon,
  RoleIcon,
  SecretIcon,
  ServiceAccountIcon,
  ServiceIcon,
  StatefulSetIcon,
  StorageClassIcon,
  StorageIcon,
  ValidatingWebhookIcon,
  VerticalPodAutoscalerIcon,
  VirtualClusterIcon,
  WorkloadsIcon,
} from '../components/icons/kube';
import HelmIcon from '../components/icons/HelmIcon';
import CrossplaneIcon from '../components/icons/CrossplaneIcon';
import ArgoIcon from '../components/icons/ArgoIcon';
import KanivetMark from '../components/icons/KanivetMark';

type IconRenderer = ReactNode;

/**
 * Registers a kind under every spelling the app passes around: the singular `kind` field from
 * tabs and search results, the plural display label from the tree, and any short aliases.
 */
const register = (
  map: Record<string, IconRenderer>,
  icon: IconRenderer,
  ...names: string[]
): void => {
  for (const name of names) map[name.toLowerCase()] = icon;
};

const buildKindIcons = (): Record<string, IconRenderer> => {
  const m: Record<string, IconRenderer> = {};

  register(m, <OverviewIcon />, 'clusterdashboard', 'overview');
  register(m, <FinOpsIcon />, 'finopsdashboard', 'finops');
  register(m, <EventIcon />, 'event', 'events');

  // Workloads
  register(m, <PodIcon />, 'pod', 'pods');
  register(m, <DeploymentIcon />, 'deployment', 'deployments');
  register(m, <ReplicaSetIcon />, 'replicaset', 'replicasets');
  register(m, <StatefulSetIcon />, 'statefulset', 'statefulsets');
  register(m, <DaemonSetIcon />, 'daemonset', 'daemonsets');
  register(m, <JobIcon />, 'job', 'jobs');
  register(m, <CronJobIcon />, 'cronjob', 'cronjobs');
  register(
    m,
    <HorizontalPodAutoscalerIcon />,
    'horizontalpodautoscaler',
    'horizontalpodautoscalers',
    'hpa',
  );
  register(
    m,
    <VerticalPodAutoscalerIcon />,
    'verticalpodautoscaler',
    'verticalpodautoscalers',
    'vpa',
  );
  register(
    m,
    <PodDisruptionBudgetIcon />,
    'poddisruptionbudget',
    'poddisruptionbudgets',
    'pdb',
  );

  // Networking
  register(m, <ServiceIcon />, 'service', 'services');
  register(m, <IngressIcon />, 'ingress', 'ingresses');
  register(m, <EndpointsIcon />, 'endpoint', 'endpoints');
  register(m, <EndpointSliceIcon />, 'endpointslice', 'endpointslices');
  register(m, <NetworkPolicyIcon />, 'networkpolicy', 'networkpolicies');

  // Config & policy
  register(m, <ConfigMapIcon />, 'configmap', 'configmaps');
  register(m, <SecretIcon />, 'secret', 'secrets');
  register(m, <ResourceQuotaIcon />, 'resourcequota', 'resourcequotas');
  register(m, <LimitRangeIcon />, 'limitrange', 'limitranges');
  register(m, <PriorityClassIcon />, 'priorityclass', 'priorityclasses');

  // Storage
  register(
    m,
    <PersistentVolumeIcon />,
    'persistentvolume',
    'persistentvolumes',
  );
  register(
    m,
    <PersistentVolumeClaimIcon />,
    'persistentvolumeclaim',
    'persistentvolumeclaims',
  );
  register(m, <StorageClassIcon />, 'storageclass', 'storageclasses');

  // Cluster
  register(m, <NamespaceIcon />, 'namespace', 'namespaces');
  register(m, <NodeIcon />, 'node', 'nodes');

  // RBAC
  register(m, <ServiceAccountIcon />, 'serviceaccount', 'serviceaccounts');
  register(m, <RoleIcon />, 'role', 'roles');
  register(m, <ClusterRoleIcon />, 'clusterrole', 'clusterroles');
  register(m, <RoleBindingIcon />, 'rolebinding', 'rolebindings');
  register(
    m,
    <ClusterRoleBindingIcon />,
    'clusterrolebinding',
    'clusterrolebindings',
  );

  // Extension & admission
  register(
    m,
    <CustomResourceDefinitionIcon />,
    'customresourcedefinition',
    'customresourcedefinitions',
    'crd',
  );
  register(
    m,
    <MutatingWebhookIcon />,
    'mutatingwebhookconfiguration',
    'mutatingwebhookconfigurations',
  );
  register(
    m,
    <ValidatingWebhookIcon />,
    'validatingwebhookconfiguration',
    'validatingwebhookconfigurations',
  );

  // cert-manager
  register(m, <CertificateIcon />, 'certificate', 'certificates');
  register(m, <IssuerIcon />, 'issuer', 'issuers');
  register(m, <ClusterIssuerIcon />, 'clusterissuer', 'clusterissuers');

  return m;
};

const buildCategoryIcons = (): Record<string, IconRenderer> => {
  const m: Record<string, IconRenderer> = {};

  register(m, <KanivetMark size={15} />, 'kanivetide', 'kakauide');
  register(m, <OverviewIcon />, 'overview');
  register(m, <WorkloadsIcon />, 'workloads');
  register(m, <ConfigIcon />, 'config', 'configs');
  register(m, <NetworkIcon />, 'network', 'networking');
  register(m, <StorageIcon />, 'storage');
  register(m, <RbacIcon />, 'rbac');
  register(m, <ClusterIcon />, 'cluster');
  register(
    m,
    <CustomResourceDefinitionIcon />,
    'custom resources',
    'customresources',
    'custom',
  );
  register(m, <PackageIcon />, 'package');
  register(m, <FolderIcon />, 'folder');
  register(m, <HelmIcon />, 'helm', 'helm releases');
  register(m, <FinOpsIcon />, 'finops');
  register(m, <CrossplaneIcon />, 'crossplane');
  register(m, <ArgoIcon />, 'argocd', 'argo cd', 'argo');
  register(
    m,
    <VirtualClusterIcon />,
    'vclusters',
    'virtual clusters',
    'vcluster',
  );

  return m;
};

// Built once at module load: these lookups run inside virtualized tree and list rows.
const KIND_ICONS = buildKindIcons();
const CATEGORY_ICONS = buildCategoryIcons();
const FALLBACK_ICON: IconRenderer = <ResourceIcon />;

/** Icon for a Kubernetes kind, matched case-insensitively by singular, plural, or short alias. */
export const getResourceIcon = (kind: string): IconRenderer =>
  KIND_ICONS[kind.toLowerCase()] ?? FALLBACK_ICON;

/** Icon for a sidebar section or grouping node. */
export const getCategoryIcon = (category: string): IconRenderer =>
  CATEGORY_ICONS[category.toLowerCase()] ?? FALLBACK_ICON;
