export interface HelmRelease {
  name: string;
  namespace: string;
  revision: number;
  status: string;
  chart: string;
  chartVersion: string;
  appVersion: string;
  updated: string;
  description: string;
  notes?: string;
  values?: Record<string, any>;
  manifest?: string;
  labels?: Record<string, string>;
}

export interface HelmChartMetadata {
  name: string;
  version: string;
  appVersion: string;
  description: string;
  home?: string;
  icon?: string;
  sources?: string[];
  keywords?: string[];
  maintainers?: string[];
  type?: string;
  apiVersion?: string;
}

export interface HelmHook {
  name: string;
  kind: string;
  path: string;
  events: string[];
  weight: number;
  deletePolicies?: string[];
}

export interface HelmManagedResource {
  kind: string;
  name: string;
  namespace?: string;
}

export interface HelmReleaseDetail extends HelmRelease {
  chartMetadata?: HelmChartMetadata;
  hooks?: HelmHook[];
  resources?: HelmManagedResource[];
}

export interface HelmHistoryEntry {
  revision: number;
  status: string;
  chart: string;
  appVersion: string;
  updated: string;
  description: string;
}

export interface HelmHistoryResponse {
  entries: HelmHistoryEntry[];
  hasMore: boolean;
}

export type HelmReleaseStatus = 
  | 'deployed'
  | 'uninstalled'
  | 'superseded'
  | 'failed'
  | 'uninstalling'
  | 'pending-install'
  | 'pending-upgrade'
  | 'pending-rollback'
  | 'unknown';

