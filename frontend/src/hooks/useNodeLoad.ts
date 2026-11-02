import { useCallback, useEffect, useRef, useState } from 'react';
import api from '../services/api';
import type { NodeLoadReport } from '../utils/nodeColumns';
import { useVisibleInterval } from './useVisibleInterval';

// metrics-server refreshes node usage every 15s by default.
const POLL_MS = 15000;

/**
 * Polls the load of a cluster's nodes while a Nodes list is shown. Returns
 * null until the first report for `cluster` arrives; a failed poll keeps the
 * last figures.
 */
export const useNodeLoad = (
  cluster: string | null | undefined,
  enabled: boolean,
): NodeLoadReport | null => {
  const [latest, setLatest] = useState<{ cluster: string; report: NodeLoadReport } | null>(null);
  const inFlight = useRef<string | null>(null);
  const active = enabled && !!cluster;

  const load = useCallback(async () => {
    // The first request can wait for the cluster's pod watch to sync.
    if (!cluster || inFlight.current === cluster) return;
    inFlight.current = cluster;
    try {
      const report = await api.getNodeLoad(cluster);
      setLatest({ cluster, report });
    } catch {
      // Logged by the API client.
    } finally {
      if (inFlight.current === cluster) inFlight.current = null;
    }
  }, [cluster]);

  useEffect(() => {
    if (active) load();
  }, [active, load]);
  useVisibleInterval(load, POLL_MS, { enabled: active });

  return active && latest?.cluster === cluster ? latest.report : null;
};
