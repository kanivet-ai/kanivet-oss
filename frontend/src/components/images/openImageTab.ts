import { useStore } from '../../store';
import type { ImagePullContext } from '../../services/api/images';
import { imageTabResource } from './imageUtils';

/** Opens (or focuses) the Image tab for ref in cluster. */
export function openImageTab(ref: string, cluster: string, pullContext: Omit<ImagePullContext, 'cluster'> = {}) {
  const clean = ref.trim();
  if (!clean || !cluster) return;
  const resource = imageTabResource({ ref: clean, pullContext: { ...pullContext, cluster } });
  void useStore.getState().openResourceListTab(resource, cluster, true);
}

/** The pull context of a container in a Pod object. */
export function podPullContext(pod: any, containerName?: string): Omit<ImagePullContext, 'cluster'> {
  const md = pod?.metadata || {};
  const statuses = [
    ...(pod?.status?.initContainerStatuses || []),
    ...(pod?.status?.containerStatuses || []),
  ];
  const st = statuses.find((s: any) => s.name === containerName);
  return {
    namespace: md.namespace,
    pod: md.name,
    container: containerName,
    imageID: st?.imageID || undefined,
  };
}

/** The pull context of a workload's pod template (no running digest). */
export function templatePullContext(resource: any): Omit<ImagePullContext, 'cluster'> {
  const spec = resource?.spec?.template?.spec || resource?.spec?.jobTemplate?.spec?.template?.spec || {};
  return {
    namespace: resource?.metadata?.namespace,
    serviceAccount: spec.serviceAccountName || undefined,
    pullSecrets: (spec.imagePullSecrets || []).map((s: any) => s.name).filter(Boolean),
  };
}
