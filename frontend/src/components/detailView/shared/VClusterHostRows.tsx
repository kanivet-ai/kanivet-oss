import React, { useEffect, useState } from 'react';
import api from '../../../services/api';
import type { VClusterHostPod } from '../../../services/api/vclusters';
import PropertyRow from '../../common/PropertyRow';
import useResourceNavigation from '../../../hooks/useResourceNavigation';
import { useStore } from '../../../store';
import { parseClusterName } from '../../../utils/clusterUtils';

interface VClusterHostRowsProps {
  cluster: string;
  namespace: string;
  name: string;
}

const hostBadge = <span className="ap-badge ap-badge--sm ap-badge--success">host</span>;
const hostNodeLabel = <>Node {hostBadge}</>;
const hostPodLabel = <>Pod {hostBadge}</>;

// Inside a vcluster, a pod's node is a synthetic copy that hides the real
// machine's capacity and neighbours. These rows resolve the host pod the
// syncer created and link to it, and to its node, in the host cluster's tab.
const VClusterHostRows: React.FC<VClusterHostRowsProps> = ({ cluster, namespace, name }) => {
  const [hostPod, setHostPod] = useState<VClusterHostPod | null>(null);
  const [error, setError] = useState<string | null>(null);
  const { navigateToResource } = useResourceNavigation(cluster);

  useEffect(() => {
    let cancelled = false;
    setHostPod(null);
    setError(null);
    api.getVClusterHostPod(cluster, namespace, name)
      .then((result) => { if (!cancelled) setHostPod(result); })
      .catch((err: any) => {
        if (cancelled) return;
        setError(err?.response?.status === 404 ? 'Not synced to host' : 'Unavailable');
      });
    return () => { cancelled = true; };
  }, [cluster, namespace, name]);

  const openInHost = async (e: React.MouseEvent, kind: 'Node' | 'Pod', targetName: string, targetNamespace?: string) => {
    e.preventDefault();
    e.stopPropagation();
    if (!hostPod) return;
    await useStore.getState().openTab(hostPod.host);
    await navigateToResource({ cluster: hostPod.host, apiVersion: 'v1', kind, name: targetName, namespace: targetNamespace });
  };

  if (error) return <PropertyRow label={hostNodeLabel} value={error} />;
  if (!hostPod) return <PropertyRow label={hostNodeLabel} value="Resolving…" />;

  const hostLabel = parseClusterName(hostPod.host).displayName;
  return (
    <>
      <PropertyRow
        label={hostNodeLabel}
        copyText={hostPod.nodeName || undefined}
        value={hostPod.nodeName ? (
          <button
            className="link-button"
            onClick={(e) => openInHost(e, 'Node', hostPod.nodeName)}
            title={`Open node ${hostPod.nodeName} in ${hostLabel} (the vcluster Node above is a virtual copy)`}
          >
            {hostPod.nodeName}
          </button>
        ) : 'Not scheduled'}
      />
      <PropertyRow
        label={hostPodLabel}
        copyText={`${hostPod.namespace}/${hostPod.name}`}
        value={
          <button
            className="link-button"
            onClick={(e) => openInHost(e, 'Pod', hostPod.name, hostPod.namespace)}
            title={`Open pod ${hostPod.namespace}/${hostPod.name} in ${hostLabel}`}
          >
            {hostPod.namespace}/{hostPod.name}
          </button>
        }
      />
    </>
  );
};

export default VClusterHostRows;
