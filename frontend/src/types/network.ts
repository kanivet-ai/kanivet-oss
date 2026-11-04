// Mirrors backend/internal/network/types.go and traffic/traffic.go.

export type NetRefKind = 'Pod' | 'Deployment' | 'StatefulSet' | 'DaemonSet' | 'ReplicaSet' | 'Job' | 'Service' | 'IP' | string;

export interface NetRef {
  kind: NetRefKind;
  namespace?: string;
  name?: string;
  ip?: string;
}

export interface ObjectRef {
  kind: string;
  apiVersion: string;
  namespace?: string;
  name: string;
}

export interface PodRef {
  namespace: string;
  name: string;
  ip?: string;
  ready: boolean;
  workload?: ObjectRef;
}

export interface RuleRef {
  policy: ObjectRef;
  direction: 'ingress' | 'egress';
  index: number;
}

export type HopStatus = 'pass' | 'fail' | 'warn' | 'skip';
export type Verdict = 'allowed' | 'blocked' | 'partial';

export interface Hop {
  id: string;
  kind: 'source' | 'egress' | 'service' | 'endpoints' | 'ingress' | 'port' | 'destination';
  title: string;
  status: HopStatus;
  detail: string;
  reasons?: string[];
  allowedBy?: RuleRef[];
  policies?: ObjectRef[];
  objects?: ObjectRef[];
}

export interface PathRequest {
  source: NetRef;
  destination: NetRef;
  port: number;
  protocol?: string;
}

export interface PathResult {
  verdict: Verdict;
  summary: string;
  hops: Hop[];
  port: number;
  protocol: string;
  sourcePods: PodRef[];
  destPods: PodRef[];
  pairs: number;
  pairsAllowed: number;
  warnings?: string[];
  enforcer?: string;
}

export interface WorkloadCoverage {
  workload: ObjectRef;
  pods: number;
  ingressIsolated: boolean;
  egressIsolated: boolean;
  ingressPolicies: ObjectRef[];
  egressPolicies: ObjectRef[];
  status: 'isolated' | 'partial' | 'open';
}

export interface PolicySummary {
  policy: ObjectRef;
  policyTypes: string[];
  selectedPods: number;
  ingressRules: number;
  egressRules: number;
  defaultDenyIngress: boolean;
  defaultDenyEgress: boolean;
  selector: string;
}

export interface CoverageReport {
  namespace: string;
  pods: number;
  ingressIsolated: number;
  egressIsolated: number;
  workloads: WorkloadCoverage[];
  policies: PolicySummary[];
  enforcer?: string;
  warnings?: string[];
}

export interface RuleView {
  rule: RuleRef;
  peers: string[];
  ports: string[];
  matches: number;
}

export interface Peer {
  workload: ObjectRef;
  ports: string[];
}

export interface DirectionView {
  isolated: boolean;
  policies: ObjectRef[];
  rules: RuleView[];
  summary: string;
  peers: Peer[];
  peersTruncated?: boolean;
  externalCidrs?: string[];
}

export interface PortMapping {
  name?: string;
  port: number;
  protocol: string;
  targetPort: string;
  resolvedPort?: number;
  exposed: boolean;
  detail: string;
  readyEndpoints: number;
  totalEndpoints: number;
}

export interface ServiceView {
  service: ObjectRef;
  type: string;
  ports: PortMapping[];
}

export interface ResourceNetwork {
  subject: NetRef;
  pods: PodRef[];
  ingress: DirectionView;
  egress: DirectionView;
  services: ServiceView[];
  enforcer?: string;
  warnings?: string[];
}

export interface PolicyDetail {
  policy: ObjectRef;
  policyTypes: string[];
  selector: string;
  selectedPods: PodRef[];
  selectedWorkloads: ObjectRef[];
  ingress: RuleView[];
  egress: RuleView[];
  summary: string[];
  enforcer?: string;
  warnings?: string[];
}

export interface Target {
  ref: NetRef;
  pods: number;
  ports?: number[];
  portNames?: string[];
}

export interface Targets {
  namespaces: string[];
  targets: Target[];
  truncated?: boolean;
}

export interface FlowEndpoint {
  namespace: string;
  workload: string;
  service?: string;
}

export interface Flow {
  source: FlowEndpoint;
  destination: FlowEndpoint;
  protocol: string;
  rps: number;
  errorRps: number;
  errorRate: number;
  p95Ms?: number;
}

export interface TrafficSource {
  name: string;
  status: 'available' | 'detected' | 'unavailable';
  detail: string;
  hints?: string[];
}

export interface FlowsResponse {
  available: boolean;
  source?: string;
  window: string;
  flows: Flow[];
  sources: TrafficSource[];
  error?: string;
}

export interface ProbeRequest {
  source: NetRef;
  destination: NetRef;
  port: number;
  mimicLabels: boolean;
  image?: string;
  confirm: boolean;
}

export interface ProbeResult {
  status: 'reached' | 'failed' | 'timeout' | 'forbidden' | 'error';
  reached: boolean;
  detail: string;
  output?: string;
  namespace: string;
  podName?: string;
  target: string;
  durationMs: number;
  cleaned: boolean;
  command: string;
  mimicLabels: boolean;
}
