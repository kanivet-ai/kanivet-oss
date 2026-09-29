export interface NatsDetection {
  installed: boolean;
  hasMonitor: boolean;
  hasJetStreamCrds: boolean;
  legacyOperator: boolean;
  namespace?: string;
  serviceName?: string;
  monitorPort?: number;
}

export interface NatsVarz {
  server_id: string;
  version: string;
  uptime: string;
  connections: number;
  in_msgs: number;
  out_msgs: number;
  in_bytes: number;
  out_bytes: number;
  max_payload: number;
  cluster: { name?: string };
  gateway: { name?: string };
  leafnodes: number;
}

export interface NatsHealthz {
  status: string;
  error?: string;
}

export interface NatsConsumerDetail {
  name: string;
  num_ack_pending: number;
  num_redelivered: number;
  num_waiting: number;
  num_pending: number;
}

export interface NatsStreamState {
  messages: number;
  bytes: number;
  first_seq: number;
  last_seq: number;
}

export interface NatsStreamConfig {
  subjects?: string[];
  retention?: string;
  storage?: string;
  max_age?: number;
  max_bytes?: number;
  max_msgs?: number;
}

export interface NatsStreamDetail {
  name: string;
  config: NatsStreamConfig;
  state: NatsStreamState;
  consumer_detail: NatsConsumerDetail[];
}

export interface NatsAccountDetail {
  name: string;
  stream_detail: NatsStreamDetail[];
}

export interface NatsJsz {
  streams: number;
  consumers: number;
  messages: number;
  bytes: number;
  account_details: NatsAccountDetail[];
}

export interface NatsConnInfo {
  cid: number;
  ip: string;
  port: number;
  name?: string;
  account?: string;
  lang?: string;
  version?: string;
  uptime: string;
  subscriptions: number;
  in_msgs: number;
  out_msgs: number;
  in_bytes: number;
  out_bytes: number;
  podName?: string;
  podNamespace?: string;
}

export interface NatsConnz {
  num_connections: number;
  connections: NatsConnInfo[];
}

export interface NatsRates {
  inMsgsPerSec: number;
  outMsgsPerSec: number;
}

export interface NatsOverview {
  varz?: NatsVarz;
  jsz?: NatsJsz;
  healthz?: NatsHealthz;
  connz?: NatsConnz;
  rates?: NatsRates;
}
