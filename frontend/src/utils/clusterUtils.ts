export type CloudProvider = 'aws' | 'gcp' | 'azure' | 'vcluster' | 'other';

interface ParsedClusterInfo {
  provider: CloudProvider;
  isAWS: boolean;  // Kept for backward compatibility
  isVCluster?: boolean;
  region?: string;
  project?: string;  // For GCP
  accountId?: string;  // For AWS
  clusterName: string;
  displayName: string;
  originalContext: string;
  hasAlias: boolean;
  vcluster?: { host: string; namespace: string; name: string };
}

export function parseVClusterId(id: string): { host: string; namespace: string; name: string } | null {
  if (!id.startsWith('vcluster:')) return null;
  const rest = id.slice('vcluster:'.length);
  const lastColon = rest.lastIndexOf(':');
  if (lastColon < 0) return null;
  const name = rest.slice(lastColon + 1);
  const middle = rest.slice(0, lastColon);
  const nsColon = middle.lastIndexOf(':');
  if (nsColon < 0) return null;
  const namespace = middle.slice(nsColon + 1);
  const host = middle.slice(0, nsColon);
  if (!host || !namespace || !name) return null;
  return { host, namespace, name };
}

export function parseClusterName(context: string, alias?: string): ParsedClusterInfo {
  if (typeof context !== 'string') {
    const fallback = String(context);
    return { provider: 'other', isAWS: false, clusterName: fallback, displayName: alias || fallback, originalContext: fallback, hasAlias: !!alias };
  }
  const vc = parseVClusterId(context);
  if (vc) {
    const hostInfo = parseClusterName(vc.host);
    const display = `${hostInfo.displayName} › ${vc.name}`;
    return {
      provider: 'vcluster',
      isAWS: false,
      isVCluster: true,
      clusterName: vc.name,
      displayName: alias || display,
      originalContext: context,
      hasAlias: !!alias,
      vcluster: vc,
    };
  }
  const awsPattern = /^arn:aws:eks:([^:]+):([^:]+):cluster\/(.+)$/;
  const awsMatch = context.match(awsPattern);

  if (awsMatch) {
    const region = awsMatch[1];
    const accountId = awsMatch[2];
    const clusterName = awsMatch[3];
    const hasAlias = !!alias && alias !== clusterName && alias !== context;
    return {
      provider: 'aws',
      isAWS: true,
      region,
      accountId,
      clusterName,
      displayName: alias || clusterName,
      originalContext: context,
      hasAlias,
    };
  }

  // GCP GKE pattern: gke_<project>_<zone/region>_<cluster-name>
  const gkePattern = /^gke_([^_]+)_([^_]+)_(.+)$/;
  const gkeMatch = context.match(gkePattern);

  if (gkeMatch) {
    const project = gkeMatch[1];
    const region = gkeMatch[2];
    const clusterName = gkeMatch[3];
    const hasAlias = !!alias && alias !== clusterName && alias !== context;
    return {
      provider: 'gcp',
      isAWS: false,
      region,
      project,
      clusterName,
      displayName: alias || clusterName,
      originalContext: context,
      hasAlias,
    };
  }

  // Azure AKS patterns - check for common Azure context naming conventions
  // Pattern 1: Contains 'aks' in the name (common convention)
  // Pattern 2: <cluster-name>-<resource-group> format from az aks get-credentials
  const isAzure = context.toLowerCase().includes('aks') || 
                  context.includes('azure') ||
                  context.match(/^[^-]+-[^-]+-aks-[^-]+$/i);  // common naming pattern

  if (isAzure) {
    const clusterName = context;
    const hasAlias = !!alias && alias !== clusterName && alias !== context;
    return {
      provider: 'azure',
      isAWS: false,
      clusterName,
      displayName: alias || clusterName,
      originalContext: context,
      hasAlias,
    };
  }

  // Default: other/unknown provider
  const hasAlias = !!alias && alias !== context;
  return {
    provider: 'other',
    isAWS: false,
    clusterName: context,
    displayName: alias || context,
    originalContext: context,
    hasAlias,
  };
}
