import { useCallback, useEffect, useRef, useState } from 'react';
import { getCertificates } from '../../services/api/certs';
import type { CertFilter, CertReport } from '../../types/certs';

// Last report per query, so reopening a page or inspector paints at once
// while a fresh copy loads.
const lastReports = new Map<string, CertReport>();

export function useCertificates(cluster: string, filter: CertFilter = {}, enabled = true) {
  const key = `${cluster}|${JSON.stringify(filter)}`;
  const [report, setReport] = useState<CertReport | null>(() => lastReports.get(key) ?? null);
  const [loading, setLoading] = useState(!lastReports.has(key));
  const [error, setError] = useState<string | null>(null);
  const seq = useRef(0);

  const load = useCallback(
    async (refresh = false) => {
      if (!cluster || !enabled) return;
      const id = ++seq.current;
      setLoading(true);
      try {
        const r = await getCertificates(cluster, { ...JSON.parse(key.slice(cluster.length + 1)), refresh });
        if (id !== seq.current) return;
        lastReports.set(key, r);
        setReport(r);
        setError(null);
      } catch (e: any) {
        if (id !== seq.current) return;
        setError(e?.response?.data?.error || e?.message || 'Could not load certificates');
      } finally {
        if (id === seq.current) setLoading(false);
      }
    },
    [cluster, key, enabled],
  );

  useEffect(() => {
    setReport(lastReports.get(key) ?? null);
    void load(false);
  }, [load, key]);

  return { report, loading, error, reload: () => load(false), refresh: () => load(true) };
}
