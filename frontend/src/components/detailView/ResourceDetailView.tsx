import React, { useState } from 'react';
import * as ScrollArea from '@radix-ui/react-scroll-area';
import { ChevronDownIcon, ChevronRightIcon, ReloadIcon, TrashIcon, SizeIcon, CubeIcon, BarChartIcon, CheckCircledIcon, PlayIcon } from '@radix-ui/react-icons';
import useResourceNavigation from '../../hooks/useResourceNavigation';
import { formatAge } from '../../utils/formatters';
import PropertyGroup from './shared/PropertyGroup';
import MetricsPropertyGroup from './shared/MetricsPropertyGroup';
import ConditionsView from './shared/ConditionsView';
import SpecificationSection from './shared/SpecificationSection';
import EventsSection from './shared/EventsSection';
import ContainerDropdown from '../ContainerDropdown';
import { PodMetrics } from '../PodMetrics';
import { WorkloadMetrics } from '../WorkloadMetrics';
import api from '../../services/api';
import ClipboardCopy from '../common/ClipboardCopy';
import { useStore } from '../../store';
import CRDDefinitionView from '../CRDDefinitionView';
import APIResourceDefinitionView from '../APIResourceDefinitionView';
import CustomResourceDetailView from './resourceTypes/CustomResourceDetailView';
import ServiceDetailView from './resourceTypes/ServiceDetailView';
import ConfigMapDetailView from './resourceTypes/ConfigMapDetailView';
import SecretDetailView from './resourceTypes/SecretDetailView';
import EndpointsDetailView from './resourceTypes/EndpointsDetailView';
import RoleDetailView from './resourceTypes/RoleDetailView';
import RoleBindingDetailView from './resourceTypes/RoleBindingDetailView';
import ServiceAccountDetailView from './resourceTypes/ServiceAccountDetailView';
import NodeDetailView from './resourceTypes/NodeDetailView';
import EventDetailView from './resourceTypes/EventDetailView';
import ApplicationDetailView from './resourceTypes/ApplicationDetailView';
import MetadataSection from './shared/MetadataSection';
import ScaleDialog from '../dialogs/ScaleDialog';
import Dialog from '../common/Dialog';

const getContainerState = (container: any) => {
  if (container.state) {
    const stateKey = Object.keys(container.state)[0];
    const stateData = container.state[stateKey];
    switch (stateKey) {
      case 'running': return { state: 'Running', details: stateData?.startedAt ? `since ${formatAge(stateData.startedAt)}` : '' };
      case 'terminated': return { state: stateData?.reason === 'Completed' ? 'Completed' : 'Terminated', details: stateData?.reason ? `(${stateData.reason}, exit ${stateData.exitCode || 0})` : '' };
      case 'waiting': return { state: 'Waiting', details: stateData?.reason ? `(${stateData.reason})` : '' };
      default: return { state: 'Unknown', details: '' };
    }
  }
  if (container.ready !== undefined) return { state: container.ready ? 'Ready' : 'Not Ready', details: container.restartCount > 0 ? `(${container.restartCount} restarts)` : '' };
  return { state: 'Loading...', details: '' };
};

const ContainersSection = ({ resource, cluster, onVolumeClick }: { resource: any; cluster: string; onVolumeClick: (kind: string, name: string, namespace?: string, e?: React.MouseEvent) => void; }) => {
  const { metadata = {}, spec = {}, status = {} } = resource;
  const allContainers = [...(status.initContainerStatuses || []).map((c: any) => ({ ...c, isInit: true })), ...(status.containerStatuses || [])];
  const [expanded, setExpanded] = useState<Set<string>>(new Set(allContainers.filter((c: any) => getContainerState(c).state === 'Running').map((c: any) => c.name)));
  const [portForwards, setPortForwards] = useState<Record<string, any>>({});
  const [loadingPorts, setLoadingPorts] = useState<Set<string>>(new Set());

  if (!status.containerStatuses && !status.initContainerStatuses) return null;
  if (allContainers.length === 0) return null;

  const toggleExpand = (name: string) => setExpanded(prev => { const next = new Set(prev); next.has(name) ? next.delete(name) : next.add(name); return next; });

  const handlePortForward = async (containerName: string, port: number) => {
    const portKey = `${containerName}-${port}`;
    setLoadingPorts(prev => new Set(prev).add(portKey));
    try {
      if (portForwards[portKey]) {
        await api.stopPortForward(portForwards[portKey].id);
        setPortForwards(prev => { const next = { ...prev }; delete next[portKey]; return next; });
      } else {
        const result = await api.createPortForward(cluster, metadata.namespace, metadata.name, port);
        setPortForwards(prev => ({ ...prev, [portKey]: result }));
        window.open(`http://localhost:${result.localPort}`, '_blank');
      }
    } catch (e) { console.error('Port forward failed:', e); }
    finally { setLoadingPorts(prev => { const next = new Set(prev); next.delete(portKey); return next; }); }
  };

  const getStatusClass = (container: any) => {
    const { state } = getContainerState(container);
    if (state === 'Completed' || state === 'Terminated') return 'completed';
    if (['CrashLoopBackOff', 'Error', 'ImagePullBackOff', 'ErrImagePull'].includes(container.state?.waiting?.reason)) return 'error';
    if (container.ready) return 'ready';
    return 'not-ready';
  };

  return (
    <PropertyGroup title="Containers" count={allContainers.length} icon={<CubeIcon />} defaultOpen>
      <div className="containers-list">
        {allContainers.map((container: any) => {
          const containerSpec = (container.isInit ? spec.initContainers : spec.containers)?.find((c: any) => c.name === container.name) || {};
          const { state } = getContainerState(container);
          const isExpanded = expanded.has(container.name);
          const statusClass = getStatusClass(container);

          return (
            <div key={container.name} className={`container-item ${statusClass} ${isExpanded ? 'expanded' : ''}`}>
              <button className="container-header" onClick={() => toggleExpand(container.name)}>
                <div className="container-header-left">
                  <span className="container-expand-icon">{isExpanded ? <ChevronDownIcon /> : <ChevronRightIcon />}</span>
                  <span className={`container-status-dot ${statusClass}`} />
                  <span className="container-name">
                    {container.name}
                    {container.isInit && <span className="container-init-badge">INIT</span>}
                    <span className={`container-state-badge ${statusClass}`}>{state}</span>
                    {container.restartCount > 0 && <span className="container-restarts">{container.restartCount} restart{container.restartCount > 1 ? 's' : ''}</span>}
                  </span>
                </div>
              </button>
              <div className="container-details-wrapper">
                <div className="container-details">
                  <ContainerDropdown container={container} containerSpec={containerSpec} podName={metadata.name} namespace={metadata.namespace} cluster={cluster} portForwards={portForwards} loadingPorts={loadingPorts} onPortForward={handlePortForward} volumes={spec.volumes} onVolumeClick={onVolumeClick} />
                </div>
              </div>
            </div>
          );
        })}
      </div>
    </PropertyGroup>
  );
};

const SCALABLE_KINDS = ['Deployment', 'StatefulSet', 'ReplicaSet'];
const RESTARTABLE_KINDS = ['Deployment', 'StatefulSet', 'DaemonSet'];
const TRIGGERABLE_KINDS = ['CronJob'];
const DELETABLE_KINDS = ['Deployment', 'StatefulSet', 'ReplicaSet', 'DaemonSet', 'Pod', 'Service', 'ConfigMap', 'Secret', 'Ingress', 'Job', 'CronJob'];

const QuickActions: React.FC<{ resource: any; cluster: string; hideDelete?: boolean }> = ({ resource, cluster }) => {
  const [isScaling, setIsScaling] = useState(false);
  const [isRestarting, setIsRestarting] = useState(false);
  const [isTriggering, setIsTriggering] = useState(false);
  const [showScaleDialog, setShowScaleDialog] = useState(false);
  const [showRestartDialog, setShowRestartDialog] = useState(false);
  const [showTriggerDialog, setShowTriggerDialog] = useState(false);
  const { loadDetails } = useStore();

  const kind = resource.kind || '';
  const metadata = resource.metadata || {};
  const apiVersion = resource.apiVersion || '';
  const group = apiVersion.includes('/') ? apiVersion.split('/')[0] : '';
  const version = apiVersion.includes('/') ? apiVersion.split('/')[1] : apiVersion;

  const canScale = SCALABLE_KINDS.includes(kind);
  const canRestart = RESTARTABLE_KINDS.includes(kind);
  const canTrigger = TRIGGERABLE_KINDS.includes(kind);

  // Build resource definition for loadDetails
  const resourceDef = { group, version, kind, namespaced: !!metadata.namespace };
  const item = { name: metadata.name, namespace: metadata.namespace };

  const handleScaleConfirm = async (replicas: number) => {
    setIsScaling(true);
    try {
      await api.scaleResource(cluster, group, version, kind, metadata.namespace || '', metadata.name, replicas);
      setShowScaleDialog(false);
      // Refresh the detail view data
      await loadDetails(cluster, resourceDef, item);
    } catch (e: any) {
      console.error(`Failed to scale: ${e.message}`);
    } finally {
      setIsScaling(false);
    }
  };

  const handleRestartConfirm = async () => {
    setIsRestarting(true);
    try {
      await api.restartResource(cluster, group, version, kind, metadata.namespace || '', metadata.name);
      setShowRestartDialog(false);
      // Refresh the detail view data
      await loadDetails(cluster, resourceDef, item);
    } catch (e: any) {
      console.error(`Failed to restart: ${e.message}`);
    } finally {
      setIsRestarting(false);
    }
  };

  const handleTriggerConfirm = async () => {
    setIsTriggering(true);
    try {
      await api.triggerCronJob(cluster, metadata.namespace || '', metadata.name);
      setShowTriggerDialog(false);
    } catch (e: any) {
      console.error(`Failed to trigger: ${e.message}`);
    } finally {
      setIsTriggering(false);
    }
  };

  if (!canScale && !canRestart && !canTrigger) return null;

  return (
    <>
      <div className="quick-actions">
        {canScale && <button className="quick-action-btn" onClick={() => setShowScaleDialog(true)} disabled={isScaling} title="Scale"><SizeIcon /></button>}
        {canRestart && <button className="quick-action-btn" onClick={() => setShowRestartDialog(true)} disabled={isRestarting} title="Restart"><ReloadIcon /></button>}
        {canTrigger && <button className="quick-action-btn" onClick={() => setShowTriggerDialog(true)} disabled={isTriggering} title="Trigger"><PlayIcon /></button>}
      </div>
      <ScaleDialog
        isOpen={showScaleDialog}
        item={{ name: metadata.name }}
        currentReplicas={resource.spec?.replicas ?? 0}
        isScaling={isScaling}
        onConfirm={handleScaleConfirm}
        onCancel={() => setShowScaleDialog(false)}
      />
      <Dialog
        isOpen={showRestartDialog}
        title={`Restart ${kind}`}
        confirmText="Restart"
        isLoading={isRestarting}
        loadingText="Restarting..."
        onConfirm={handleRestartConfirm}
        onClose={() => setShowRestartDialog(false)}
      >
        <p>Are you sure you want to restart {kind} "{metadata.name}"?</p>
      </Dialog>
      <Dialog
        isOpen={showTriggerDialog}
        title="Trigger CronJob"
        confirmText="Trigger"
        isLoading={isTriggering}
        loadingText="Triggering..."
        onConfirm={handleTriggerConfirm}
        onClose={() => setShowTriggerDialog(false)}
      >
        <p>This will create a new Job from CronJob "{metadata.name}". Continue?</p>
      </Dialog>
    </>
  );
};

const DeleteAction: React.FC<{ resource: any; cluster: string }> = ({ resource, cluster }) => {
  const [isDeleting, setIsDeleting] = useState(false);
  const [showDeleteDialog, setShowDeleteDialog] = useState(false);

  const kind = resource.kind || '';
  const metadata = resource.metadata || {};
  const apiVersion = resource.apiVersion || '';
  const group = apiVersion.includes('/') ? apiVersion.split('/')[0] : '';
  const version = apiVersion.includes('/') ? apiVersion.split('/')[1] : apiVersion;

  const standardGroups = ['', 'apps', 'batch', 'networking.k8s.io', 'rbac.authorization.k8s.io', 'policy', 'storage.k8s.io', 'autoscaling', 'coordination.k8s.io', 'discovery.k8s.io', 'events.k8s.io', 'flowcontrol.apiserver.k8s.io', 'node.k8s.io', 'scheduling.k8s.io', 'admissionregistration.k8s.io', 'apiextensions.k8s.io', 'certificates.k8s.io'];
  const isCustomResource = group && !standardGroups.includes(group) && !group.includes('k8s.io') && !['CustomResourceDefinition', 'APIResourceDefinition'].includes(kind);
  const canDelete = DELETABLE_KINDS.includes(kind) || isCustomResource;

  const handleDeleteConfirm = async () => {
    setIsDeleting(true);
    try {
      await api.deleteResources(cluster, group, version, kind, [{ name: metadata.name, namespace: metadata.namespace }]);
      setShowDeleteDialog(false);
    } catch (e: any) {
      console.error(`Failed to delete: ${e.message}`);
    } finally {
      setIsDeleting(false);
    }
  };

  if (!canDelete) return null;

  return (
    <>
      <button className="quick-action-btn quick-action-danger" onClick={() => setShowDeleteDialog(true)} disabled={isDeleting} title="Delete"><TrashIcon /></button>
      <Dialog
        isOpen={showDeleteDialog}
        title={`Delete ${kind}`}
        confirmText="Delete"
        isLoading={isDeleting}
        loadingText="Deleting..."
        variant="danger"
        onConfirm={handleDeleteConfirm}
        onClose={() => setShowDeleteDialog(false)}
      >
        <p>Are you sure you want to delete {kind} "{metadata.name}"? This cannot be undone.</p>
      </Dialog>
    </>
  );
};

const ResourceDetailView = ({ resource, cluster, actions, mode = 'detail' }: { resource: any; cluster: string; actions: React.ReactNode; mode?: 'detail' | 'center'; }) => {
  const { navigateToLink } = useResourceNavigation(cluster);
  const [clickTimer, setClickTimer] = React.useState<NodeJS.Timeout | null>(null);

  const handleResourceClick = React.useCallback((kind: string, name: string, namespace?: string, e?: React.MouseEvent, apiVersion?: string) => {
    if (e) { e.preventDefault(); e.stopPropagation(); }
    const openInDetailTab = mode === 'detail';
    if (openInDetailTab) {
      if (clickTimer) {
        clearTimeout(clickTimer);
        setClickTimer(null);
        navigateToLink(kind, name, namespace, { openInDetailTab: true, isPinned: true, apiVersion });
      } else {
        const timer = setTimeout(() => { setClickTimer(null); navigateToLink(kind, name, namespace, { openInDetailTab: true, isPinned: false, apiVersion }); }, 200);
        setClickTimer(timer);
      }
    } else {
      navigateToLink(kind, name, namespace, { apiVersion });
    }
  }, [clickTimer, navigateToLink, mode]);

  React.useEffect(() => { return () => { if (clickTimer) clearTimeout(clickTimer); }; }, [clickTimer]);

  const { metadata = {}, status = {}, spec = {} } = resource;

  const isCustomResource = () => {
    if (spec.parameters) return true;
    const standardGroups = ['', 'apps', 'batch', 'networking.k8s.io', 'rbac.authorization.k8s.io', 'policy', 'storage.k8s.io', 'autoscaling', 'coordination.k8s.io', 'discovery.k8s.io', 'events.k8s.io', 'flowcontrol.apiserver.k8s.io', 'node.k8s.io', 'scheduling.k8s.io', 'admissionregistration.k8s.io', 'apiextensions.k8s.io', 'certificates.k8s.io'];
    const apiVersion = resource.apiVersion;
    if (apiVersion) {
      const group = apiVersion.includes('/') ? apiVersion.split('/')[0] : '';
      if (standardGroups.includes(group) || group.includes('k8s.io')) return false;
      if (['CustomResourceDefinition', 'APIResourceDefinition'].includes(resource.kind)) return false;
      return true;
    }
    return false;
  };

  const getStatusPhase = () => {
    if (status.phase) return status.phase;
    if (status.state) return status.state;
    if (status.conditions) {
      const ready = status.conditions.find((c: any) => c.type === 'Ready');
      if (ready) return ready.status === 'True' ? 'Ready' : 'NotReady';
    }
    return null;
  };

  const phase = getStatusPhase();

  const renderReplicas = () => {
    if (spec.replicas === undefined) return null;
    const ready = status.readyReplicas || 0;
    const total = spec.replicas;
    let statusClass = 'status-neutral';
    if (total === 0) statusClass = 'status-warning';
    else if (ready === total) statusClass = 'status-running';
    else if (ready === 0) statusClass = 'status-failed';
    else statusClass = 'status-warning';
    return <span className={`status-badge ${statusClass}`}>{`${ready}/${total}`}</span>;
  };

  const parseMemoryValue = (value: string): number => {
    if (!value) return 0;
    const num = parseFloat(value);
    if (value.endsWith('Ki')) return num * 1024;
    if (value.endsWith('Mi')) return num * 1024 * 1024;
    if (value.endsWith('Gi')) return num * 1024 * 1024 * 1024;
    return num;
  };

  const SPECIAL_KINDS = ['Service', 'ConfigMap', 'Secret', 'Endpoints', 'ServiceAccount', 'Role', 'ClusterRole', 'RoleBinding', 'ClusterRoleBinding', 'Node', 'Event', 'events'];

  const apiGroup = (resource.apiVersion || '').includes('/') ? resource.apiVersion.split('/')[0] : '';
  const isArgoApplication = resource.kind === 'Application' && (apiGroup === 'argoproj.io' || apiGroup.endsWith('.argoproj.io'));

  return (
    <div className="resource-detail-view">
      <div className="resource-header">
        <div className="resource-header-top">
          <div className="resource-kind-row">
            <span className="resource-kind-badge">{resource.kind}</span>
            {(phase || renderReplicas()) && (
              <div className="resource-status-row">
                {phase && <span className={`status-badge status-${String(phase).toLowerCase()}`}>{phase}</span>}
                {renderReplicas()}
              </div>
            )}
            <div className="resource-actions-row">
              <QuickActions resource={resource} cluster={cluster} />
              {actions}
              <DeleteAction resource={resource} cluster={cluster} />
            </div>
          </div>
          <h2 className="resource-name">
            <span>{metadata.name}</span>
            <ClipboardCopy text={metadata.name} />
          </h2>
        </div>
      </div>

      <ScrollArea.Root className="resource-detail-scroll">
        <ScrollArea.Viewport className="resource-detail-viewport">
          <div className="info-sections">
            {resource.kind === 'CustomResourceDefinition' && <CRDDefinitionView crd={resource} />}
            {resource.kind === 'APIResourceDefinition' && <APIResourceDefinitionView resource={resource} />}
            {resource.kind === 'Node' && <NodeDetailView resource={resource} cluster={cluster} handleResourceClick={handleResourceClick} />}
            {(resource.kind === 'Event' || resource.kind === 'events') && <EventDetailView resource={resource} handleResourceClick={handleResourceClick} />}
            {resource.kind === 'Service' && <ServiceDetailView resource={resource} cluster={cluster} handleResourceClick={handleResourceClick} />}
            {resource.kind === 'ConfigMap' && <ConfigMapDetailView resource={resource} cluster={cluster} handleResourceClick={handleResourceClick} />}
            {resource.kind === 'Secret' && <SecretDetailView resource={resource} cluster={cluster} handleResourceClick={handleResourceClick} />}
            {resource.kind === 'Endpoints' && <EndpointsDetailView resource={resource} handleResourceClick={handleResourceClick} />}
            {(resource.kind === 'Role' || resource.kind === 'ClusterRole') && <RoleDetailView resource={resource} handleResourceClick={handleResourceClick} />}
            {(resource.kind === 'RoleBinding' || resource.kind === 'ClusterRoleBinding') && <RoleBindingDetailView resource={resource} handleResourceClick={handleResourceClick} />}
            {resource.kind === 'ServiceAccount' && <ServiceAccountDetailView resource={resource} cluster={cluster} handleResourceClick={handleResourceClick} />}
            {isArgoApplication && <ApplicationDetailView cluster={cluster} resource={resource} handleResourceClick={handleResourceClick} />}
            {isCustomResource() && !SPECIAL_KINDS.includes(resource.kind) && !isArgoApplication && <CustomResourceDetailView resource={resource} cluster={cluster} handleResourceClick={handleResourceClick} />}

            {!isCustomResource() && !SPECIAL_KINDS.includes(resource.kind) && !isArgoApplication && (
              <>
                <MetadataSection metadata={metadata} handleResourceClick={handleResourceClick} />
                <div className="section-divider" />

                {resource.kind === 'Pod' && (() => {
                  let totalLimits = { cpu: 0, memory: 0 };
                  let totalRequests = { cpu: 0, memory: 0 };
                  (resource.spec?.containers || []).forEach((c: any) => {
                    const r = c.resources || {};
                    if (r.limits?.cpu) totalLimits.cpu += r.limits.cpu.endsWith('m') ? parseInt(r.limits.cpu) : parseFloat(r.limits.cpu) * 1000;
                    if (r.requests?.cpu) totalRequests.cpu += r.requests.cpu.endsWith('m') ? parseInt(r.requests.cpu) : parseFloat(r.requests.cpu) * 1000;
                    if (r.limits?.memory) totalLimits.memory += parseMemoryValue(r.limits.memory);
                    if (r.requests?.memory) totalRequests.memory += parseMemoryValue(r.requests.memory);
                  });
                  return (
                    <>
                      <MetricsPropertyGroup cluster={cluster} icon={<BarChartIcon />}>
                        <div className="pod-metrics-wrapper">
                          <PodMetrics
                            cluster={cluster}
                            namespace={metadata.namespace || 'default'}
                            podName={metadata.name}
                            resourceLimits={totalLimits.cpu || totalLimits.memory ? totalLimits : undefined}
                            resourceRequests={totalRequests.cpu || totalRequests.memory ? totalRequests : undefined}
                          />
                        </div>
                      </MetricsPropertyGroup>
                      <div className="section-divider" />
                    </>
                  );
                })()}

                {resource.kind === 'Pod' && (
                  <>
                    <ContainersSection resource={resource} cluster={cluster} onVolumeClick={handleResourceClick} />
                    <div className="section-divider" />
                  </>
                )}

                {['Deployment', 'StatefulSet', 'DaemonSet', 'ReplicaSet'].includes(resource.kind) && (
                  <>
                    <MetricsPropertyGroup cluster={cluster} icon={<BarChartIcon />}>
                      <div className="pod-metrics-wrapper">
                        <WorkloadMetrics
                          cluster={cluster}
                          kind={resource.kind}
                          namespace={metadata.namespace || 'default'}
                          name={metadata.name}
                        />
                      </div>
                    </MetricsPropertyGroup>
                    <div className="section-divider" />
                  </>
                )}

                <SpecificationSection spec={spec} handleResourceClick={handleResourceClick} />
                <div className="section-divider" />

                {status.conditions && (
                  <>
                    <PropertyGroup title="Conditions" icon={<CheckCircledIcon />} count={status.conditions.length} defaultOpen>
                      <ConditionsView conditions={status.conditions} />
                    </PropertyGroup>
                    <div className="section-divider" />
                  </>
                )}

                <EventsSection events={resource.events} />
              </>
            )}
          </div>
        </ScrollArea.Viewport>
        <ScrollArea.Scrollbar className="scrollbar" orientation="vertical">
          <ScrollArea.Thumb className="scrollbar-thumb" />
        </ScrollArea.Scrollbar>
      </ScrollArea.Root>
    </div>
  );
};

export default ResourceDetailView;
