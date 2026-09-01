import React from 'react';
import { TargetIcon, CheckCircledIcon, CrossCircledIcon } from '@radix-ui/react-icons';
import PropertyRow from '../../common/PropertyRow';
import PropertyGroup from '../shared/PropertyGroup';
import MetadataSection from '../shared/MetadataSection';
import EventsSection from '../shared/EventsSection';
import { DetailViewProps } from '../../../types/detailView';
import './EndpointsDetailView.css';

const EndpointsDetailView: React.FC<DetailViewProps> = ({ resource, handleResourceClick }) => {
  const { metadata = {}, subsets = [] } = resource;

  const totalReady = subsets.reduce((sum: number, s: any) => sum + (s.addresses?.length || 0), 0);
  const totalNotReady = subsets.reduce((sum: number, s: any) => sum + (s.notReadyAddresses?.length || 0), 0);

  return (
    <>
      <PropertyGroup title="Summary" icon={<TargetIcon />} defaultOpen>
        <PropertyRow
          label="Service"
          value={
            handleResourceClick ? (
              <button className="link-button" onClick={(e) => handleResourceClick('Service', metadata.name, metadata.namespace, e)}>
                {metadata.name}
              </button>
            ) : metadata.name
          }
          copyText={metadata.name}
        />
        <PropertyRow label="Ready" value={<span className="endpoint-count ready">{totalReady}</span>} />
        {totalNotReady > 0 && <PropertyRow label="Not Ready" value={<span className="endpoint-count not-ready">{totalNotReady}</span>} />}
        <PropertyRow label="Subsets" value={subsets.length} />
      </PropertyGroup>
      <div className="section-divider" />

      <MetadataSection metadata={metadata} handleResourceClick={handleResourceClick} />
      <div className="section-divider" />

      {subsets.map((subset: any, i: number) => {
        const addresses = subset.addresses || [];
        const notReadyAddresses = subset.notReadyAddresses || [];
        const ports = subset.ports || [];

        return (
          <PropertyGroup key={i} title={`Subset ${i + 1}`} count={addresses.length + notReadyAddresses.length} defaultOpen={i === 0}>
            {ports.length > 0 && (
              <div className="endpoints-ports">
                {ports.map((port: any, j: number) => (
                  <div key={j} className="port-badge">
                    {port.name && <span className="port-name">{port.name}</span>}
                    <span className="port-number">{port.port}</span>
                    <span className="port-protocol">{port.protocol}</span>
                  </div>
                ))}
              </div>
            )}
            {addresses.length > 0 && (
              <div className="endpoints-addresses">
                <div className="addresses-header">
                  <CheckCircledIcon className="ready-icon" />
                  <span>Ready ({addresses.length})</span>
                </div>
                <div className="addresses-list">
                  {addresses.map((addr: any, j: number) => (
                    <div key={j} className="address-item">
                      <span className="address-ip">{addr.ip}</span>
                      {addr.targetRef && (
                        <button className="link-button" onClick={(e) => handleResourceClick?.(addr.targetRef.kind, addr.targetRef.name, addr.targetRef.namespace, e)}>
                          {addr.targetRef.name}
                        </button>
                      )}
                    </div>
                  ))}
                </div>
              </div>
            )}
            {notReadyAddresses.length > 0 && (
              <div className="endpoints-addresses not-ready">
                <div className="addresses-header">
                  <CrossCircledIcon className="not-ready-icon" />
                  <span>Not Ready ({notReadyAddresses.length})</span>
                </div>
                <div className="addresses-list">
                  {notReadyAddresses.map((addr: any, j: number) => (
                    <div key={j} className="address-item">
                      <span className="address-ip">{addr.ip}</span>
                      {addr.targetRef && <span className="address-target">{addr.targetRef.name}</span>}
                    </div>
                  ))}
                </div>
              </div>
            )}
          </PropertyGroup>
        );
      })}
      {subsets.length > 0 && <div className="section-divider" />}

      <EventsSection events={resource.events} />
    </>
  );
};

export default EndpointsDetailView;
