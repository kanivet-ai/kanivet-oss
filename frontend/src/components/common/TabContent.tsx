import { useEffect, useMemo, useRef, useState } from 'react';
import { DrawingPinIcon, Pencil2Icon, ReaderIcon } from '@radix-ui/react-icons';
import ResourceDetailView from '../detailView/ResourceDetailView';
import HelmReleaseDetailView from '../detailView/resourceTypes/HelmReleaseDetailView';
import ResourceDetailSkeleton from '../ResourceDetailSkeleton';
import ContainerSelector from '../ContainerSelector';
import CrossplaneIcon from '../icons/CrossplaneIcon';
import { Tooltip } from './Tooltip';
import { useStore } from '../../store';
import api from '../../services/api';
import { workloadControllerKinds } from '../../utils/resourceActions';
import './TabContent.css';

interface TabContentProps {
  tab: any;
  mode?: 'detail' | 'center';
  onPinClick?: () => void;
  isDeleted?: boolean;
}

const TabContent = ({ tab, mode = 'detail', onPinClick, isDeleted }: TabContentProps) => {
  const { openBottomTab } = useStore();
  const [showContainerSelector, setShowContainerSelector] = useState(false);
  const [isCrossplaneResource, setIsCrossplaneResource] = useState(false);
  const shellButtonRef = useRef<HTMLButtonElement>(null);

  const item = tab?.item || {};
  const cluster = tab?.cluster || '';

  const apiVersion: string = item.apiVersion || '';
  const group = useMemo(() => (apiVersion.includes('/') ? apiVersion.split('/')[0] : ''), [apiVersion]);
  const version = useMemo(() => (apiVersion.includes('/') ? apiVersion.split('/')[1] : apiVersion), [apiVersion]);
  const kind = item.kind || '';

  const isPod = item.kind === 'Pod';
  const isNode = item.kind === 'Node';
  const isDefinitionKind = item.kind === 'CustomResourceDefinition' || item.kind === 'APIResourceDefinition';

  const isWorkloadController = workloadControllerKinds.includes((item.kind || '').toLowerCase());
  const canShowShell = isPod || isNode;
  const canShowLogs = isPod || isWorkloadController;
  const canShowTrace = isCrossplaneResource;

  useEffect(() => {
    const handler = (e: MouseEvent) => {
      const target = e.target as HTMLElement;
      const withinButton = shellButtonRef.current?.contains(target);
      const withinDropdown = target?.closest?.('.container-selector-dropdown');
      if (!withinButton && !withinDropdown) setShowContainerSelector(false);
    };
    if (showContainerSelector) document.addEventListener('mousedown', handler);
    return () => document.removeEventListener('mousedown', handler);
  }, [showContainerSelector]);

  // Comprehensive crossplane resource detection
  useEffect(() => {
    if (item && cluster && kind) {
      // Quick check for core Kubernetes resources that are definitely not crossplane
      const isCoreResource =
        !group &&
        [
          'Pod',
          'Service',
          'ConfigMap',
          'Secret',
          'PersistentVolume',
          'PersistentVolumeClaim',
          'Node',
          'Namespace',
          'ServiceAccount',
          'Event',
        ].includes(kind);

      if (isCoreResource) {
        setIsCrossplaneResource(false);
        return;
      }

      // Quick check for obvious crossplane resources
      const isLikelyCrossplane =
        group.includes('crossplane.io') ||
        (group.includes('.io') &&
          !group.includes('k8s.io') &&
          !group.includes('kubernetes.io')) ||
        (item.metadata?.labels &&
          Object.keys(item.metadata.labels).some((k) =>
            k.includes('crossplane.io'),
          )) ||
        (item.metadata?.annotations &&
          Object.keys(item.metadata.annotations).some((k) =>
            k.includes('crossplane.io'),
          ));

      if (isLikelyCrossplane) {
        setIsCrossplaneResource(true);
      } else if (group || version) {
        // Fall back to backend check for thorough validation
        api
          .checkCrossplaneResource(cluster, group, version, kind)
          .then((result) => {
            setIsCrossplaneResource(result);
          })
          .catch((err) => {
            console.error('Error checking crossplane resource:', err);
            setIsCrossplaneResource(false);
          });
      }
    }
  }, [item, cluster, group, version, kind]);

  const handleActionClick = (type: 'shell' | 'trace' | 'edit' | 'logs') => {
    if (!item || !cluster) return;

    if (type === 'logs' && isWorkloadController) {
      openBottomTab('deployment-logs', item, cluster);
      return;
    }

    if (type === 'shell' && item.kind === 'Pod') {
      const containers = item.spec?.containers || item.containers || [];
      const initContainers = item.spec?.initContainers || item.initContainers || [];
      if (containers.length + initContainers.length > 1) {
        setShowContainerSelector((prev) => !prev);
        return;
      }
    }

    openBottomTab(type, item, cluster);
  };

  const handleContainerSelect = (containerName: string) => {
    if (!item || !cluster) return;
    const itemWithContainer = { ...item, selectedContainer: containerName };
    openBottomTab('shell', itemWithContainer, cluster);
    setShowContainerSelector(false);
  };

  // Resources that don't have a spec field but are still valid
  const resourcesWithoutSpec = ['Secret', 'ConfigMap', 'Endpoints', 'ServiceAccount', 'Role', 'ClusterRole', 'RoleBinding', 'ClusterRoleBinding', 'Event', 'events'];
  
  const isFullyLoaded =
    item &&
    typeof item === 'object' &&
    item.metadata &&
    (isDefinitionKind ||
     (isPod
       ? Array.isArray(item.spec?.containers) && item.spec.containers.length > 0
       : (item.spec !== undefined || resourcesWithoutSpec.includes(item.kind))));

  const hasBasicInfo = item && typeof item === 'object' && (item.metadata?.name || item.name);
  const isHelmRelease = item?.kind === 'HelmRelease' && item?.chart;

  if (isHelmRelease) {
    const releaseKey = `${cluster}-${item.namespace}-${item.name}`;
    return (
      <div className={`tab-content-inner tab-content-${mode}`}>
        {isDeleted && (
          <div className="tab-content-deleted-banner">
            <span className="deleted-icon">⚠</span>
            <span>This resource no longer exists</span>
          </div>
        )}
        <div className="view-content-root">
          <div className="view-tab-content">
            <div className="tab-content-body">
              <div className="tab-content-pretty">
                <HelmReleaseDetailView key={releaseKey} cluster={cluster} release={item} mode={mode} />
              </div>
            </div>
          </div>
        </div>
      </div>
    );
  }

  const actionButtons = (
    <div className="tab-content-actions">
      {canShowLogs && (
        <Tooltip content="View logs">
          <button
            className="tab-content-action-btn priority-low"
            onClick={() => handleActionClick('logs')}
            disabled={!isFullyLoaded}
          >
            <ReaderIcon />
          </button>
        </Tooltip>
      )}
      <Tooltip content="Edit YAML">
        <button
          className="tab-content-action-btn priority-low"
          onClick={() => handleActionClick('edit')}
          disabled={!isFullyLoaded}
        >
          <Pencil2Icon />
        </button>
      </Tooltip>
      {canShowShell && (
        <Tooltip content="Open shell">
          <button
            ref={shellButtonRef}
            className="tab-content-action-btn priority-low"
            onClick={() => handleActionClick('shell')}
            disabled={!isFullyLoaded}
          >
            <span style={{ fontSize: '16px', fontWeight: 'bold' }}>$</span>
          </button>
        </Tooltip>
      )}
      {canShowTrace && (
        <Tooltip content="Trace Crossplane resource">
          <button className="tab-content-action-btn priority-low" onClick={() => handleActionClick('trace')} disabled={!isFullyLoaded}>
            <CrossplaneIcon width={14} height={14} />
          </button>
        </Tooltip>
      )}
      {onPinClick && (
        <Tooltip content="Pin this resource">
          <button className="pin-detail-btn priority-high" onClick={onPinClick} disabled={!isFullyLoaded}>
            <DrawingPinIcon />
          </button>
        </Tooltip>
      )}
    </div>
  );

  return (
    <div className={`tab-content-inner tab-content-${mode}`}>
      {isDeleted && (
        <div className="tab-content-deleted-banner">
          <span className="deleted-icon">⚠</span>
          <span>This resource no longer exists</span>
        </div>
      )}
      <div className="view-content-root">
        <div className="view-tab-content">
          <div className="tab-content-body">
            <div className="tab-content-pretty">
              {!hasBasicInfo ? (
                <ResourceDetailSkeleton resource={item} cluster={cluster} mode={mode} actions={actionButtons} />
              ) : !isFullyLoaded ? (
                <ResourceDetailSkeleton resource={item} cluster={cluster} mode={mode} actions={actionButtons} />
              ) : (
                <ResourceDetailView resource={item} cluster={cluster} mode={mode} actions={actionButtons} />
              )}
            </div>
          </div>
        </div>
      </div>
      {showContainerSelector && item?.kind === 'Pod' && (
        <ContainerSelector
          containers={(item.spec?.containers || item.containers || []).map((c: any) => {
            const status = (item.status?.containerStatuses || item.containerStatuses || []).find((cs: any) => cs.name === c.name);
            return { ...c, isRunning: !!status?.state?.running, status };
          })}
          initContainers={(item.spec?.initContainers || item.initContainers || []).map((c: any) => {
            const status = (item.status?.initContainerStatuses || item.initContainerStatuses || []).find((cs: any) => cs.name === c.name);
            return { ...c, isRunning: !!status?.state?.running, status };
          })}
          onSelectContainer={handleContainerSelect}
          showButton={false}
          buttonElement={shellButtonRef.current}
        />
      )}
    </div>
  );
};

export default TabContent;

