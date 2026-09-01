import React from 'react';
import { formatAge } from './formatters';

// Helper function to safely get nested property value

// Format labels as key=value pairs
export const formatLabels = (
  labels: Record<string, string> | undefined,
): string => {
  // Backend extracts labels directly (not in metadata.labels)
  if (!labels || typeof labels !== 'object') return '-';

  const entries = Object.entries(labels);
  if (entries.length === 0) return '-';

  return (
    entries
      .slice(0, 3) // Show max 3 labels
      .map(([key, value]) => `${key}=${value}`)
      .join(', ') + (entries.length > 3 ? '...' : '')
  );
};

// Format annotations (similar to labels but may be longer)
export const formatAnnotations = (
  annotations: Record<string, string> | undefined,
): string => {
  // Backend now extracts all annotations
  if (!annotations || typeof annotations !== 'object') return '-';

  const entries = Object.entries(annotations);
  if (entries.length === 0) return '-';

  return (
    entries
      .slice(0, 2) // Show max 2 annotations
      .map(([key, value]) => {
        const shortValue =
          value.length > 20 ? `${value.substring(0, 20)}...` : value;
        return `${key}=${shortValue}`;
      })
      .join(', ') + (entries.length > 2 ? '...' : '')
  );
};

// Format service account
export const formatServiceAccount = (item: any): React.ReactElement => {
  // Import here to avoid circular dependency
  const ServiceAccountLink =
    require('../components/ServiceAccountLink').default;

  return (
    <ServiceAccountLink
      serviceAccountName={item.serviceAccountName || 'default'}
      namespace={item.namespace}
    />
  );
};

// Format node information
export const formatNodeName = (item: any): React.ReactElement => {
  // Import here to avoid circular dependency
  const NodeLink = require('../components/NodeLink').default;

  return <NodeLink nodeName={item.nodeName} />;
};

// Format owner references
export const formatOwner = (item: any): React.ReactElement => {
  // Import here to avoid circular dependency
  const OwnerLink = require('../components/OwnerLink').default;

  return <OwnerLink ownerReferences={item.ownerReferences} />;
};

// Format container names
export const formatContainerNames = (item: any): string => {
  // Backend now extracts containers
  const containers = item.containers;
  if (!containers || !Array.isArray(containers)) return '-';

  return containers.map((c: any) => c.name).join(', ');
};

// Format priority class
export const formatPriorityClass = (item: any): string => {
  // Backend now extracts priorityClassName
  return item.priorityClassName || '-';
};

// Format Quality of Service
export const formatQoS = (item: any): string => {
  // Backend now extracts qosClass
  return item.qosClass || '-';
};

// Format Pod IP
export const formatPodIP = (item: any): string => {
  // Backend now extracts podIP
  return item.podIP || '-';
};

// Format access modes
export const formatAccessModes = (item: any): string => {
  // Backend extracts accessModes directly
  const accessModes = item.accessModes;
  if (!accessModes || !Array.isArray(accessModes)) return '-';

  // Short form mapping
  const shortModes: Record<string, string> = {
    ReadWriteOnce: 'RWO',
    ReadOnlyMany: 'ROX',
    ReadWriteMany: 'RWX',
    ReadWriteOncePod: 'RWOP',
  };

  return accessModes.map((mode) => shortModes[mode] || mode).join(',');
};

// Format reclaim policy
export const formatReclaimPolicy = (item: any): string => {
  // Backend now extracts persistentVolumeReclaimPolicy
  return item.persistentVolumeReclaimPolicy || '-';
};

// Format storage class name
export const formatStorageClass = (item: any): string => {
  // Backend extracts storageClassName directly
  return item.storageClassName || '-';
};

// Format capacity/resources
export const formatCapacity = (item: any): string => {
  // Backend extracts capacity directly
  if (item.capacity?.storage) {
    return item.capacity.storage;
  }
  return '-';
};

// Format bound volume/claim
export const formatBoundVolume = (item: any): string => {
  // Backend extracts volumeName directly
  return item.volumeName || '-';
};

export const formatBoundClaim = (item: any): string => {
  // Backend now extracts claimRef
  return item.claimRef?.name || '-';
};

// Format ingress hosts
export const formatIngressHosts = (item: any): string => {
  // Backend extracts rules directly
  const rules = item.rules;
  if (!rules || !Array.isArray(rules)) return '-';

  const hosts = rules
    .map((rule: any) => rule.host)
    .filter(Boolean)
    .slice(0, 3); // Show max 3 hosts

  return hosts.length > 0
    ? hosts.join(', ') + (rules.length > 3 ? '...' : '')
    : '*';
};

// Format ingress address
export const formatIngressAddress = (item: any): string => {
  // Backend extracts loadBalancer directly
  const ingress = item.loadBalancer?.ingress;
  if (!ingress || !Array.isArray(ingress) || ingress.length === 0) return '-';

  const firstIngress = ingress[0];
  return firstIngress.ip || firstIngress.hostname || '-';
};

// Format ingress class
export const formatIngressClass = (item: any): string => {
  // Backend extracts ingressClassName and ingressClass
  return item.ingressClassName || item.ingressClass || '-';
};

// Format ports for ingresses

// Format job completions
export const formatJobCompletions = (item: any): string => {
  // Backend extracts succeeded and completions directly
  const succeeded = item.succeeded || 0;
  const completions = item.completions;

  if (completions) {
    return `${succeeded}/${completions}`;
  }

  return succeeded.toString();
};

// Format job parallelism
export const formatJobParallelism = (item: any): string => {
  // Backend now extracts parallelism
  return item.parallelism?.toString() || '1';
};

// Format backoff limit
export const formatBackoffLimit = (item: any): string => {
  // Backend now extracts backoffLimit
  return item.backoffLimit?.toString() || '6';
};

// Format job duration
export const formatJobDuration = (item: any): string => {
  // Backend extracts startTime and completionTime directly
  const startTime = item.startTime;
  const completionTime = item.completionTime;

  if (!startTime) return '-';

  const endTime = completionTime || new Date().toISOString();
  const start = new Date(startTime);
  const end = new Date(endTime);
  const durationMs = end.getTime() - start.getTime();

  if (durationMs < 1000) return '<1s';
  if (durationMs < 60000) return `${Math.floor(durationMs / 1000)}s`;
  if (durationMs < 3600000) return `${Math.floor(durationMs / 60000)}m`;

  return `${Math.floor(durationMs / 3600000)}h`;
};

// Format cron schedule
export const formatCronSchedule = (item: any): string => {
  // Backend extracts schedule directly
  return item.schedule || '-';
};

// Format suspend status
export const formatSuspendStatus = (item: any): React.ReactElement => {
  // Backend now extracts suspend
  const suspended = item.suspend;

  return (
    <span
      style={{
        color: suspended ? 'var(--warning)' : 'var(--success)',
        fontWeight: '500',
      }}
    >
      {suspended ? 'Yes' : 'No'}
    </span>
  );
};

// Format active jobs count
export const formatActiveJobs = (item: any): string => {
  // Backend extracts active directly
  const active = item.active;
  return Array.isArray(active)
    ? active.length.toString()
    : active?.toString() || '0';
};

// Format last schedule time
export const formatLastSchedule = (item: any): string => {
  // Backend extracts lastScheduleTime directly
  const lastSchedule = item.lastScheduleTime;
  return lastSchedule ? formatAge(lastSchedule) : 'Never';
};

// Format update strategy
export const formatUpdateStrategy = (item: any): string => {
  // Backend now extracts updateStrategy
  const strategy = item.updateStrategy?.type || item.strategy?.type;
  return strategy || 'RollingUpdate';
};

// Format service name for StatefulSets
export const formatServiceName = (item: any): string => {
  // Backend now extracts serviceName
  return item.serviceName || '-';
};

// Format node roles
export const formatNodeRoles = (item: any): string => {
  // Backend extracts labels directly
  const labels = item.labels || {};
  const roles = [];

  // Check for standard role labels
  if (
    labels['node-role.kubernetes.io/master'] !== undefined ||
    labels['node-role.kubernetes.io/control-plane'] !== undefined
  ) {
    roles.push('master');
  }

  if (labels['node-role.kubernetes.io/worker'] !== undefined) {
    roles.push('worker');
  }

  // Check for custom roles
  Object.keys(labels).forEach((key) => {
    if (
      key.startsWith('node-role.kubernetes.io/') &&
      !['master', 'control-plane', 'worker'].includes(key.split('/')[1])
    ) {
      roles.push(key.split('/')[1]);
    }
  });

  return roles.length > 0 ? roles.join(',') : '<none>';
};

// Format node taints
export const formatNodeTaints = (item: any): React.ReactElement => {
  // Backend extracts taints directly
  const taints = item.taints;

  if (!taints || !Array.isArray(taints) || taints.length === 0) {
    return <span style={{ color: 'var(--success)' }}>None</span>;
  }

  const taintCount = taints.length;
  const taintSummary = taints
    .slice(0, 2)
    .map((taint: any) => `${taint.key}:${taint.effect}`)
    .join(', ');

  return (
    <span style={{ color: 'var(--warning)' }}>
      {taintSummary}
      {taintCount > 2 ? ` +${taintCount - 2} more` : ''}
    </span>
  );
};

// Format node version
export const formatNodeVersion = (item: any): string => {
  // Backend extracts kubeletVersion as 'version'
  return item.version || '-';
};

// Format node OS image
export const formatNodeOSImage = (item: any): string => {
  // Backend now extracts osImage
  return item.osImage || '-';
};

// Format node kernel version
export const formatNodeKernelVersion = (item: any): string => {
  // Backend now extracts kernelVersion
  return item.kernelVersion || '-';
};

// Format container runtime
export const formatContainerRuntime = (item: any): string => {
  // Backend now extracts containerRuntimeVersion
  return item.containerRuntimeVersion || '-';
};

// Format node internal IP
export const formatNodeInternalIP = (item: any): string => {
  // Backend now extracts node addresses
  const addresses = item.addresses;
  if (!addresses || !Array.isArray(addresses)) return '-';

  const internalIP = addresses.find((addr: any) => addr.type === 'InternalIP');
  return internalIP?.address || '-';
};

// Format node external IP
export const formatNodeExternalIP = (item: any): string => {
  // Backend now extracts node addresses
  const addresses = item.addresses;
  if (addresses && Array.isArray(addresses)) {
    const externalIP = addresses.find(
      (addr: any) => addr.type === 'ExternalIP',
    );
    if (externalIP?.address) return externalIP.address;
  }

  // Fall back to service external IP logic for services
  if (item.loadBalancer?.ingress) {
    const ingress = item.loadBalancer.ingress;
    if (Array.isArray(ingress) && ingress.length > 0) {
      return ingress[0].ip || ingress[0].hostname || '-';
    }
  }
  return '-';
};

// Format allocatable resources
export const formatAllocatableCPU = (item: any): string => {
  // Backend now extracts allocatable resources
  return item.allocatable?.cpu || '-';
};

export const formatAllocatableMemory = (item: any): string => {
  // Backend now extracts allocatable resources
  return item.allocatable?.memory || '-';
};

// Format secrets count for service accounts
export const formatSecretsCount = (item: any): string => {
  // Backend extracts secretsCount directly
  return item.secretsCount?.toString() || '0';
};

// Format automount service account token
export const formatAutomountToken = (item: any): React.ReactElement => {
  // Backend now extracts automountServiceAccountToken
  const automount = item.automountServiceAccountToken;
  const color = automount === false ? 'var(--warning)' : 'var(--success)';

  return (
    <span style={{ color, fontWeight: '500' }}>
      {automount === false ? 'No' : 'Yes'}
    </span>
  );
};

// Format session affinity
export const formatSessionAffinity = (item: any): string => {
  // Backend now extracts sessionAffinity
  return item.sessionAffinity || 'None';
};

// Format binary data count
export const formatBinaryDataCount = (item: any): string => {
  // Backend now extracts binaryDataCount
  return item.binaryDataCount?.toString() || '0';
};
