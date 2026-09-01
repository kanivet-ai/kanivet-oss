import { useEffect, useRef, useState, useSyncExternalStore } from 'react';
import { wsManager } from '../../services/api/websocket';
import { LogBuffer, RawBatchLine } from './logEngine';

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

export type FeedStatus = 'connecting' | 'live' | 'ended' | 'error';

export interface FeedOptions {
  cluster: string;
  namespace: string;
  name: string;
  kind: string;
  container: string;
  tailLines: number;
  previous: boolean;
}

export function useLogFeed(o: FeedOptions) {
  const bufferRef = useRef<LogBuffer | null>(null);
  if (!bufferRef.current) bufferRef.current = new LogBuffer();
  const buffer = bufferRef.current;

  const [pods, setPods] = useState<PodInfo[]>([]);
  const [containers, setContainers] = useState<ContainerInfo[]>([]);
  const [status, setStatus] = useState<FeedStatus>('connecting');
  const [error, setError] = useState<string | null>(null);

  const key = `${o.kind}:${o.cluster}:${o.namespace}:${o.name}:${o.container}:${o.tailLines}:${o.previous ? 1 : 0}`;

  useEffect(() => {
    if (!o.cluster || !o.namespace || !o.name) return;
    buffer.clear();
    setStatus('connecting');
    setError(null);
    let lastSeq = 0;

    const handler = (raw: any) => {
      const d = raw?.payload || raw;
      if (!d || (d.key && d.key !== key)) return;
      switch (d.type) {
        case 'connected':
          setStatus('live');
          break;
        case 'pods':
          setPods(d.pods || []);
          break;
        case 'containers':
          setContainers(d.containers || []);
          break;
        case 'end':
          setStatus('ended');
          break;
        case 'error':
          setError(d.error || 'log stream error');
          setStatus('error');
          break;
        case 'batch': {
          if (typeof d.sequence === 'number') {
            if (d.sequence <= lastSeq) return;
            lastSeq = d.sequence;
          }
          const lines: RawBatchLine[] = Array.isArray(d.lines)
            ? d.lines.map((l: any) => (typeof l === 'string' ? { data: l } : l))
            : [];
          buffer.append(lines);
          setStatus((s) => (s === 'connecting' ? 'live' : s));
          break;
        }
      }
    };

    const handlers = wsManager.getHandlers();
    if (!handlers.has('logs')) handlers.set('logs', new Set());
    handlers.get('logs')!.add(handler);

    wsManager.sendWS({
      type: 'logs',
      payload: {
        action: 'start',
        key,
        cluster: o.cluster,
        namespace: o.namespace,
        name: o.name,
        resourceType: o.kind,
        container: o.container || undefined,
        tailLines: o.tailLines,
        follow: !o.previous,
        previous: o.previous,
      },
    });

    return () => {
      wsManager.sendWS({ type: 'logs', payload: { action: 'stop', key } });
      const set = wsManager.getHandlers().get('logs');
      if (set) {
        set.delete(handler);
        if (set.size === 0) wsManager.getHandlers().delete('logs');
      }
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key]);

  const version = useSyncExternalStore(buffer.subscribe, buffer.getVersion);

  return { buffer, version, pods, containers, status, error };
}
