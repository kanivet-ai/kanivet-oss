import { apiClient } from './client';

/* Flux CD + CloudNativePG endpoints. Shapes mirror backend/internal/gitops and
   backend/internal/cnpg; see their README.md. */

export interface ObjectRef {
  apiVersion?: string;
  kind: string;
  namespace?: string;
  name: string;
}

export interface FluxKind {
  kind: string;
  plural: string;
  group: string;
  version: string;
  category: 'source' | 'kustomization' | 'helmrelease';
}

export interface IntegrationsDetection {
  argo: boolean;
  argoNamespace: string;
  flux: { installed: boolean; kinds: FluxKind[] };
  cnpg: { installed: boolean; kinds: { kind: string; plural: string; version: string }[] };
}

export type FluxStatus = 'Ready' | 'Failed' | 'Progressing' | 'Suspended' | 'Unknown';

export interface FluxObject {
  kind: string;
  apiVersion: string;
  category: FluxKind['category'];
  namespace: string;
  name: string;
  status: FluxStatus;
  ready: string;
  reason?: string;
  message?: string;
  suspended: boolean;
  revision?: string;
  attemptedRevision?: string;
  url?: string;
  path?: string;
  sourceRef?: ObjectRef;
  chart?: string;
  chartVersion?: string;
  targetNamespace?: string;
  interval?: string;
  lastReconciledAt?: string;
  lastHandledReconcileAt?: string;
  inventoryCount: number;
  createdAt?: string;
}

export interface Condition {
  type: string;
  status: string;
  reason?: string;
  message?: string;
  lastTransitionTime?: string;
}

export interface HelmHistoryEntry {
  version: number;
  chartName?: string;
  chartVersion?: string;
  appVersion?: string;
  status?: string;
  firstDeployed?: string;
  lastDeployed?: string;
}

export interface FluxObjectDetail extends FluxObject {
  conditions: Condition[];
  inventory: ObjectRef[];
  history?: HelmHistoryEntry[];
}

export type GitOpsHealth = 'Healthy' | 'Failing' | 'Progressing' | 'Suspended' | 'Unknown';

export interface GitOpsItem {
  tool: 'argocd' | 'flux';
  kind: string;
  apiVersion: string;
  namespace: string;
  name: string;
  health: GitOpsHealth;
  detail?: string;
  message?: string;
  revision?: string;
  source?: string;
  suspended: boolean;
  updatedAt?: string;
}

export interface GitOpsStatus {
  argoInstalled: boolean;
  fluxInstalled: boolean;
  items: GitOpsItem[];
  byHealth: Record<string, number>;
  flux: { total: number; byStatus: Record<string, number>; byCategory: Record<string, number> };
  argoTotal: number;
  errors?: string[];
}

const ns = (namespace?: string) => encodeURIComponent(namespace || '_');
const fluxPath = (kind: string, namespace: string | undefined, name: string) =>
  `/cluster/gitops/flux/${encodeURIComponent(kind)}/${ns(namespace)}/${encodeURIComponent(name)}`;

export async function getIntegrationsDetection(cluster: string): Promise<IntegrationsDetection> {
  return apiClient.request('/cluster/integrations/detect', { cluster }, true);
}

export async function getGitOpsStatus(cluster: string): Promise<GitOpsStatus> {
  const r = await apiClient.requestFresh('/cluster/gitops/status', { cluster });
  return r.status;
}

export async function getFluxObject(cluster: string, kind: string, namespace: string, name: string): Promise<FluxObjectDetail> {
  const r = await apiClient.requestFresh(fluxPath(kind, namespace, name), { cluster });
  return r.object;
}

export async function reconcileFlux(cluster: string, kind: string, namespace: string, name: string, withSource = false): Promise<void> {
  await apiClient.getAxios().post(`${fluxPath(kind, namespace, name)}/reconcile`, {}, { params: { cluster, withSource: withSource ? 'true' : undefined } });
}

export async function setFluxSuspended(cluster: string, kind: string, namespace: string, name: string, suspend: boolean): Promise<void> {
  await apiClient.getAxios().post(`${fluxPath(kind, namespace, name)}/${suspend ? 'suspend' : 'resume'}`, {}, { params: { cluster } });
}

export type CNPGHealth = 'Healthy' | 'Degraded' | 'Failing' | 'Progressing';

export interface CNPGClusterSummary {
  namespace: string;
  name: string;
  phase?: string;
  phaseReason?: string;
  health: CNPGHealth;
  instances: number;
  readyInstances: number;
  currentPrimary?: string;
  targetPrimary?: string;
  timeline?: number;
  image?: string;
  storageSize?: string;
  backupMethod?: string;
  backupPlugin?: string;
  firstRecoverabilityPoint?: string;
  lastSuccessfulBackup?: string;
  lastFailedBackup?: string;
  createdAt?: string;
}

export interface CNPGInstance {
  name: string;
  role: 'primary' | 'replica';
  status: string;
  ready: boolean;
  node?: string;
  podPhase?: string;
  restarts: number;
  lagSeconds?: number;
}

export interface CNPGBackup {
  name: string;
  phase?: string;
  method?: string;
  target?: string;
  startedAt?: string;
  stoppedAt?: string;
  error?: string;
  createdAt?: string;
}

export interface CNPGScheduledBackup {
  name: string;
  schedule: string;
  suspended: boolean;
  immediate: boolean;
  method?: string;
  lastScheduleTime?: string;
  nextScheduleTime?: string;
}

export interface CNPGPooler {
  name: string;
  type: string;
  instances: number;
  poolMode?: string;
}

export interface CNPGClusterStatus {
  cluster: CNPGClusterSummary;
  instances: CNPGInstance[];
  backups: CNPGBackup[];
  scheduledBackups: CNPGScheduledBackup[];
  poolers: CNPGPooler[];
  conditions: Condition[];
  backupConfigured: boolean;
}

const cnpgPath = (namespace: string, name: string) => `/cluster/cnpg/clusters/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}`;

export async function getCNPGClusters(cluster: string): Promise<CNPGClusterSummary[]> {
  const r = await apiClient.requestFresh('/cluster/cnpg/clusters', { cluster });
  return r.items || [];
}

export async function getCNPGClusterStatus(cluster: string, namespace: string, name: string): Promise<CNPGClusterStatus> {
  const r = await apiClient.requestFresh(cnpgPath(namespace, name), { cluster });
  return r.status;
}

export async function cnpgBackupNow(cluster: string, namespace: string, name: string): Promise<{ name: string; method: string }> {
  const r = await apiClient.getAxios().post(`${cnpgPath(namespace, name)}/backup`, {}, { params: { cluster } });
  return r.data.result;
}

export async function cnpgPromote(cluster: string, namespace: string, name: string, instance: string): Promise<void> {
  await apiClient.getAxios().post(`${cnpgPath(namespace, name)}/promote`, { instance }, { params: { cluster } });
}

export async function cnpgRestart(cluster: string, namespace: string, name: string): Promise<void> {
  await apiClient.getAxios().post(`${cnpgPath(namespace, name)}/restart`, {}, { params: { cluster } });
}
