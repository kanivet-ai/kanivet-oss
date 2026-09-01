import React from 'react';
import './ServicePorts.css';

interface ServicePortsProps {
  ports: any[];
  serviceName: string;
  namespace: string;
  cluster: string;
  portForwards: Record<string, any>;
  loadingPorts: Set<string>;
  onPortForward: (port: number) => Promise<void>;
}

const ServicePorts: React.FC<ServicePortsProps> = ({ ports, portForwards, loadingPorts, onPortForward }) => {
  return (
    <div className="service-ports">
      {ports.map((port: any, idx: number) => {
        const portKey = `${port.port}`;
        const isForwarded = !!portForwards[portKey];
        const isLoading = loadingPorts.has(portKey);

        return (
          <div key={idx} className="service-port-row">
            <span className="port-name">{port.name || 'port'}</span>
            <span className="port-spec">
              <span className="port-number">{port.port}</span>
              {port.targetPort && port.targetPort !== port.port && (
                <span className="port-target">→{port.targetPort}</span>
              )}
              {port.nodePort && <span className="port-node">:{port.nodePort}</span>}
              <span className="port-proto">{port.protocol}</span>
            </span>
            <button
              className={`port-fwd-btn ${isForwarded ? 'active' : ''}`}
              onClick={(e) => { e.stopPropagation(); if (!isLoading) onPortForward(port.port); }}
              disabled={isLoading}
              title={isForwarded ? `localhost:${portForwards[portKey].localPort}` : 'Forward port'}
            >
              {isLoading ? '...' : isForwarded ? portForwards[portKey].localPort : 'FWD'}
            </button>
          </div>
        );
      })}
    </div>
  );
};

export default ServicePorts;
