import { useStore } from '../../store';
import { getResourceDetails } from '../../services/api/resources';
import { kindToResource, parseApiVersion } from '../../utils/resourceUtils';
import { pageResources } from '../../store/navigationTargets';
import type { HealthIssue, HealthRef, InvestigateRequest } from '../../types/health';

/** The window event the AI agent listens to for an Investigate click. */
export const INVESTIGATE_EVENT = 'kanivet:investigate';

const LOG_KINDS = new Set(['Pod', 'Deployment', 'StatefulSet', 'DaemonSet', 'ReplicaSet', 'Job', 'CronJob']);
export const canOpenLogs = (ref: HealthRef) => LOG_KINDS.has(ref.kind);

function resourceOf(ref: HealthRef) {
  const { group, version } = parseApiVersion(ref.apiVersion || 'v1');
  return { kind: ref.kind, group, version, name: kindToResource(ref.kind), namespaced: !!ref.namespace };
}

function itemOf(ref: HealthRef) {
  return {
    name: ref.name,
    namespace: ref.namespace,
    metadata: { name: ref.name, namespace: ref.namespace },
    kind: ref.kind,
    apiVersion: ref.apiVersion || 'v1',
  };
}

async function fetchObject(cluster: string, ref: HealthRef) {
  const r = resourceOf(ref);
  return getResourceDetails(cluster, r.group, r.version, ref.kind, ref.namespace || '', ref.name, undefined, { fresh: true });
}

/** Opens the resource in the inspector. */
export async function openInspector(cluster: string, ref: HealthRef) {
  const { openDetailTab, loadDetails } = useStore.getState();
  const resource = resourceOf(ref);
  const item = itemOf(ref);
  openDetailTab(resource, item, cluster);
  try {
    await loadDetails(cluster, resource, item);
  } catch (e) {
    console.error('health: failed to load details', e);
  }
}

/** Opens logs in the console dock: a pod's, or every pod of a workload. */
export async function openLogs(cluster: string, ref: HealthRef) {
  const { openBottomTab } = useStore.getState();
  if (ref.kind === 'Pod') {
    let spec: any;
    try {
      spec = (await fetchObject(cluster, ref))?.spec;
    } catch {
      spec = undefined;
    }
    openBottomTab('logs', { kind: 'Pod', metadata: { name: ref.name, namespace: ref.namespace }, spec }, cluster);
    return;
  }
  openBottomTab('deployment-logs', { kind: ref.kind, metadata: { name: ref.name, namespace: ref.namespace } }, cluster);
}

/** Opens the object's YAML in the editor dock, where a fix snippet goes. */
export async function openYamlEditor(cluster: string, ref: HealthRef) {
  const obj = await fetchObject(cluster, ref);
  if (obj) useStore.getState().openBottomTab('edit', obj, cluster);
}

/** Asks the AI agent to investigate. Nothing happens when no agent listens. */
export function investigate(cluster: string, ref: HealthRef, issue?: HealthIssue) {
  const detail: InvestigateRequest = {
    cluster,
    resource: ref,
    issue,
    context: issue ? `${issue.title}: ${issue.message}` : undefined,
  };
  window.dispatchEvent(new CustomEvent<InvestigateRequest>(INVESTIGATE_EVENT, { detail }));
}

/** Opens the Issues or Audit page of the cluster. */
export async function openHealthPage(cluster: string, page: 'issues' | 'audit') {
  if (!cluster) return;
  const { openResourceListTab, recordNavigation } = useStore.getState();
  await openResourceListTab(pageResources[page], cluster);
  recordNavigation(page, page === 'issues' ? 'cluster-issues' : 'cluster-audit', { cluster }).catch(() => {});
}
