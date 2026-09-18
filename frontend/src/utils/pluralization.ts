/**
 * Kubernetes resource pluralization.
 *
 * The API server's discovery data is the authority on resource names, and the
 * tree, search results and recents carry that spelling wherever the backend
 * has it. These helpers are the fallback for kinds the app has not seen from
 * discovery. They accept either spelling, so callers may pass whatever they
 * hold: `pluralize('pods')` is `pods`, `singularize('ingress')` is `ingress`.
 */

/** Lowercase kind -> plural resource name for every built-in kind. */
const KNOWN_RESOURCE_NAMES: Record<string, string> = {
  pod: 'pods',
  service: 'services',
  configmap: 'configmaps',
  secret: 'secrets',
  namespace: 'namespaces',
  node: 'nodes',
  serviceaccount: 'serviceaccounts',
  persistentvolume: 'persistentvolumes',
  persistentvolumeclaim: 'persistentvolumeclaims',
  endpoints: 'endpoints',
  event: 'events',
  limitrange: 'limitranges',
  resourcequota: 'resourcequotas',
  replicationcontroller: 'replicationcontrollers',
  podtemplate: 'podtemplates',
  componentstatus: 'componentstatuses',
  deployment: 'deployments',
  replicaset: 'replicasets',
  statefulset: 'statefulsets',
  daemonset: 'daemonsets',
  controllerrevision: 'controllerrevisions',
  job: 'jobs',
  cronjob: 'cronjobs',
  ingress: 'ingresses',
  ingressclass: 'ingressclasses',
  networkpolicy: 'networkpolicies',
  ipaddress: 'ipaddresses',
  servicecidr: 'servicecidrs',
  endpointslice: 'endpointslices',
  gatewayclass: 'gatewayclasses',
  gateway: 'gateways',
  httproute: 'httproutes',
  grpcroute: 'grpcroutes',
  role: 'roles',
  rolebinding: 'rolebindings',
  clusterrole: 'clusterroles',
  clusterrolebinding: 'clusterrolebindings',
  storageclass: 'storageclasses',
  volumeattachment: 'volumeattachments',
  csidriver: 'csidrivers',
  csinode: 'csinodes',
  horizontalpodautoscaler: 'horizontalpodautoscalers',
  poddisruptionbudget: 'poddisruptionbudgets',
  podsecuritypolicy: 'podsecuritypolicies',
  priorityclass: 'priorityclasses',
  runtimeclass: 'runtimeclasses',
  lease: 'leases',
  certificatesigningrequest: 'certificatesigningrequests',
  apiservice: 'apiservices',
  mutatingwebhookconfiguration: 'mutatingwebhookconfigurations',
  validatingwebhookconfiguration: 'validatingwebhookconfigurations',
  customresourcedefinition: 'customresourcedefinitions',
};

/** Plural resource name -> lowercase kind, the inverse of the table above. */
const KNOWN_KINDS: Record<string, string> = Object.fromEntries(
  Object.entries(KNOWN_RESOURCE_NAMES).map(([kind, plural]) => [plural, kind]),
);

/**
 * Plural resource name for a kind, the way the API server spells it:
 * Deployment -> deployments, NetworkPolicy -> networkpolicies,
 * Ingress -> ingresses, Gateway -> gateways, Prometheus -> prometheuses.
 * A resource name passes through unchanged.
 */
export function pluralize(kind: string): string {
  const lower = kind.trim().toLowerCase();
  if (!lower) return lower;
  const known = KNOWN_RESOURCE_NAMES[lower];
  if (known) return known;
  if (KNOWN_KINDS[lower]) return lower;
  // ingress, storageclass, ipaddress, prometheus, componentstatus
  if (lower.endsWith('ss') || lower.endsWith('us')) return `${lower}es`;
  // Already a resource name (pods, gateways). Kinds ending in a bare s are
  // rare enough that reading the input as plural is the safer choice.
  if (lower.endsWith('s')) return lower;
  if (/(x|z|ch|sh)$/.test(lower)) return `${lower}es`;
  // policy -> policies, but gateway -> gateways
  if (/[^aeiou]y$/.test(lower)) return `${lower.slice(0, -1)}ies`;
  return `${lower}s`;
}

/**
 * Lowercase kind for a plural resource name: ingresses -> ingress,
 * networkpolicies -> networkpolicy, leases -> lease, statuses -> status.
 * A kind passes through unchanged (lowercased).
 */
export function singularize(resource: string): string {
  const lower = resource.trim().toLowerCase();
  if (!lower) return lower;
  const known = KNOWN_KINDS[lower];
  if (known) return known;
  if (KNOWN_RESOURCE_NAMES[lower]) return lower;
  if (lower.endsWith('ies')) return `${lower.slice(0, -3)}y`;
  // Only -sses/-uses/-xes/-zes/-ches/-shes drop an "es"; other -ses words
  // (leases, releases) end in a silent e and drop just the "s".
  if (/(sses|uses|xes|zes|ches|shes)$/.test(lower)) return lower.slice(0, -2);
  if (lower.endsWith('s') && !lower.endsWith('ss')) return lower.slice(0, -1);
  return lower;
}

/**
 * Whether a value is spelled like a plural resource name (all lowercase and
 * already plural) rather than a Kind. Used to recognise documents indexed by
 * older releases that stored `deployments` where the Kind belongs.
 */
export function isResourceName(value: string): boolean {
  const trimmed = value.trim();
  return (
    trimmed !== '' &&
    trimmed === trimmed.toLowerCase() &&
    pluralize(trimmed) === trimmed
  );
}
