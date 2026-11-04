// Mirrors backend/internal/upgrade (Report and Catalog).

export type UpgradeSeverity = 'blocker' | 'warning' | 'info';
export type UpgradeSource = 'live' | 'last-applied' | 'helm';

export interface UpgradeAffectedResource {
  apiVersion: string;
  liveApiVersion?: string;
  kind: string;
  name: string;
  namespace?: string;
  source: UpgradeSource;
  helmRelease?: string;
  helmNamespace?: string;
  severity: UpgradeSeverity;
}

export interface UpgradeAPIFinding {
  severity: UpgradeSeverity;
  apiVersion: string;
  kind: string;
  resource: string;
  deprecatedIn: string;
  removedIn?: string;
  replacement?: string;
  hint: string;
  removedByTarget: boolean;
  alreadyRemoved: boolean;
  served: boolean;
  liveRequests: boolean;
  message: string;
  resources: UpgradeAffectedResource[];
}

export interface UpgradeNodeFinding {
  severity: UpgradeSeverity;
  node: string;
  kubeletVersion: string;
  message: string;
}

export interface UpgradeCRDFinding {
  severity: UpgradeSeverity;
  name: string;
  kind: string;
  version: string;
  message: string;
}

export interface UpgradeCheck {
  id: string;
  label: string;
  status: 'ok' | 'partial' | 'unavailable';
  detail?: string;
}

export interface UpgradeReport {
  cluster: string;
  currentVersion: string;
  serverGitVersion: string;
  targetVersion: string;
  availableTargets: string[] | null;
  path: string[] | null;
  summary: { blockers: number; warnings: number; infos: number; ok: number; verdict: 'blocked' | 'warnings' | 'ready' };
  apis: UpgradeAPIFinding[];
  nodes: UpgradeNodeFinding[];
  nodeSummary: { total: number; minKubelet?: string; maxKubelet?: string; maxSkew: number };
  crds: UpgradeCRDFinding[];
  notes: string[];
  checks: UpgradeCheck[];
  catalogReviewed: string;
  catalogSource: string;
  collectedAt: string;
}

export interface UpgradeDeprecation {
  group: string;
  version: string;
  kind: string;
  resource: string;
  deprecatedIn: string;
  removedIn: string;
  replacement: string;
  hint: string;
}

export interface UpgradeCatalog {
  source: string;
  reviewed: string;
  latestRelease: string;
  apis: UpgradeDeprecation[];
}
