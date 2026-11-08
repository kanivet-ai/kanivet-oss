import type { HealthRef } from './health';

export type CisStatus =
  | 'automated-pass'
  | 'automated-fail'
  | 'manual'
  | 'provider-responsibility'
  | 'not-visible'
  | 'error';

export interface ProviderEvidence {
  signal: string;
  value: string;
  provider: string;
  weight: number;
}

export interface ProviderInfo {
  provider: string;
  distribution: string;
  version: string;
  gitVersion?: string;
  managedControlPlane: boolean;
  controlPlaneVisible: boolean;
  variants?: string[];
  evidence: ProviderEvidence[];
  confidence: number;
  overridden?: boolean;
}

export interface CisValue {
  name: string;
  value: string;
  source?: string;
  ok: boolean;
}

export interface CisNodeGroup {
  pool: string;
  nodes: string[];
  totalNodes: number;
  value: string;
  ok: boolean;
  error?: string;
}

export interface CisScannerFinding {
  source: string;
  report?: string;
  status: CisStatus;
  detail?: string;
  scannedAt?: string;
}

export interface CisControl {
  id: string;
  section: string;
  sectionTitle: string;
  subsection: string;
  subsectionTitle: string;
  title: string;
  description: string;
  scored: boolean;
  level: number;
  status: CisStatus;
  source: string;
  message?: string;
  manualCheck?: string;
  resources?: HealthRef[];
  resourcesTotal?: number;
  values?: CisValue[];
  nodeGroups?: CisNodeGroup[];
  remediation?: string;
  snippet?: string;
  scanners?: CisScannerFinding[];
  disagreement?: boolean;
}

export interface CisSection {
  id: string;
  title: string;
  score: number | null;
  counts: Partial<Record<CisStatus, number>>;
}

export interface CisProfile {
  id: string;
  title: string;
  version: string;
  description: string;
}

export interface CisScannerSource {
  source: string;
  report: string;
  profile?: string;
  controls: number;
  scannedAt?: string;
  used: boolean;
}

export interface CisNote {
  kind: 'rbac' | 'provider' | 'scanner' | 'info';
  text: string;
}

export interface CisReport {
  cluster: string;
  provider: ProviderInfo;
  profile: CisProfile;
  profiles: CisProfile[];
  autoProfile: string;
  generatedAt: string;
  score: number | null;
  counts: Partial<Record<CisStatus, number>>;
  sections: CisSection[];
  controls: CisControl[];
  scanners: CisScannerSource[];
  notes: CisNote[];
}

export interface CisOverride {
  provider?: string;
  profile?: string;
}
