import { useEffect, useState } from 'react';
import { gitopsOwnerOf } from '../../utils/gitopsOwner';
import { getIntegrationsDetection, IntegrationsDetection } from '../../services/api/integrations';
import ArgoIcon from '../icons/ArgoIcon';
import FluxIcon from '../icons/FluxIcon';
import { openInspector } from './openInspector';
import './GitOpsPage.css';

/** Integration detection per cluster, fetched once: the Argo namespace and
    the served Flux versions are needed to resolve an owner reference. */
const detectionCache = new Map<string, Promise<IntegrationsDetection | null>>();
const detection = (cluster: string) => {
  let p = detectionCache.get(cluster);
  if (!p) {
    p = getIntegrationsDetection(cluster).catch(() => null);
    detectionCache.set(cluster, p);
  }
  return p;
};

/**
 * "Managed by Flux Kustomization apps" in the inspector header of any
 * resource a GitOps tool manages; click opens the owner.
 */
const GitOpsOwnerBadge = ({ cluster, resource }: { cluster: string; resource: any }) => {
  const metadata = resource?.metadata;
  const [det, setDet] = useState<IntegrationsDetection | null | undefined>(undefined);
  const raw = gitopsOwnerOf(metadata);

  useEffect(() => {
    if (!raw) return;
    let live = true;
    detection(cluster).then((d) => live && setDet(d));
    return () => { live = false; };
  }, [cluster, !!raw]);

  if (!raw || det === undefined) return null;
  const owner = gitopsOwnerOf(metadata, det?.argoNamespace || 'argocd')!;
  const served = det?.flux.kinds.find((k) => k.kind === owner.kind);
  if (served) owner.apiVersion = `${served.group}/${served.version}`;
  // An object never "owns" itself.
  if (owner.kind === resource.kind && owner.name === metadata?.name && owner.namespace === (metadata?.namespace || '')) return null;

  const toolName = owner.tool === 'flux' ? 'Flux' : 'Argo CD';
  return (
    <button
      type="button"
      className="gitops-owner-badge"
      title={`Managed by ${toolName} ${owner.kind} ${owner.namespace ? `${owner.namespace}/` : ''}${owner.name} (from ${owner.via})`}
      onClick={(e) => { e.stopPropagation(); openInspector(cluster, owner); }}
    >
      {owner.tool === 'flux' ? <FluxIcon width={12} height={12} /> : <ArgoIcon width={12} height={12} />}
      <span>{owner.kind === 'Application' ? 'Argo app' : owner.kind} {owner.name}</span>
    </button>
  );
};

export default GitOpsOwnerBadge;
