import React, { useState } from 'react';
import { GlobeIcon, Share2Icon, CheckCircledIcon } from '@radix-ui/react-icons';
import PropertyRow from '../../common/PropertyRow';
import PropertyGroup from '../shared/PropertyGroup';
import ConditionsView from '../shared/ConditionsView';
import MetadataSection from '../shared/MetadataSection';
import EventsSection from '../shared/EventsSection';
import ServicePorts from '../../ServicePorts';
import ClipboardCopy from '../../common/ClipboardCopy';
import { Tooltip } from '../../common/Tooltip';
import api from '../../../services/api';
import { DetailViewPropsWithCluster } from '../../../types/detailView';
import './ServiceDetailView.css';

const ServiceDetailView: React.FC<DetailViewPropsWithCluster> = ({ resource, cluster, handleResourceClick }) => {
  const { metadata = {}, spec = {}, status = {} } = resource;
  const [portForwards, setPortForwards] = useState<Record<string, any>>({});
  const [loadingPorts, setLoadingPorts] = useState<Set<string>>(new Set());

  const handlePortForward = async (port: number) => {
    const portKey = `${port}`;
    setLoadingPorts(prev => new Set(prev).add(portKey));
    try {
      if (portForwards[portKey]) {
        await api.stopPortForward(portForwards[portKey].id);
        setPortForwards(prev => { const next = { ...prev }; delete next[portKey]; return next; });
      } else {
        const result = await api.createServicePortForward(cluster, metadata.namespace, metadata.name, port);
        setPortForwards(prev => ({ ...prev, [portKey]: result }));
        window.open(`http://localhost:${result.localPort}`, '_blank');
      }
    } catch (e) { console.error('Port forward failed:', e); }
    finally { setLoadingPorts(prev => { const next = new Set(prev); next.delete(portKey); return next; }); }
  };

  const selectorString = spec.selector ? Object.entries(spec.selector).map(([k, v]) => `${k}=${v}`).join(',') : '';

  return (
    <>
      <PropertyGroup title="Configuration" icon={<GlobeIcon />} defaultOpen>
        <PropertyRow label="Type" value={<span className="service-type-badge">{spec.type}</span>} copyText={spec.type} />
        {spec.clusterIP && spec.clusterIP !== 'None' && (
          <PropertyRow label="Cluster IP" value={spec.clusterIP} copyText={spec.clusterIP} mono />
        )}
        {spec.clusterIP === 'None' && <PropertyRow label="Cluster IP" value="Headless" />}
        {spec.externalIPs?.length > 0 && (
          <PropertyRow label="External IPs" value={spec.externalIPs.join(', ')} copyText={spec.externalIPs.join(', ')} mono />
        )}
        {spec.loadBalancerIP && <PropertyRow label="LB IP" value={spec.loadBalancerIP} copyText={spec.loadBalancerIP} mono />}
        {spec.externalName && <PropertyRow label="External Name" value={spec.externalName} copyText={spec.externalName} />}
        {spec.sessionAffinity && spec.sessionAffinity !== 'None' && (
          <PropertyRow label="Session Affinity" value={spec.sessionAffinity} />
        )}
        {spec.ipFamilyPolicy && <PropertyRow label="IP Policy" value={spec.ipFamilyPolicy} muted />}
      </PropertyGroup>
      <div className="section-divider" />

      {spec.ports?.length > 0 && (
        <>
          <PropertyGroup title="Ports" count={spec.ports.length} icon={<Share2Icon />} defaultOpen>
            <div className="service-ports-wrapper">
              <ServicePorts
                ports={spec.ports}
                serviceName={metadata.name}
                namespace={metadata.namespace}
                cluster={cluster}
                portForwards={portForwards}
                loadingPorts={loadingPorts}
                onPortForward={handlePortForward}
              />
            </div>
          </PropertyGroup>
          <div className="section-divider" />
        </>
      )}

      {spec.selector && Object.keys(spec.selector).length > 0 && (
        <>
          <PropertyGroup title="Selector" defaultOpen={false}>
            <div className="service-selector">
              {Object.entries(spec.selector).map(([key, value]) => (
                <div key={key} className="selector-tag">
                  <Tooltip content={`${key}=${value}`}>
                    <span className="selector-key">{key}</span>
                  </Tooltip>
                  <span className="selector-value">{value as string}</span>
                </div>
              ))}
              <ClipboardCopy text={selectorString} alwaysShow />
            </div>
          </PropertyGroup>
          <div className="section-divider" />
        </>
      )}

      <MetadataSection metadata={metadata} handleResourceClick={handleResourceClick} />
      <div className="section-divider" />

      {spec.type === 'LoadBalancer' && status.loadBalancer && (
        <>
          <PropertyGroup title="Load Balancer" defaultOpen>
            {status.loadBalancer.ingress?.length > 0 ? (
              status.loadBalancer.ingress.map((ing: any, i: number) => (
                <PropertyRow key={i} label="Ingress" value={ing.ip || ing.hostname} copyText={ing.ip || ing.hostname} mono />
              ))
            ) : (
              <PropertyRow label="Status" value={<span className="status-badge status-pending">Pending</span>} />
            )}
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

export default ServiceDetailView;
