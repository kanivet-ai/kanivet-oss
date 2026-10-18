import { useEffect, useRef, useState, useSyncExternalStore } from 'react';
import { wsManager } from '../../services/api/websocket';
import { LogBuffer } from './logEngine';
import { createLogFeedState, FeedEnd, FeedStatus } from './logFeedState';
import { subscribeLogStream } from './logSubscription';

export type { FeedEnd, FeedStatus } from './logFeedState';

export interface PodInfo {
  name: string;
  status: string;
  ready: boolean;
  restartCount: number;
  containers: string[];
}

export interface ContainerInfo {
  name: string;
  init: boolean;
}

export interface FeedOptions {
  cluster: string;
  namespace: string;
  name: string;
  kind: string;
  container: string;
  tailLines: number;
  previous: boolean;
}

const noSubscription = () => () => {};

// `active` false (a tab kept mounted while hidden) stops re-rendering on new
// lines; the buffer keeps filling and the view catches up when shown again.
export function useLogFeed(o: FeedOptions, active = true) {
  const bufferRef = useRef<LogBuffer | null>(null);
  if (!bufferRef.current) bufferRef.current = new LogBuffer();
  const buffer = bufferRef.current;

  const [pods, setPods] = useState<PodInfo[]>([]);
  const [containers, setContainers] = useState<ContainerInfo[]>([]);
  const [status, setStatus] = useState<FeedStatus>('connecting');
  const [error, setError] = useState<string | null>(null);
  const [end, setEnd] = useState<FeedEnd | null>(null);

  const key = `${o.kind}:${o.cluster}:${o.namespace}:${o.name}:${o.container}:${o.tailLines}:${o.previous ? 1 : 0}`;

  useEffect(() => {
    if (!o.cluster || !o.namespace || !o.name) return;
    const feed = createLogFeedState(buffer, { setStatus, setError, setEnd, setPods, setContainers });

    const handler = (raw: any) => {
      const d = raw?.payload || raw;
      if (!d || (d.key && d.key !== key)) return;
      feed.handle(d);
    };

    return subscribeLogStream(wsManager, window, {
      key,
      cluster: o.cluster,
      namespace: o.namespace,
      name: o.name,
      resourceType: o.kind,
      container: o.container || undefined,
      tailLines: o.tailLines,
      follow: !o.previous,
      previous: o.previous,
    }, handler, feed.restart, feed.disconnect);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key]);

  const version = useSyncExternalStore(active ? buffer.subscribe : noSubscription, buffer.getVersion);

  return { buffer, version, pods, containers, status, error, end };
}
