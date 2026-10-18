import React, { useCallback, useEffect, useRef, useState } from 'react';
import api from '../../../services/api';
import { useVisibleInterval } from '../../../hooks/useVisibleInterval';
import type { VClusterHostPod } from '../../../services/api/vclusters';
import PropertyRow from '../../common/PropertyRow';
import useResourceNavigation from '../../../hooks/useResourceNavigation';
import { useStore } from '../../../store';
import { parseClusterName } from '../../../utils/clusterUtils';

interface VClusterHostRowsProps {
  cluster: string;
  namespace: string;
  name: string;
  /** The pod's uid, node and phase: when one changes (scheduled, recreated
   * elsewhere), the host pod is resolved again. */
  uid?: string;
  nodeName?: string;
  phase?: string;
}

const HOST_POLL_MS = 10000;

/** Whether the host rows are still waiting on something that will change by
 * itself: the syncer has not copied the pod yet, the lookup failed, or the
 * host pod is not on a node yet. */
export const hostPodUnresolved = (hostPod: VClusterHostPod | null, error: string | null): boolean =>
  !!error || !hostPod || !hostPod.nodeName;

const hostBadge = <span className="ap-badge ap-badge--sm ap-badge--success">host</span>;
const hostNodeLabel = <>Node {hostBadge}</>;
const hostPodLabel = <>Pod {hostBadge}</>;

// Inside a vcluster, a pod's node is a synthetic copy that hides the real
// machine's capacity and neighbours. These rows resolve the host pod the
// syncer created and link to it, and to its node, in the host cluster's tab.
const VClusterHostRows: React.FC<VClusterHostRowsProps> = ({ cluster, namespace, name, uid, nodeName, phase }) => {
  const [hostPod, setHostPod] = useState<VClusterHostPod | null>(null);
  const [error, setError] = useState<string | null>(null);
  const { navigateToResource } = useResourceNavigation(cluster);
  // Answers for an earlier pod (or an earlier lookup) are dropped.
  const requestRef = useRef(0);

  const resolve = useCallback(() => {
    const request = ++requestRef.current;
    api.getVClusterHostPod(cluster, namespace, name)
      .then((result) => {
        if (request !== requestRef.current) return;
        setHostPod(result);
        setError(null);
      })
      .catch((err: any) => {
        if (request !== requestRef.current) return;
        setError(err?.response?.status === 404 ? 'Not synced to host' : 'Unavailable');
      });
  }, [cluster, namespace, name]);

  useEffect(() => {
    setHostPod(null);
    setError(null);
    resolve();
    return () => { requestRef.current++; };
  }, [resolve, uid, nodeName, phase]);

  // Until the host pod is on a node, look again: a Pending pod gets scheduled.
  useVisibleInterval(resolve, HOST_POLL_MS, { enabled: hostPodUnresolved(hostPod, error) });

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
