// The cluster view: every namespace as a card with its health rollup and
// applications. Clicking one drills into its Application view.
import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { getTopologyOverview, type NamespaceSummary, type TopologyOverview } from '../../services/api/topology';
import { failureMessage } from '../../utils/errorMessage';
import type { Health } from './graphModel';
import { summarizeCounts } from './graphModel';

interface Props {
  cluster: string;
  onOpen: (namespace: string) => void;
}

const POLL_MS = 10000;
const ORDER: Health[] = ['error', 'warning', 'unknown', 'healthy'];
const rank = (h: Health) => ORDER.indexOf(h);

const Bar: React.FC<{ c: NamespaceSummary['pods'] }> = ({ c }) => {
  const total = c.healthy + c.warning + c.error + c.unknown;
  if (!total) return <span className="topo-healthbar topo-healthbar--empty" aria-hidden />;
  return (
    <span className="topo-healthbar" aria-hidden>
      {ORDER.filter((h) => c[h] > 0).map((h) => (
        <span key={h} className={`topo-healthbar-seg topo-healthbar-seg--${h}`} style={{ flexGrow: c[h] / total }} />
      ))}
    </span>
  );
};

const ClusterOverview: React.FC<Props> = ({ cluster, onOpen }) => {
  const [data, setData] = useState<TopologyOverview | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [hideSystem, setHideSystem] = useState(true);

  const load = useCallback(async (signal?: AbortSignal) => {
    try {
      const ov = await getTopologyOverview(cluster, signal);
      setData(ov);
      setError(null);
    } catch (e) {
      if (!signal?.aborted) setError(failureMessage('Could not load the cluster overview', e));
    }
  }, [cluster]);

  useEffect(() => {
    const ctrl = new AbortController();
    setData(null);
    void load(ctrl.signal);
    const t = window.setInterval(() => { if (document.visibilityState === 'visible') void load(); }, POLL_MS);
    return () => { ctrl.abort(); window.clearInterval(t); };
  }, [load]);

  const list = useMemo(() => {
    const all = data?.namespaces || [];
    const isSystem = (ns: string) => ns.startsWith('kube-') || ns === 'kube-system';
    const shown = hideSystem ? all.filter((n) => !isSystem(n.namespace)) : all;
    return [...shown].sort((a, b) => rank(a.health) - rank(b.health) || a.namespace.localeCompare(b.namespace));
  }, [data, hideSystem]);

  if (!data && error) {
    return (
      <div className="topo-empty" role="alert">
        <span>{error}</span>
        <button type="button" className="ap-btn" onClick={() => void load()}>Try again</button>
      </div>
    );
  }
  if (!data) return <div className="topo-empty" role="status"><span className="topo-spinner" /> Reading the cluster…</div>;

  const failing = list.filter((n) => n.health === 'error').length;
  const degraded = list.filter((n) => n.health === 'warning').length;
  const pods = list.reduce((a, n) => a + n.pods.healthy + n.pods.warning + n.pods.error + n.pods.unknown, 0);
  const systemCount = (data.namespaces || []).filter((n) => n.namespace.startsWith('kube-')).length;

  return (
    <div className="topo-overview">
      <div className="topo-overview-scroll">
        {data.warnings?.length ? <div className="topo-notice" role="status">Partial view ({data.warnings.join(', ')}).</div> : null}
        <div className="topo-overview-head">
          <span className="topo-overview-title">Namespaces</span>
          <span className="topo-overview-sub">
            {list.length} namespace{list.length === 1 ? '' : 's'} · {pods} pods
            {failing ? ` · ${failing} failing` : ''}{degraded ? ` · ${degraded} degraded` : ''}
          </span>
          <span className="topo-toolbar-spacer" />
          {systemCount > 0 && (
            <label className="topo-check">
              <input type="checkbox" checked={!hideSystem} onChange={(e) => setHideSystem(!e.target.checked)} />
              <span>System namespaces</span>
            </label>
          )}
        </div>
        {list.length === 0 ? (
          <div className="topo-empty">No workloads found in this cluster.</div>
        ) : (
          <div className="topo-ns-grid" role="list">
            {list.map((n) => {
              const podTotal = n.pods.healthy + n.pods.warning + n.pods.error + n.pods.unknown;
              const wlTotal = n.workloads.healthy + n.workloads.warning + n.workloads.error + n.workloads.unknown;
              const extra = n.appCount - Math.min(4, n.apps?.length || 0);
              return (
                <button key={n.namespace} type="button" role="listitem" className={`topo-ns-card topo-ns-card--${n.health}`} onClick={() => onOpen(n.namespace)}>
                  <span className="topo-ns-card-head">
                    <span className={`topo-dot topo-dot--${n.health}`} aria-hidden />
                    <span className="topo-ns-card-name">{n.namespace}</span>
                    <svg className="topo-ns-card-chev" viewBox="0 0 10 10" aria-hidden><path d="M3.5 2 7 5 3.5 8" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" /></svg>
                  </span>
                  <Bar c={n.pods} />
                  <span className="topo-ns-card-stats">
                    <span>{podTotal} pod{podTotal === 1 ? '' : 's'}{podTotal ? ` · ${summarizeCounts(n.pods)}` : ''}</span>
                    <span>{wlTotal} workload{wlTotal === 1 ? '' : 's'}</span>
                  </span>
                  {n.apps && n.apps.length > 0 && (
                    <span className="topo-ns-card-apps">
                      {n.apps.slice(0, 4).map((a) => <span key={a} className="topo-app-chip">{a}</span>)}
                      {extra > 0 ? <span className="topo-app-chip topo-app-chip--more">+{extra}</span> : null}
                    </span>
                  )}
                </button>
              );
            })}
          </div>
        )}
      </div>
      <div className="ap-statusbar topo-status">Cluster overview · click a namespace to see its applications · live</div>
    </div>
  );
};

export default ClusterOverview;
