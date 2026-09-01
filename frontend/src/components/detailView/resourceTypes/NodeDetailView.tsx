import React from 'react';
import { DesktopIcon, GlobeIcon, GearIcon, InfoCircledIcon, BarChartIcon, CheckCircledIcon } from '@radix-ui/react-icons';
import PropertyRow from '../../common/PropertyRow';
import PropertyGroup from '../shared/PropertyGroup';
import MetricsPropertyGroup from '../shared/MetricsPropertyGroup';
import ConditionsView from '../shared/ConditionsView';
import { NodeMetrics } from '../../NodeMetrics';
import EventsSection from '../shared/EventsSection';
import MetadataSection from '../shared/MetadataSection';
import { parseCPUToMillicores, parseMemoryToBytes } from '../../../utils/detailViewFormatters';
import { DetailViewPropsWithCluster } from '../../../types/detailView';
import './NodeDetailView.css';

const NodeDetailView: React.FC<DetailViewPropsWithCluster> = ({ resource, cluster, handleResourceClick }) => {
  const { metadata = {}, spec = {}, status = {} } = resource;

  const getNodeRole = (): string => {
    if (!metadata.labels) return 'worker';
    const roles = Object.keys(metadata.labels)
      .filter(key => key.startsWith('node-role.kubernetes.io/'))
      .map(key => key.replace('node-role.kubernetes.io/', ''));
    return roles.length > 0 ? roles.join(', ') : 'worker';
  };

  const resourceCapacity = status.capacity ? { cpu: parseCPUToMillicores(status.capacity.cpu), memory: parseMemoryToBytes(status.capacity.memory) } : undefined;
  const resourceAllocatable = status.allocatable ? { cpu: parseCPUToMillicores(status.allocatable.cpu), memory: parseMemoryToBytes(status.allocatable.memory) } : undefined;

  return (
    <>
      <MetricsPropertyGroup cluster={cluster} icon={<BarChartIcon />}>
        <div className="node-metrics-wrapper">
          <NodeMetrics cluster={cluster} nodeName={metadata.name} resourceCapacity={resourceCapacity} resourceAllocatable={resourceAllocatable} />
        </div>
      </MetricsPropertyGroup>
      <div className="section-divider" />

      <PropertyGroup title="Node Info" icon={<DesktopIcon />} defaultOpen>
        <PropertyRow label="Role" value={<span className="node-role-badge">{getNodeRole()}</span>} />
        <PropertyRow label="Version" value={status.nodeInfo?.kubeletVersion} copyText={status.nodeInfo?.kubeletVersion} />
        {spec.providerID && <PropertyRow label="Provider" value={spec.providerID.split('/').pop()} copyText={spec.providerID} muted />}
        {spec.podCIDR && <PropertyRow label="Pod CIDR" value={spec.podCIDR} copyText={spec.podCIDR} mono />}
      </PropertyGroup>
      <div className="section-divider" />

      <MetadataSection metadata={metadata} handleResourceClick={handleResourceClick} showNamespace={false} />
      <div className="section-divider" />

      {status.addresses?.length > 0 && (
        <>
          <PropertyGroup title="Addresses" icon={<GlobeIcon />} count={status.addresses.length} defaultOpen={false}>
            <div className="node-addresses">
              {status.addresses.map((addr: any, i: number) => (
                <div key={i} className="address-item">
                  <span className="address-type">{addr.type}</span>
                  <span className="address-value">{addr.address}</span>
                </div>
              ))}
            </div>
          </PropertyGroup>
          <div className="section-divider" />
        </>
      )}

      {(status.capacity || status.allocatable) && (
        <>
          <PropertyGroup title="Resources" icon={<GearIcon />} defaultOpen={false}>
            <div className="node-resources">
              <div className="resources-header">
                <span className="resources-label"></span>
                <span className="resources-col">Capacity</span>
                <span className="resources-col">Allocatable</span>
              </div>
              {Object.keys(status.capacity || {}).map(key => (
                <div key={key} className="resources-row">
                  <span className="resources-label">{key}</span>
                  <span className="resources-col">{status.capacity?.[key]}</span>
                  <span className="resources-col">{status.allocatable?.[key]}</span>
                </div>
              ))}
            </div>
          </PropertyGroup>
          <div className="section-divider" />
        </>
      )}

      {spec.taints?.length > 0 && (
        <>
          <PropertyGroup title="Taints" count={spec.taints.length} defaultOpen={false}>
            <div className="node-taints">
              {spec.taints.map((taint: any, i: number) => (
                <div key={i} className="taint-item">
                  <span className="taint-key">{taint.key}</span>
                  {taint.value && <span className="taint-value">{taint.value}</span>}
                  <span className={`taint-effect effect-${taint.effect.toLowerCase()}`}>{taint.effect}</span>
                </div>
              ))}
            </div>
          </PropertyGroup>
          <div className="section-divider" />
        </>
      )}

      {status.nodeInfo && (
        <>
          <PropertyGroup title="System" icon={<InfoCircledIcon />} defaultOpen={false}>
            <PropertyRow label="OS" value={status.nodeInfo.osImage || status.nodeInfo.operatingSystem} />
            <PropertyRow label="Kernel" value={status.nodeInfo.kernelVersion} muted />
            <PropertyRow label="Runtime" value={status.nodeInfo.containerRuntimeVersion} />
            <PropertyRow label="Architecture" value={status.nodeInfo.architecture} />
            <PropertyRow label="Machine ID" value={status.nodeInfo.machineID?.slice(0, 12) + '...'} copyText={status.nodeInfo.machineID} muted mono />
          </PropertyGroup>
          <div className="section-divider" />
        </>
      )}

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
  );
};

export default NodeDetailView;
