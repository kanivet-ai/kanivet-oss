import { useStore } from '../../store';
import { kindToResource, parseApiVersion } from '../../utils/resourceUtils';

export interface InspectorRef {
  apiVersion: string;
  kind: string;
  namespace?: string;
  name: string;
}

/** Opens a resource in the inspector, the same way a list row does. */
export function openInspector(cluster: string, ref: InspectorRef, isPinned = false): void {
  const { group, version } = parseApiVersion(ref.apiVersion);
  const resource = { name: kindToResource(ref.kind), group, version, kind: ref.kind, namespaced: !!ref.namespace };
  const item = { name: ref.name, namespace: ref.namespace, uid: `${ref.namespace || 'cluster'}-${ref.name}`, kind: ref.kind, apiVersion: ref.apiVersion };
  const { openDetailTab, loadDetails } = useStore.getState();
  openDetailTab(resource, item, cluster, isPinned);
  loadDetails(cluster, resource, item).catch(() => {});
}
