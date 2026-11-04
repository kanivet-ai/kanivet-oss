import React, { useMemo, useState } from 'react';
import ResourceLink from '../ResourceLink';
import type { CoverageReport, ObjectRef, WorkloadCoverage } from '../../types/network';
import { formatPct } from './networkUtils';

interface CoverageViewProps {
  cluster: string;
  report: CoverageReport | null;
  loading: boolean;
  error: string | null;
  onCheckFrom: (w: ObjectRef) => void;
}

type Filter = 'all' | 'open' | 'partial' | 'isolated';

const STATUS_TONE: Record<WorkloadCoverage['status'], string> = { isolated: 'success', partial: 'warning', open: 'danger' };
const STATUS_TEXT: Record<WorkloadCoverage['status'], string> = { isolated: 'Isolated', partial: 'Partly isolated', open: 'Open' };

const Link: React.FC<{ cluster: string; obj: ObjectRef }> = ({ cluster, obj }) => (
  <ResourceLink cluster={cluster} apiVersion={obj.apiVersion} kind={obj.kind} name={obj.name} namespace={obj.namespace}>{obj.name}</ResourceLink>
);

const PolicyList: React.FC<{ cluster: string; isolated: boolean; policies: ObjectRef[] }> = ({ cluster, isolated, policies }) =>
  !isolated ? <span className="net-muted">open</span> : (
    <span className="net-policy-list">
      {policies.map((p, i) => (
        <React.Fragment key={p.name}>{i > 0 && ', '}<Link cluster={cluster} obj={p} /></React.Fragment>
      ))}
    </span>
  );

const CoverageView: React.FC<CoverageViewProps> = ({ cluster, report, loading, error, onCheckFrom }) => {
  const [filter, setFilter] = useState<Filter>('all');
  const workloads = useMemo(
    () => (report?.workloads || []).filter((w) => filter === 'all' || w.status === filter),
    [report, filter],
  );
  const counts = useMemo(() => {
    const c = { open: 0, partial: 0, isolated: 0 };
    for (const w of report?.workloads || []) c[w.status]++;
    return c;
  }, [report]);

  if (error) return <div className="net-banner net-banner--danger" role="alert">{error}</div>;
  if (!report) return <div className="net-empty"><span className="ap-spinner" /> <span className="net-empty-text">Reading NetworkPolicies…</span></div>;

  const pct = (n: number) => (report.pods ? formatPct(n / report.pods) : '0%');

  return (
    <div className={`net-coverage${loading ? ' is-stale' : ''}`}>
      {report.warnings?.map((w, i) => <div key={i} className="net-banner net-banner--warning">{w}</div>)}
      <div className="net-tiles">
        <div className="net-tile ap-card">
          <span className="net-tile-value ap-tnum">{report.pods}</span>
          <span className="net-tile-label">Pods</span>
        </div>
        <div className="net-tile ap-card">
          <span className="net-tile-value ap-tnum">{pct(report.ingressIsolated)}</span>
          <span className="net-tile-label">Ingress isolated · {report.ingressIsolated} pods</span>
          <span className="net-meter"><span className="net-meter-fill" style={{ width: pct(report.ingressIsolated) }} /></span>
        </div>
        <div className="net-tile ap-card">
          <span className="net-tile-value ap-tnum">{pct(report.egressIsolated)}</span>
          <span className="net-tile-label">Egress isolated · {report.egressIsolated} pods</span>
          <span className="net-meter"><span className="net-meter-fill" style={{ width: pct(report.egressIsolated) }} /></span>
        </div>
        <div className="net-tile ap-card">
          <span className="net-tile-value ap-tnum">{report.policies.length}</span>
          <span className="net-tile-label">NetworkPolicies{report.enforcer ? ` · ${report.enforcer}` : ''}</span>
        </div>
      </div>

      <div className="net-section-head">
        <span className="ap-group-label net-section-label">Workloads</span>
        <div className="ap-segmented net-filter" role="group" aria-label="Filter by isolation">
          {(['all', 'open', 'partial', 'isolated'] as Filter[]).map((f) => (
            <button key={f} type="button" aria-pressed={filter === f} onClick={() => setFilter(f)}>
              {f === 'all' ? `All ${report.workloads.length}` : `${STATUS_TEXT[f]} ${counts[f]}`}
            </button>
          ))}
        </div>
      </div>
      {workloads.length === 0 ? (
        <div className="net-empty"><span className="net-empty-text">No workloads match.</span></div>
      ) : (
        <div className="net-table ap-card" role="table">
          <div className="net-tr net-th" role="row">
            <span role="columnheader">Workload</span>
            <span role="columnheader">Pods</span>
            <span role="columnheader">Status</span>
            <span role="columnheader">Ingress</span>
            <span role="columnheader">Egress</span>
            <span role="columnheader" aria-label="Actions" />
          </div>
          {workloads.map((w) => (
            <div className="net-tr" role="row" key={`${w.workload.kind}/${w.workload.namespace}/${w.workload.name}`}>
              <span role="cell" className="net-td-name">
                <span className="net-kind">{w.workload.kind}</span>
                {!report.namespace && <span className="net-ns">{w.workload.namespace}/</span>}
                <Link cluster={cluster} obj={w.workload} />
              </span>
              <span role="cell" className="ap-tnum">{w.pods}</span>
              <span role="cell"><span className={`ap-dot ap-dot--${STATUS_TONE[w.status]}`} /> {STATUS_TEXT[w.status]}</span>
              <span role="cell"><PolicyList cluster={cluster} isolated={w.ingressIsolated} policies={w.ingressPolicies} /></span>
              <span role="cell"><PolicyList cluster={cluster} isolated={w.egressIsolated} policies={w.egressPolicies} /></span>
              <span role="cell">
                <button type="button" className="ap-btn ap-btn--ghost ap-btn--sm" onClick={() => onCheckFrom(w.workload)}>Check path</button>
              </span>
            </div>
          ))}
        </div>
      )}

      <div className="net-section-head">
        <span className="ap-group-label net-section-label">Policies</span>
      </div>
      {report.policies.length === 0 ? (
        <div className="net-empty">
          <div className="net-empty-title">No NetworkPolicies</div>
          <div className="net-empty-text">Every pod here accepts traffic from anywhere and can connect anywhere. A default-deny policy is the usual first step.</div>
        </div>
      ) : (
        <div className="net-table net-table--policies ap-card" role="table">
          <div className="net-tr net-th" role="row">
            <span role="columnheader">Policy</span>
            <span role="columnheader">Selects</span>
            <span role="columnheader">Pods</span>
            <span role="columnheader">Types</span>
            <span role="columnheader">Rules</span>
          </div>
          {report.policies.map((p) => (
            <div className="net-tr" role="row" key={`${p.policy.namespace}/${p.policy.name}`}>
              <span role="cell" className="net-td-name">
                {!report.namespace && <span className="net-ns">{p.policy.namespace}/</span>}
                <Link cluster={cluster} obj={p.policy} />
                {p.defaultDenyIngress && <span className="ap-badge ap-badge--sm ap-badge--danger">default-deny ingress</span>}
                {p.defaultDenyEgress && <span className="ap-badge ap-badge--sm ap-badge--danger">default-deny egress</span>}
              </span>
              <span role="cell" className="ap-mono net-selector">{p.selector}</span>
              <span role="cell" className="ap-tnum">{p.selectedPods}</span>
              <span role="cell">{p.policyTypes.join(', ')}</span>
              <span role="cell" className="ap-tnum">{p.ingressRules} in · {p.egressRules} out</span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
};

export default CoverageView;
