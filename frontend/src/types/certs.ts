// Mirrors backend/internal/certs/types.go.

export type CertStatus = 'expired' | 'critical' | 'warning' | 'ok' | 'invalid';

export interface CertRef {
  apiVersion: string;
  kind: string;
  namespace?: string;
  name: string;
  hosts?: string[];
}

export interface CertChainEntry {
  subject: string;
  issuer: string;
  notBefore: string;
  notAfter: string;
  isCA: boolean;
  selfSigned: boolean;
}

export interface CertCondition {
  type: string;
  status: string;
  reason?: string;
  message?: string;
  lastTransitionTime?: string;
}

export interface CertManagerInfo {
  certificate: CertRef;
  issuerRef: CertRef;
  ready: boolean;
  reason?: string;
  message?: string;
  renewalTime?: string;
  notAfter?: string;
  dnsNames?: string[];
  conditions?: CertCondition[];
  failedRequests: number;
  pendingRequests: number;
  secretMissing?: boolean;
}

export interface CertificateInfo {
  id: string;
  source: 'secret' | 'webhook' | 'cert-manager';
  owner: CertRef;
  subject: string;
  commonName?: string;
  sans: string[];
  issuer: string;
  issuerCommonName?: string;
  issuerOrganization?: string;
  chain: CertChainEntry[];
  notBefore?: string;
  notAfter?: string;
  daysLeft: number;
  status: CertStatus;
  keyAlgorithm?: string;
  keySize?: number;
  signatureAlgorithm?: string;
  selfSigned: boolean;
  isCA: boolean;
  serialNumber?: string;
  fingerprintSha256?: string;
  parseError?: string;
  usedBy: CertRef[];
  certManager?: CertManagerInfo;
}

export interface CertIssue {
  type: string;
  severity: 'critical' | 'warning';
  message: string;
  resource: CertRef;
  certificateId?: string;
  host?: string;
}

export interface CertIssuer {
  ref: CertRef;
  type: string;
  ready: boolean;
  reason?: string;
  message?: string;
  server?: string;
}

export interface CertSummary {
  total: number;
  expired: number;
  critical: number;
  warning: number;
  ok: number;
  invalid: number;
  issues: number;
}

export interface CertReport {
  cluster: string;
  generatedAt: string;
  certificates: CertificateInfo[];
  issues: CertIssue[];
  issuers: CertIssuer[];
  certManagerDetected: boolean;
  gatewayApiDetected: boolean;
  summary: CertSummary;
  warnings?: string[];
}

export interface CertFilter {
  namespace?: string;
  status?: CertStatus[];
  expiringWithin?: number;
  search?: string;
  source?: string;
  kind?: string;
  resourceNamespace?: string;
  name?: string;
  refresh?: boolean;
}
