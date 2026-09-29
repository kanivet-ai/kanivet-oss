export type NatsStreamKind = 'stream' | 'kv' | 'objectstore';

export interface ParsedStreamName {
  kind: NatsStreamKind;
  displayName: string;
}

/**
 * KV buckets and Object Store buckets are plain JetStream streams named by
 * convention (`KV_<bucket>`, `OBJ_<bucket>`) - the monitor endpoint's stream
 * list is enough to tell them apart from a regular stream, no NATS client
 * connection needed.
 */
export const parseStreamName = (name: string): ParsedStreamName => {
  if (name.startsWith('KV_')) return { kind: 'kv', displayName: name.slice(3) };
  if (name.startsWith('OBJ_'))
    return { kind: 'objectstore', displayName: name.slice(4) };
  return { kind: 'stream', displayName: name };
};
