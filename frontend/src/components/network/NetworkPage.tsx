import React, { useCallback, useEffect, useState } from 'react';
import { ReloadIcon } from '@radix-ui/react-icons';
import { getErrorMessage } from '../../utils/errorMessage';
import { getNetworkCoverage, getNetworkFlows, getNetworkTargets } from '../../services/api/network';
import type { CoverageReport, FlowsResponse, Targets } from '../../types/network';
import { RouteIcon } from '../icons/kube';
import CoverageView from './CoverageView';
import PathChecker from './PathChecker';
import TrafficView from './TrafficView';
import { onNetworkIntent, takeNetworkIntent, type NetworkIntent } from './networkUtils';
import './Network.css';

type View = 'path' | 'coverage' | 'traffic';

const VIEWS: { id: View; label: string }[] = [
  { id: 'path', label: 'Path check' },
  { id: 'coverage', label: 'Policy coverage' },
  { id: 'traffic', label: 'Traffic' },
];

const NetworkPage: React.FC<{ cluster: string }> = ({ cluster }) => {
  const [intent, setIntent] = useState<NetworkIntent | null>(() => takeNetworkIntent());
  const [view, setView] = useState<View>(intent?.view || 'path');
  const [namespace, setNamespace] = useState(intent?.namespace || '');
  const [targets, setTargets] = useState<Targets | null>(null);
  const [targetsError, setTargetsError] = useState<string | null>(null);
  const [coverage, setCoverage] = useState<CoverageReport | null>(null);
  const [coverageError, setCoverageError] = useState<string | null>(null);
  const [flows, setFlows] = useState<FlowsResponse | null>(null);
  const [flowsError, setFlowsError] = useState<string | null>(null);
  const [windowMinutes, setWindowMinutes] = useState(5);
  const [loading, setLoading] = useState(false);
  const [reload, setReload] = useState(0);

  // Intents sent while the page is already open.
  useEffect(() => onNetworkIntent(() => {
    const i = takeNetworkIntent();
    if (!i) return;
    setIntent(i);
    setView(i.view);
    if (i.namespace !== undefined) setNamespace(i.namespace);
  }), []);

  useEffect(() => {
    let live = true;
    setTargetsError(null);
    getNetworkTargets(cluster)
      .then((t) => { if (live) setTargets(t); })
      .catch((e) => { if (live) setTargetsError(getErrorMessage(e, 'Could not load the cluster\'s workloads')); });
    return () => { live = false; };
  }, [cluster, reload]);

  useEffect(() => {
    if (view !== 'coverage') return;
    let live = true;
    setLoading(true);
    setCoverageError(null);
    getNetworkCoverage(cluster, namespace)
      .then((c) => { if (live) setCoverage(c); })
      .catch((e) => { if (live) setCoverageError(getErrorMessage(e, 'Could not read NetworkPolicies')); })
      .finally(() => { if (live) setLoading(false); });
    return () => { live = false; };
  }, [cluster, namespace, view, reload]);

  useEffect(() => {
    if (view !== 'traffic') return;
    let live = true;
    setLoading(true);
    setFlowsError(null);
    getNetworkFlows(cluster, namespace, windowMinutes)
      .then((f) => { if (live) setFlows(f); })
      .catch((e) => { if (live) setFlowsError(getErrorMessage(e, 'Could not read traffic metrics')); })
      .finally(() => { if (live) setLoading(false); });
    return () => { live = false; };
  }, [cluster, namespace, view, windowMinutes, reload]);

  const onIntentUsed = useCallback(() => setIntent(null), []);
  const checkPath = (i: NetworkIntent) => { setIntent(i); setView('path'); };

  return (
    <div className="net-page">
      <div className="net-header">
        <div className="net-title">
          <span className="net-title-icon"><RouteIcon /></span>
          <h2>Network</h2>
        </div>
        <div className="ap-segmented net-views" role="tablist" aria-label="Network views">
          {VIEWS.map((v) => (
            <button key={v.id} type="button" role="tab" aria-selected={view === v.id} aria-pressed={view === v.id} onClick={() => setView(v.id)}>
              {v.label}
            </button>
          ))}
        </div>
        <div className="net-header-actions">
          {view !== 'path' && (
            <select className="ap-select net-ns-select" value={namespace} onChange={(e) => setNamespace(e.target.value)} aria-label="Namespace">
              <option value="">All namespaces</option>
              {(targets?.namespaces || []).map((n) => <option key={n} value={n}>{n}</option>)}
            </select>
          )}
          <button type="button" className="ap-icon-btn" onClick={() => setReload((r) => r + 1)} aria-label="Refresh" title="Refresh">
            <ReloadIcon className={loading ? 'net-spin' : undefined} />
          </button>
        </div>
      </div>

      <div className="net-body">
        {targetsError && view === 'path' && <div className="net-banner net-banner--danger" role="alert">{targetsError}</div>}
        {view === 'path' && <PathChecker cluster={cluster} targets={targets} intent={intent} onIntentUsed={onIntentUsed} />}
        {view === 'coverage' && (
          <CoverageView
            cluster={cluster}
            report={coverage}
            loading={loading}
            error={coverageError}
            onCheckFrom={(w) => checkPath({ view: 'path', source: { kind: w.kind, namespace: w.namespace, name: w.name } })}
          />
        )}
        {view === 'traffic' && (
          <TrafficView
            flows={flows}
            loading={loading}
            error={flowsError}
            windowMinutes={windowMinutes}
            onWindow={setWindowMinutes}
            onCheck={(f) => checkPath({
              view: 'path',
              query: `can ${f.source.namespace}/${f.source.workload} reach ${f.destination.namespace}/${f.destination.service || f.destination.workload}`,
            })}
          />
        )}
      </div>
    </div>
  );
};

export default NetworkPage;
