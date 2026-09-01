import React from 'react';
import * as formatColumnValue from './columnFormatters';
import StatusIndicator from '../components/common/StatusIndicator';
import { StatusBadge, RestartBadge, ReadyBadge } from './badgeRenderers';
import ResourceLink from '../components/ResourceLink';
import LiveAge from '../components/common/LiveAge';
import { isRolloutComplete } from '../store/utils';

type ColumnFormatter = (
  item: any,
  selectedNode?: any,
  rolloutStatuses?: Map<string, any>,
  handleNamespaceChange?: (namespace: string) => void,
  cluster?: string,
) => string | React.ReactElement;

const columnFormatters: Record<string, ColumnFormatter> = {
  MESSAGE: (item) => item.message || '-',
  'INVOLVED OBJECT': (item, _selectedNode, _rolloutStatuses, _handleNamespaceChange, cluster) => {
    if (item.involvedObject) {
      const { kind, name, namespace, apiVersion } = item.involvedObject;
      if (!kind || !name) return name || '-';
      if (!cluster || !apiVersion) return `${kind}: ${name}`;
      return React.createElement(ResourceLink, {
        cluster,
        apiVersion,
        kind,
        name,
        namespace,
        openInDetailTab: true,
        className: 'involved-object-link',
      }, `${kind}: ${name}`);
    }
    return '-';
  },
  SOURCE: (item) => {
    if (item.source) {
      const { component, host } = item.source;
      return component ? (host ? `${component} ${host}` : component) : '-';
    }
    return '-';
  },
  COUNT: (item) => {
    const count = item.count !== undefined && item.count !== null ? item.count : 1;
    let tone = 'neutral';
    if (count >= 3 && count < 10) tone = 'warning';
    if (count >= 10) tone = 'danger';
    return React.createElement('span', { className: `count-badge ${tone}` }, count.toString());
  },
  'LAST SEEN': (item) => React.createElement(LiveAge, { timestamp: item.lastTimestamp || item.eventTime }),

  NAME: (item) => item.name || '-',

  CHART: (item) => item.chart || '-',
  UPDATED: (item) => React.createElement(LiveAge, { timestamp: item.updated || item.creationTimestamp }),

  NAMESPACE: (item, _selectedNode, _rolloutStatuses, handleNamespaceChange) => {
    if (!item.namespace) return '-';
    return React.createElement(
      'span',
      {
        className: 'namespace-link',
        onClick: (e: React.MouseEvent) => {
          e.stopPropagation();
          handleNamespaceChange?.(item.namespace);
        },
        title: `Filter by namespace: ${item.namespace}`,
      },
      item.namespace,
    );
  },

  AGE: (item) => React.createElement(LiveAge, { timestamp: item.creationTimestamp }),

  STATUS: (item) => React.createElement(StatusBadge, { item }),
  READY: (item) => React.createElement(ReadyBadge, { item }),
  PHASE: (item) => item.phase || '-',
  RESTARTS: (item) =>
    React.createElement(RestartBadge, { restarts: item.restarts }),

  NODE: (item) => formatColumnValue.formatNodeName(item),
  'NOMINATED NODE': (item) => item.nominatedNodeName || '-',
  CONTAINERS: (item) =>
    React.createElement(StatusIndicator, {
      items: item.containerStatuses,
      initItems: item.initContainerStatuses,
      type: 'container',
      showCount: false,
    }),

  'SERVICE ACCOUNT': (item) => formatColumnValue.formatServiceAccount(item),

  REPLICAS: (item) => {
    if (item.replicas !== undefined) {
      const readyReplicas = item.readyReplicas || 0;
      return `${readyReplicas}/${item.replicas}`;
    }
    return '-';
  },

  ROLLOUT: (item, selectedNode, rolloutStatuses) => {
    const resourceName = selectedNode?.data?.name;
    const rolloutKey = selectedNode?.data && resourceName
      ? `${selectedNode.data.group || '_'}/${selectedNode.data.version}/${resourceName}/${item.namespace || '_'}/${item.name}`
      : null;
    const rolloutStatus = rolloutKey ? rolloutStatuses?.get(rolloutKey) : null;
    if (rolloutStatus) {
      const status = rolloutStatus.status;
      const isProgressing = status === 'Progressing' || status === 'Updating';
      const isComplete = status === 'Complete';
      const isScaledToZero = status === 'Scaled to 0';
      const className = isComplete ? 'rollout-badge complete' : isProgressing ? 'rollout-badge progressing' : isScaledToZero ? 'rollout-badge scaled-zero' : 'rollout-badge';
      const progressPercent = rolloutStatus.replicas > 0 ? Math.round((rolloutStatus.updatedReplicas / rolloutStatus.replicas) * 100) : 0;
      const displayText = isComplete ? 'Ready' : isScaledToZero ? 'Stopped' : `Progressing (${progressPercent}%)`;
      return React.createElement('span', { className, title: rolloutStatus.message || status }, displayText);
    }
    const replicas = item.replicas ?? item.desiredNumberScheduled;
    if (replicas === undefined) return '-';
    if (replicas === 0) return React.createElement('span', { className: 'rollout-badge scaled-zero', title: 'No replicas' }, 'Stopped');
    if (isRolloutComplete(item)) {
      return React.createElement('span', { className: 'rollout-badge complete', title: 'All replicas ready' }, 'Ready');
    }
    const ready = item.readyReplicas ?? item.numberReady ?? 0;
    const updated = item.updatedReplicas ?? item.updatedNumberScheduled ?? replicas;
    const progress = Math.round((Math.min(ready, updated) / replicas) * 100);
    return React.createElement('span', { className: 'rollout-badge progressing', title: `${ready}/${replicas} ready, ${updated}/${replicas} updated` }, `Progressing (${progress}%)`);
  },

  STRATEGY: (item) => formatColumnValue.formatUpdateStrategy(item),
  'UPDATE STRATEGY': (item) => formatColumnValue.formatUpdateStrategy(item),

  SELECTOR: (item) => {
    if (item.selector) {
      return Object.entries(item.selector)
        .map(([k, v]) => `${k}=${v}`)
        .join(',');
    }
    if (item.spec?.selector) {
      if (
        typeof item.spec.selector === 'object' &&
        !Array.isArray(item.spec.selector)
      ) {
        const matchLabels =
          item.spec.selector.matchLabels || item.spec.selector;
        return Object.entries(matchLabels)
          .map(([k, v]) => `${k}=${v}`)
          .join(',');
      }
    }
    return '-';
  },

  TYPE: (item) => {
    const type = item.type || item.secretType;
    if (!type) return '-';

    if (type === 'Warning') {
      return React.createElement('span', { className: 'event-type-badge danger' }, type);
    }
    if (type === 'Normal') {
      return React.createElement('span', { className: 'event-type-badge success' }, type);
    }

    const typeMap: Record<string, string> = {
      'kubernetes.io/service-account-token': 'ServiceAccountToken',
      'kubernetes.io/dockerconfigjson': 'DockerConfig',
      'kubernetes.io/dockercfg': 'DockerCfg',
      'kubernetes.io/basic-auth': 'BasicAuth',
      'kubernetes.io/ssh-auth': 'SSHAuth',
      'kubernetes.io/tls': 'TLS',
      'bootstrap.kubernetes.io/token': 'BootstrapToken',
      'helm.sh/release.v1': 'HelmRelease',
      'connection.crossplane.io/v1alpha1': 'CrossplaneConn',
    };

    return typeMap[type] || type;
  },
  'CLUSTER-IP': (item) => item.clusterIP || '-',

  'EXTERNAL-IP': (item) => {
    if (item.loadBalancer?.ingress) {
      const ingress = item.loadBalancer.ingress;
      if (Array.isArray(ingress) && ingress.length > 0) {
        return ingress[0].ip || ingress[0].hostname || '-';
      }
    }
    return formatColumnValue.formatNodeExternalIP(item);
  },

  PORTS: (item) => {
    if (item.ports && Array.isArray(item.ports) && item.ports.length > 0) {
      return item.ports
        .map(
          (p: any) =>
            `${p.port}${p.nodePort ? `:${p.nodePort}` : ''}${
              p.protocol && p.protocol !== 'TCP' ? `/${p.protocol}` : ''
            }`,
        )
        .join(', ');
    }
    return '-';
  },
  'PORT(S)': (item) => columnFormatters['PORTS'](item),

  LABELS: (item) => formatColumnValue.formatLabels(item.labels),
  ANNOTATIONS: (item) => formatColumnValue.formatAnnotations(item.annotations),
  OWNER: (item) => formatColumnValue.formatOwner(item),

  DATA: (item) => (item.dataCount !== undefined ? `${item.dataCount}` : '-'),
  'BINARY DATA': (item) => formatColumnValue.formatBinaryDataCount(item),
  CAPACITY: (item) => formatColumnValue.formatCapacity(item),
  'ACCESS MODES': (item) => formatColumnValue.formatAccessModes(item),
  'ACCESS-MODES': (item) => formatColumnValue.formatAccessModes(item),
  'RECLAIM POLICY': (item) => formatColumnValue.formatReclaimPolicy(item),
  'STORAGE CLASS': (item) => formatColumnValue.formatStorageClass(item),
  'STORAGE-CLASS': (item) => formatColumnValue.formatStorageClass(item),
  VOLUME: (item) => formatColumnValue.formatBoundVolume(item),
  CLAIM: (item) => formatColumnValue.formatBoundClaim(item),

  ROLES: (item) => formatColumnValue.formatNodeRoles(item),
  TAINTS: (item) => formatColumnValue.formatNodeTaints(item),
  VERSION: (item) => {
    if (item.chart) return item.chartVersion || item.appVersion || '-';
    return formatColumnValue.formatNodeVersion(item);
  },
  'INTERNAL-IP': (item) => formatColumnValue.formatNodeInternalIP(item),
  'OS-IMAGE': (item) => formatColumnValue.formatNodeOSImage(item),
  'KERNEL-VERSION': (item) => formatColumnValue.formatNodeKernelVersion(item),
  'CONTAINER-RUNTIME': (item) => formatColumnValue.formatContainerRuntime(item),
  'ALLOCATABLE CPU': (item) => formatColumnValue.formatAllocatableCPU(item),
  'ALLOCATABLE MEMORY': (item) =>
    formatColumnValue.formatAllocatableMemory(item),

  SECRETS: (item) => formatColumnValue.formatSecretsCount(item),
  'AUTOMOUNT TOKEN': (item) => formatColumnValue.formatAutomountToken(item),

  CLASS: (item) => item.gatewayClassName || formatColumnValue.formatIngressClass(item),
  HOSTS: (item) => formatColumnValue.formatIngressHosts(item),
  ADDRESS: (item) => {
    if (item.addresses && Array.isArray(item.addresses)) {
      const addressValues = item.addresses.map((addr: any) => addr.value || addr).filter(Boolean);
      if (addressValues.length > 0) {
        return addressValues.join(', ');
      }
    }
    return formatColumnValue.formatIngressAddress(item);
  },

  CONTROLLER: (item) => item.controller || item.controllerName || '-',
  PARAMETERS: (item) => {
    if (item.parameters) {
      const { kind, name } = item.parameters;
      return kind && name ? `${kind}/${name}` : '-';
    }
    return '-';
  },

  ACCEPTED: (item) => {
    if (item.conditions && Array.isArray(item.conditions)) {
      const acceptedCondition = item.conditions.find((c: any) => c.type === 'Accepted');
      if (acceptedCondition) {
        return acceptedCondition.status === 'True' ? 'True' : 'False';
      }
    }
    return '-';
  },

  PROGRAMMED: (item) => {
    if (item.conditions && Array.isArray(item.conditions)) {
      const programmedCondition = item.conditions.find((c: any) => c.type === 'Programmed');
      if (programmedCondition) {
        return programmedCondition.status === 'True' ? 'True' : 'False';
      }
    }
    return '-';
  },

  HOSTNAMES: (item) => {
    if (item.hostnames && Array.isArray(item.hostnames)) {
      return item.hostnames.join(', ');
    }
    return '-';
  },

  COMPLETIONS: (item) => formatColumnValue.formatJobCompletions(item),
  PARALLELISM: (item) => formatColumnValue.formatJobParallelism(item),
  'BACKOFF LIMIT': (item) => formatColumnValue.formatBackoffLimit(item),
  DURATION: (item) => formatColumnValue.formatJobDuration(item),

  SCHEDULE: (item) => formatColumnValue.formatCronSchedule(item),
  SUSPEND: (item) => formatColumnValue.formatSuspendStatus(item),
  ACTIVE: (item) => formatColumnValue.formatActiveJobs(item),
  'LAST SCHEDULE': (item) => formatColumnValue.formatLastSchedule(item),
  'LAST-SCHEDULE': (item) => formatColumnValue.formatLastSchedule(item),

  'SERVICE NAME': (item) => formatColumnValue.formatServiceName(item),
  DESIRED: (item) =>
    item.desiredNumberScheduled?.toString() || item.replicas?.toString() || '-',
  CURRENT: (item) =>
    item.currentNumberScheduled?.toString() ||
    item.statusReplicas?.toString() ||
    '-',
  'UP-TO-DATE': (item) =>
    item.updatedNumberScheduled?.toString() ||
    item.updatedReplicas?.toString() ||
    '-',
  AVAILABLE: (item) => {
    // For DaemonSets/Deployments: show replica counts
    if (item.numberAvailable !== undefined || item.availableReplicas !== undefined) {
      return item.numberAvailable?.toString() || item.availableReplicas?.toString() || '-';
    }
    // For APIServices: show availability condition
    if (item.conditions && Array.isArray(item.conditions)) {
      const available = item.conditions.find((c: any) => c.type === 'Available');
      if (available) {
        return available.status === 'True' ? 'True' : 'False';
      }
    }
    return '-';
  },
  REVISION: (item) => {
    if (item.chart && typeof item.revision === 'number') return item.revision.toString();
    return item.annotations?.['deployment.kubernetes.io/revision'] || '-';
  },

  'SESSION AFFINITY': (item) => formatColumnValue.formatSessionAffinity(item),

  PRIORITY: (item) => formatColumnValue.formatPriorityClass(item),
  QOS: (item) => formatColumnValue.formatQoS(item),
  IP: (item) => formatColumnValue.formatPodIP(item),

  KIND: (item) => item.kind || '-',
  
  ROLE: (item) => {
    if (item.roleRef) {
      const { kind, name } = item.roleRef;
      return kind && name ? `${kind}/${name}` : name || '-';
    }
    return item.roleName || '-';
  },

  ENDPOINTS: (item) => {
    if (item.subsets && Array.isArray(item.subsets)) {
      const addresses = item.subsets.flatMap((subset: any) => {
        const addrs = subset.addresses || [];
        const ports = subset.ports || [];
        if (ports.length > 0) {
          return addrs.map((addr: any) => 
            `${addr.ip}:${ports[0].port}`
          );
        }
        return addrs.map((addr: any) => addr.ip);
      });
      if (addresses.length > 0) {
        return addresses.join(', ');
      }
    }
    if (item.endpoints && Array.isArray(item.endpoints)) {
      const addresses = item.endpoints
        .flatMap((ep: any) => ep.addresses || [])
        .slice(0, 3)
        .join(', ');
      if (addresses) {
        return addresses;
      }
    }
    return React.createElement(
      'span',
      { className: 'smart-value-empty', style: { fontStyle: 'italic', color: 'var(--text-muted)' } },
      'none'
    );
  },

  ADDRESSTYPE: (item) => item.addressType || '-',

  HANDLER: (item) => item.handler || '-',

  HOLDER: (item) => {
    if (item.holderIdentity) {
      return item.holderIdentity;
    }
    if (item.spec?.holderIdentity) {
      return item.spec.holderIdentity;
    }
    return '-';
  },

  SIGNER: (item) => item.signerName || item.spec?.signerName || '-',
  
  REQUESTOR: (item) => item.username || item.spec?.username || '-',

  CONDITION: (item) => {
    if (item.conditions && Array.isArray(item.conditions)) {
      const approved = item.conditions.find((c: any) => c.type === 'Approved');
      const denied = item.conditions.find((c: any) => c.type === 'Denied');
      if (approved) return 'Approved';
      if (denied) return 'Denied';
      return 'Pending';
    }
    return '-';
  },

  SERVICE: (item) => {
    if (item.service) {
      const { namespace, name } = item.service;
      return namespace && name ? `${namespace}/${name}` : name || '-';
    }
    if (item.spec?.service) {
      const { namespace, name } = item.spec.service;
      return namespace && name ? `${namespace}/${name}` : name || '-';
    }
    return '-';
  },

  WEBHOOKS: (item) => {
    if (item.webhooks && Array.isArray(item.webhooks)) {
      return item.webhooks.length.toString();
    }
    return '-';
  },

  'MIN AVAILABLE': (item) => {
    if (item.minAvailable !== undefined && item.minAvailable !== null) {
      return item.minAvailable.toString();
    }
    return 'N/A';
  },

  'MAX UNAVAILABLE': (item) => {
    if (item.maxUnavailable !== undefined && item.maxUnavailable !== null) {
      return item.maxUnavailable.toString();
    }
    return 'N/A';
  },

  'ALLOWED DISRUPTIONS': (item) => {
    if (item.disruptionsAllowed !== undefined) {
      return item.disruptionsAllowed.toString();
    }
    return '-';
  },

  SYNC: (item) => {
    const v = item.syncStatus || '-';
    const cls = v.toLowerCase() === 'synced' ? 'success' : v.toLowerCase() === 'outofsync' ? 'warning' : 'neutral';
    return React.createElement('span', { className: `status-badge ${cls}`, title: `Sync: ${v}` }, v);
  },
  HEALTH: (item) => {
    const v = item.healthStatus || '-';
    const lc = v.toLowerCase();
    let cls = 'neutral';
    if (lc === 'healthy') cls = 'success';
    else if (lc === 'degraded' || lc === 'missing') cls = 'danger';
    else if (lc === 'progressing') cls = 'warning';
    return React.createElement('span', { className: `status-badge ${cls}`, title: `Health: ${v}` }, v);
  },
  PROJECT: (item) => item.project || '-',
  'TARGET NAMESPACE': (item) => item.destNamespace || '-',
  DESCRIPTION: (item) => item.description || '-',
};

export const getColumnValue = (
  item: any,
  column: string,
  selectedNode?: any,
  rolloutStatuses?: Map<string, any>,
  handleNamespaceChange?: (namespace: string) => void,
  cluster?: string,
): string | React.ReactElement => {
  const formatter = columnFormatters[column];
  if (formatter) {
    return formatter(
      item,
      selectedNode,
      rolloutStatuses,
      handleNamespaceChange,
      cluster,
    );
  }
  return '-';
};
