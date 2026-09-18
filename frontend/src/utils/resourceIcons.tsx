import type { ReactNode } from 'react';
import * as K from '../components/icons/kube';
import HelmIcon from '../components/icons/HelmIcon';
import CrossplaneIcon from '../components/icons/CrossplaneIcon';
import ArgoIcon from '../components/icons/ArgoIcon';
import KanivetMark from '../components/icons/KanivetMark';
import { pluralize } from './pluralization';

type IconRenderer = ReactNode;

/**
 * Registers an icon under every spelling the app passes around: the singular `kind` from tabs,
 * search results and custom-resource nodes, the plural resource name the tree shows for built-in
 * categories, and any short aliases.
 */
const register = (
  map: Record<string, IconRenderer>,
  icon: IconRenderer,
  ...kinds: string[]
): void => {
  for (const kind of kinds) {
    const k = kind.toLowerCase();
    map[k] = icon;
    map[pluralize(k)] = icon;
  }
};

const buildKindIcons = (): Record<string, IconRenderer> => {
  const m: Record<string, IconRenderer> = {};

  register(m, <K.OverviewIcon />, 'clusterdashboard', 'overview');
  register(m, <K.FinOpsIcon />, 'finopsdashboard', 'finops');
  register(m, <K.EventIcon />, 'event');

  // Workloads
  register(m, <K.PodIcon />, 'pod', 'podtemplate');
  register(
    m,
    <K.DeploymentIcon />,
    'deployment',
    'deploymentconfig',
    'rollout',
  );
  register(m, <K.ReplicaSetIcon />, 'replicaset', 'replicationcontroller');
  register(m, <K.StatefulSetIcon />, 'statefulset');
  register(m, <K.DaemonSetIcon />, 'daemonset');
  register(m, <K.JobIcon />, 'job');
  register(m, <K.CronJobIcon />, 'cronjob', 'cronworkflow');
  register(
    m,
    <K.HorizontalPodAutoscalerIcon />,
    'horizontalpodautoscaler',
    'hpa',
    'scaledobject',
    'scaledjob',
  );
  register(m, <K.VerticalPodAutoscalerIcon />, 'verticalpodautoscaler', 'vpa');
  register(m, <K.PodDisruptionBudgetIcon />, 'poddisruptionbudget', 'pdb');
  register(m, <K.ClockIcon />, 'controllerrevision', 'revision', 'lease');

  // Networking
  register(m, <K.ServiceIcon />, 'service', 'serviceentry', 'serverstransport');
  register(m, <K.IngressIcon />, 'ingress');
  register(m, <K.GatewayIcon />, 'gateway');
  register(
    m,
    <K.RouteIcon />,
    'httproute',
    'grpcroute',
    'tcproute',
    'tlsroute',
    'udproute',
    'route',
    'ingressroute',
    'ingressroutetcp',
    'ingressrouteudp',
    'virtualservice',
  );
  register(m, <K.EndpointsIcon />, 'endpoints', 'ciliumendpoint');
  register(m, <K.EndpointSliceIcon />, 'endpointslice');
  register(
    m,
    <K.NetworkPolicyIcon />,
    'networkpolicy',
    'adminnetworkpolicy',
    'baselineadminnetworkpolicy',
    'ciliumnetworkpolicy',
    'ciliumclusterwidenetworkpolicy',
    'backendtlspolicy',
    'destinationrule',
    'peerauthentication',
    'authorizationpolicy',
    'securitygrouppolicy',
  );
  register(
    m,
    <K.RoleBindingIcon />,
    'referencegrant',
    'targetgroupbinding',
    'volumeattachment',
  );
  register(
    m,
    <K.ClusterIcon />,
    'ipaddress',
    'servicecidr',
    'dnsendpoint',
    'ciliumnode',
  );

  // Config & policy
  register(m, <K.ConfigMapIcon />, 'configmap');
  register(
    m,
    <K.SecretIcon />,
    'secret',
    'externalsecret',
    'sealedsecret',
    'pushsecret',
    'triggerauthentication',
    'clustertriggerauthentication',
  );
  register(m, <K.RbacIcon />, 'secretstore', 'clustersecretstore');
  register(
    m,
    <K.ConfigIcon />,
    'kustomization',
    'backendconfig',
    'frontendconfig',
    'eniconfig',
    'configuration',
  );
  register(
    m,
    <K.ResourceQuotaIcon />,
    'resourcequota',
    'resourceclaim',
    'resourceclaimtemplate',
    'resourceslice',
    'csistoragecapacity',
  );
  register(m, <K.LimitRangeIcon />, 'limitrange', 'prioritylevelconfiguration');
  register(m, <K.PriorityClassIcon />, 'priorityclass', 'flowschema');
  register(
    m,
    <K.MutatingWebhookIcon />,
    'mutatingwebhookconfiguration',
    'middleware',
    'envoyfilter',
  );
  register(
    m,
    <K.ValidatingWebhookIcon />,
    'validatingwebhookconfiguration',
    'validatingadmissionpolicy',
    'policyreport',
    'clusterpolicyreport',
    'challenge',
  );
  register(m, <K.RoleBindingIcon />, 'validatingadmissionpolicybinding');

  // Storage
  register(m, <K.PersistentVolumeIcon />, 'persistentvolume');
  register(m, <K.PersistentVolumeClaimIcon />, 'persistentvolumeclaim');
  register(m, <K.StorageIcon />, 'csidriver');
  register(
    m,
    <K.ArchiveIcon />,
    'volumesnapshot',
    'volumesnapshotcontent',
    'backup',
    'restore',
    'backupstoragelocation',
    'volumesnapshotlocation',
    'backuprepository',
  );

  // Any `*Class` kind: a tier or template of something else.
  register(
    m,
    <K.LayersIcon />,
    'storageclass',
    'ingressclass',
    'gatewayclass',
    'runtimeclass',
    'volumeattributesclass',
    'volumesnapshotclass',
    'deviceclass',
    'ec2nodeclass',
    'constrainttemplate',
  );

  // Cluster
  register(m, <K.NamespaceIcon />, 'namespace', 'project');
  register(
    m,
    <K.NodeIcon />,
    'node',
    'csinode',
    'nodepool',
    'nodeclaim',
    'machine',
    'machineset',
    'machinedeployment',
  );
  register(
    m,
    <K.MonitorIcon />,
    'componentstatus',
    'nodemetrics',
    'podmetrics',
  );
  register(m, <K.PackageIcon />, 'apiservice');

  // RBAC & identity
  register(
    m,
    <K.ServiceAccountIcon />,
    'serviceaccount',
    'podidentityassociation',
  );
  register(m, <K.RoleIcon />, 'role');
  register(m, <K.ClusterRoleIcon />, 'clusterrole');
  register(m, <K.RoleBindingIcon />, 'rolebinding');
  register(m, <K.ClusterRoleBindingIcon />, 'clusterrolebinding');
  register(m, <K.PersonIcon />, 'user', 'group', 'identity', 'ciliumidentity');

  // Extension
  register(
    m,
    <K.CustomResourceDefinitionIcon />,
    'customresourcedefinition',
    'crd',
  );

  // Certificates
  register(
    m,
    <K.CertificateIcon />,
    'certificate',
    'certificaterequest',
    'certificatesigningrequest',
    'clustertrustbundle',
    'managedcertificate',
    'tlsoption',
    'tlsstore',
  );
  register(m, <K.IssuerIcon />, 'issuer');
  register(m, <K.ClusterIssuerIcon />, 'clusterissuer');
  register(m, <K.ClockIcon />, 'order', 'schedule');

  // Monitoring
  register(
    m,
    <K.MonitorIcon />,
    'servicemonitor',
    'podmonitor',
    'probe',
    'prometheusrule',
    'prometheus',
    'prometheusagent',
    'alertmanager',
    'alertmanagerconfig',
    'thanosruler',
    'scrapeconfig',
    'analysistemplate',
    'analysisrun',
    'experiment',
  );

  // Delivery
  register(
    m,
    <K.PackageIcon />,
    'application',
    'applicationset',
    'gitrepository',
    'ocirepository',
    'bucket',
    'imagerepository',
    'imagepolicy',
    'imageupdateautomation',
    'imagestream',
  );
  register(m, <K.FolderIcon />, 'appproject');
  register(m, <HelmIcon />, 'helmrelease', 'helmrepository', 'helmchart');
  register(m, <K.EventIcon />, 'receiver', 'provider', 'alert');
  register(
    m,
    <K.PipelineIcon />,
    'workflow',
    'workflowtemplate',
    'clusterworkflowtemplate',
    'pipeline',
    'pipelinerun',
    'task',
    'taskrun',
    'clustertask',
    'build',
    'buildconfig',
  );

  // Policy engines
  register(m, <K.NetworkPolicyIcon />, 'policy', 'clusterpolicy');

  return m;
};

/**
 * Kinds the pack has never heard of still get a fitting glyph when their name says what they are.
 * Checked in order, so the more specific words come first; explicit entries always win.
 */
const INFERRED: [RegExp, IconRenderer][] = [
  [/class(es)?$/, <K.LayersIcon />],
  [/route/, <K.RouteIcon />],
  [/gateway/, <K.GatewayIcon />],
  [/ingress/, <K.IngressIcon />],
  [/polic(y|ies)|constraint/, <K.NetworkPolicyIcon />],
  [/secret|credential|token|password/, <K.SecretIcon />],
  [/cert|issuer|trust|tls/, <K.CertificateIcon />],
  [
    /monitor|alert|prometheus|probe|metric|analysis|telemetry|trace|log/,
    <K.MonitorIcon />,
  ],
  [/backup|restore|snapshot|archive/, <K.ArchiveIcon />],
  [/schedule|cron|lease|timer/, <K.ClockIcon />],
  [/workflow|pipeline|task|build|runs?$/, <K.PipelineIcon />],
  [/scal/, <K.HorizontalPodAutoscalerIcon />],
  [/job/, <K.JobIcon />],
  [/node|machine|host|instance/, <K.NodeIcon />],
  [/volume|storage|disk|bucket/, <K.PersistentVolumeIcon />],
  [/binding|attachment|grant|association|link/, <K.RoleBindingIcon />],
  [/role/, <K.RoleIcon />],
  [/account/, <K.ServiceAccountIcon />],
  [/user|identity|group|member/, <K.PersonIcon />],
  [
    /webhook|mutat|transform|middleware|filter|patch/,
    <K.MutatingWebhookIcon />,
  ],
  [/valid|report|review|audit|check/, <K.ValidatingWebhookIcon />],
  [/config|setting|option|param|kustomiz|profile/, <K.ConfigIcon />],
  [/template|revision|manifest|blueprint|definition/, <K.ResourceIcon />],
  [
    /app|package|chart|repositor|image|bundle|release|module/,
    <K.PackageIcon />,
  ],
  [/namespace|project|tenant|space|environment/, <K.NamespaceIcon />],
  [/service|endpoint|entry|backend|upstream/, <K.ServiceIcon />],
  [/deploy|rollout/, <K.DeploymentIcon />],
  [/replica/, <K.ReplicaSetIcon />],
  [/daemon/, <K.DaemonSetIcon />],
  [/stateful/, <K.StatefulSetIcon />],
  [/pod|container|sidecar/, <K.PodIcon />],
  [/event|notification|receiver|provider|hook/, <K.EventIcon />],
  [/dns|domain|zone|network|cidr|address|subnet|mesh/, <K.ClusterIcon />],
  [/quota|limit|budget|claim|capacity/, <K.ResourceQuotaIcon />],
  [/priority|flow/, <K.PriorityClassIcon />],
  [/cluster/, <K.ClusterIcon />],
];

const buildCategoryIcons = (): Record<string, IconRenderer> => {
  const m: Record<string, IconRenderer> = {};

  register(m, <KanivetMark size={15} />, 'kanivetide', 'kakauide');
  register(m, <K.OverviewIcon />, 'overview');
  register(m, <K.WorkloadsIcon />, 'workloads');
  register(m, <K.ConfigIcon />, 'config', 'configuration');
  register(m, <K.NetworkIcon />, 'network', 'networking');
  register(m, <K.StorageIcon />, 'storage');
  register(m, <K.RbacIcon />, 'rbac');
  register(m, <K.ClusterIcon />, 'cluster');
  register(
    m,
    <K.CustomResourceDefinitionIcon />,
    'custom resources',
    'customresources',
    'custom',
  );
  register(m, <K.PackageIcon />, 'package');
  register(m, <K.FolderIcon />, 'folder');
  register(m, <HelmIcon />, 'helm', 'helm releases');
  register(m, <K.FinOpsIcon />, 'finops');
  register(m, <CrossplaneIcon />, 'crossplane');
  register(m, <ArgoIcon />, 'argocd', 'argo cd', 'argo');
  register(
    m,
    <K.VirtualClusterIcon />,
    'vclusters',
    'virtual clusters',
    'vcluster',
  );

  return m;
};

// Built once at module load: these lookups run inside virtualized tree and list rows.
const KIND_ICONS = buildKindIcons();
const CATEGORY_ICONS = buildCategoryIcons();
const FALLBACK_ICON: IconRenderer = <K.ResourceIcon />;
const INFERRED_CACHE = new Map<string, IconRenderer>();

const inferIcon = (name: string): IconRenderer => {
  const cached = INFERRED_CACHE.get(name);
  if (cached !== undefined) return cached;
  const hit = INFERRED.find(([pattern]) => pattern.test(name));
  const icon = hit ? hit[1] : FALLBACK_ICON;
  INFERRED_CACHE.set(name, icon);
  return icon;
};

/**
 * Icon for a Kubernetes kind, matched case-insensitively by singular kind, plural resource name or
 * short alias, falling back to a glyph inferred from the name.
 */
export const getResourceIcon = (kind: string): IconRenderer => {
  const key = kind.toLowerCase();
  return KIND_ICONS[key] ?? inferIcon(key);
};

/** Icon for a sidebar section or grouping node. */
export const getCategoryIcon = (category: string): IconRenderer =>
  CATEGORY_ICONS[category.toLowerCase()] ?? FALLBACK_ICON;
