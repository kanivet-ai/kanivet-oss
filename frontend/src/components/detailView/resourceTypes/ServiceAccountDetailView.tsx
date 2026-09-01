import React, { useState, useEffect } from 'react';
import { PersonIcon, LockClosedIcon, Link2Icon } from '@radix-ui/react-icons';
import PropertyRow from '../../common/PropertyRow';
import PropertyGroup from '../shared/PropertyGroup';
import MetadataSection from '../shared/MetadataSection';
import { getApiBase } from '../../../services/api/types';
import { wsManager } from '../../../services/api/websocket';
import './ServiceAccountDetailView.css';

interface ServiceAccountDetailViewProps {
  resource: any;
  cluster: string;
  handleResourceClick: (kind: string, name: string, namespace?: string, e?: any) => void;
}

const ServiceAccountDetailView: React.FC<ServiceAccountDetailViewProps> = ({ resource, cluster, handleResourceClick }) => {
  const metadata = resource.metadata || {};
  const imagePullSecrets = resource.imagePullSecrets || [];
  const secrets = resource.secrets || [];
  const automountToken = resource.automountServiceAccountToken;

  const [roleBindings, setRoleBindings] = useState<any[]>([]);
  const [clusterRoleBindings, setClusterRoleBindings] = useState<any[]>([]);
  const [pods, setPods] = useState<any[]>([]);
  const [loading, setLoading] = useState(false);
  const [hasLoaded, setHasLoaded] = useState(false);

  const fetchAssociated = async () => {
    if (hasLoaded) return;
    setLoading(true);
    try {
      await wsManager.waitForSessionSecret();
      const sessionSecret = wsManager.getSessionSecret();
      const res = await fetch(`${getApiBase()}/cluster/serviceaccount/${metadata.namespace}/${metadata.name}/associated?cluster=${cluster}`, {
        headers: sessionSecret ? { 'X-Session-Secret': sessionSecret } : {},
      });
      if (res.ok) {
        const data = await res.json();
        setRoleBindings(data.roleBindings || []);
        setClusterRoleBindings(data.clusterRoleBindings || []);
        setPods(data.pods || []);
      }
      setHasLoaded(true);
    } catch (e) { console.error('Failed to fetch associated:', e); }
    finally { setLoading(false); }
  };

  useEffect(() => { setHasLoaded(false); setRoleBindings([]); setClusterRoleBindings([]); setPods([]); }, [metadata.name, metadata.namespace]);

  return (
    <>
      <PropertyGroup title="Service Account" icon={<PersonIcon />} defaultOpen>
        <PropertyRow label="Automount" value={automountToken === false ? 'No' : 'Yes'} />
        <PropertyRow label="Secrets" value={secrets.length} />
        <PropertyRow label="Pull Secrets" value={imagePullSecrets.length} />
      </PropertyGroup>
      <div className="section-divider" />

      <MetadataSection metadata={metadata} handleResourceClick={handleResourceClick} />
      <div className="section-divider" />

      {secrets.length > 0 && (
        <>
          <PropertyGroup title="Secrets" icon={<LockClosedIcon />} count={secrets.length} defaultOpen={false}>
            <div className="sa-secrets-list">
              {secrets.map((s: any, i: number) => (
                <div key={i} className="sa-secret-item">
                  <button className="link-button" onClick={(e) => handleResourceClick('Secret', s.name, metadata.namespace, e)}>{s.name}</button>
                </div>
              ))}
            </div>
          </PropertyGroup>
          <div className="section-divider" />
        </>
      )}

      {imagePullSecrets.length > 0 && (
        <>
          <PropertyGroup title="Image Pull Secrets" count={imagePullSecrets.length} defaultOpen={false}>
            <div className="sa-secrets-list">
              {imagePullSecrets.map((s: any, i: number) => (
                <div key={i} className="sa-secret-item">
                  <button className="link-button" onClick={(e) => handleResourceClick('Secret', s.name, metadata.namespace, e)}>{s.name}</button>
                </div>
              ))}
            </div>
          </PropertyGroup>
          <div className="section-divider" />
        </>
      )}

      <PropertyGroup
        title="Associated"
        icon={<Link2Icon />}
        defaultOpen={false}
        onOpenChange={(open) => { if (open) fetchAssociated(); }}
      >
        {loading ? (
          <div className="sa-loading">Loading...</div>
        ) : (
          <div className="sa-associated">
            {roleBindings.length > 0 && (
              <div className="sa-group">
                <div className="sa-group-header">RoleBindings ({roleBindings.length})</div>
                {roleBindings.map((rb: any) => (
                  <div key={rb.metadata.uid} className="sa-binding-item">
                    <button className="link-button" onClick={(e) => handleResourceClick('RoleBinding', rb.metadata.name, rb.metadata.namespace, e)}>{rb.metadata.name}</button>
                    <span className="sa-role-ref">→ {rb.roleRef.name}</span>
                  </div>
                ))}
              </div>
            )}
            {clusterRoleBindings.length > 0 && (
              <div className="sa-group">
                <div className="sa-group-header">ClusterRoleBindings ({clusterRoleBindings.length})</div>
                {clusterRoleBindings.map((crb: any) => (
                  <div key={crb.metadata.uid} className="sa-binding-item">
                    <button className="link-button" onClick={(e) => handleResourceClick('ClusterRoleBinding', crb.metadata.name, undefined, e)}>{crb.metadata.name}</button>
                    <span className="sa-role-ref">→ {crb.roleRef.name}</span>
                  </div>
                ))}
              </div>
            )}
            {pods.length > 0 && (
              <div className="sa-group">
                <div className="sa-group-header">Pods ({pods.length})</div>
                {pods.map((pod: any) => (
                  <div key={pod.metadata.uid} className="sa-pod-item">
                    <button className="link-button" onClick={(e) => handleResourceClick('Pod', pod.metadata.name, pod.metadata.namespace, e)}>{pod.metadata.name}</button>
                    <span className={`sa-pod-status status-${pod.status?.phase?.toLowerCase()}`}>{pod.status?.phase}</span>
                  </div>
                ))}
              </div>
            )}
            {hasLoaded && roleBindings.length === 0 && clusterRoleBindings.length === 0 && pods.length === 0 && (
              <div className="sa-empty">No associated resources found</div>
            )}
          </div>
        )}
      </PropertyGroup>
    </>
  );
};

export default ServiceAccountDetailView;
