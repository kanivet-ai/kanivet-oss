import { useCallback, useEffect, useRef, useState } from 'react';
import api from '../services/api';

const MAX_MESSAGES = 500;
const TOPIC = 'nats-tail';

export interface NatsTailMessage {
  cluster: string;
  subject: string;
  /** Base64-encoded raw bytes, same wire format as every other NATS payload in this app. */
  data: string;
  headers?: Record<string, string[]>;
  receivedAt: string;
}

interface UseNatsLiveTailOptions {
  cluster: string;
  subject: string;
  enabled: boolean;
}

interface UseNatsLiveTailResult {
  messages: NatsTailMessage[];
  connected: boolean;
  error: string | null;
  clear: () => void;
}

/**
 * Live Tail: a subject subscription streamed over the existing websocket
 * hub. Kept local to the component, not the shared store - this is an
 * ephemeral, one-tab-at-a-time stream, not cached state other tabs read.
 */
export const useNatsLiveTail = ({
  cluster,
  subject,
  enabled,
}: UseNatsLiveTailOptions): UseNatsLiveTailResult => {
  const [messages, setMessages] = useState<NatsTailMessage[]>([]);
  const [connected, setConnected] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const subscribedRef = useRef<{ cluster: string; subject: string } | null>(
    null,
  );

  const clear = useCallback(() => setMessages([]), []);

  useEffect(() => {
    if (!enabled || !cluster || !subject) return;

    const handler = (raw: any) => {
      const data = raw?.payload || raw;
      if (!data || data.cluster !== cluster || data.subject !== subject) return;

      if (data.error) {
        setError(data.error);
        setConnected(false);
        return;
      }
      setError(null);
      setConnected(true);
      setMessages((prev) =>
        [...prev, data as NatsTailMessage].slice(-MAX_MESSAGES),
      );
    };

    const wsApi = api as any;
    wsApi.wsHandlers.set(TOPIC, wsApi.wsHandlers.get(TOPIC) || new Set());
    wsApi.wsHandlers.get(TOPIC).add(handler);
    wsApi.__sendWS({
      type: TOPIC,
      payload: { action: 'subscribe', cluster, subject },
    });
    subscribedRef.current = { cluster, subject };
    setConnected(true);
    setError(null);

    return () => {
      const sub = subscribedRef.current;
      if (sub) {
        wsApi.__sendWS({
          type: TOPIC,
          payload: {
            action: 'unsubscribe',
            cluster: sub.cluster,
            subject: sub.subject,
          },
        });
      }
      const set = wsApi.wsHandlers.get(TOPIC);
      if (set) {
        set.delete(handler);
        if (set.size === 0) wsApi.wsHandlers.delete(TOPIC);
      }
      subscribedRef.current = null;
      setConnected(false);
    };
  }, [cluster, subject, enabled]);

  return { messages, connected, error, clear };
};

export default useNatsLiveTail;
