import React from 'react';
import type { Flow, FlowsResponse } from '../../types/network';
import { formatPct, formatRate } from './networkUtils';

interface TrafficViewProps {
  flows: FlowsResponse | null;
  loading: boolean;
  error: string | null;
  windowMinutes: number;
  onWindow: (m: number) => void;
  onCheck: (f: Flow) => void;
}

const WINDOWS = [5, 15, 60];

const SOURCE_TONE: Record<string, string> = { available: 'success', detected: 'warning', unavailable: 'muted' };

const TrafficView: React.FC<TrafficViewProps> = ({ flows, loading, error, windowMinutes, onWindow, onCheck }) => {
  if (error) return <div className="net-banner net-banner--danger" role="alert">{error}</div>;
  if (!flows) return <div className="net-empty"><span className="ap-spinner" /> <span className="net-empty-text">Looking for Istio and Hubble metrics…</span></div>;

  if (!flows.available) {
    return (
      <div className="net-traffic-empty">
        <div className="net-empty">
          <div className="net-empty-title">No traffic telemetry found</div>
          <div className="net-empty-text">
            Observed flows need a service mesh or an eBPF CNI that exports metrics to Prometheus. The path checker and the
            policy coverage view work without them.
          </div>
        </div>
        <div className="net-sources">
          {flows.sources.map((s) => (
            <div key={s.name} className="net-source ap-card">
              <div className="net-source-head">
                <span className={`ap-dot ap-dot--${SOURCE_TONE[s.status] || 'muted'}`} />
                <span className="net-source-name">{s.name}</span>
                <span className="net-muted">{s.status === 'detected' ? 'Installed, metrics missing' : s.status === 'available' ? 'Available' : 'Not installed'}</span>
              </div>
              <div className="net-source-detail">{s.detail}</div>
              {s.hints && s.hints.length > 0 && (
                <ul className="net-hints">
                  {s.hints.map((h, i) => <li key={i}>{h}</li>)}
                </ul>
              )}
            </div>
          ))}
        </div>
        {flows.error && <div className="net-banner net-banner--warning">{flows.error}</div>}
      </div>
    );
  }

  return (
    <div className={`net-traffic${loading ? ' is-stale' : ''}`}>
      <div className="net-section-head">
        <span className="ap-group-label net-section-label">
          {flows.flows.length} flows · from {flows.source === 'istio' ? 'Istio' : 'Hubble'}
        </span>
        <div className="ap-segmented net-filter" role="group" aria-label="Window">
          {WINDOWS.map((w) => (
            <button key={w} type="button" aria-pressed={windowMinutes === w} onClick={() => onWindow(w)}>
              {w < 60 ? `${w}m` : `${w / 60}h`}
            </button>
          ))}
        </div>
      </div>
      {flows.flows.length === 0 ? (
        <div className="net-empty"><span className="net-empty-text">No traffic in this window.</span></div>
      ) : (
        <div className="net-table net-table--flows ap-card" role="table">
          <div className="net-tr net-th" role="row">
            <span role="columnheader">Source</span>
            <span role="columnheader">Destination</span>
            <span role="columnheader">Protocol</span>
            <span role="columnheader">Rate</span>
            <span role="columnheader">{flows.source === 'hubble' ? 'Drops' : 'Errors'}</span>
            <span role="columnheader">p95</span>
            <span role="columnheader" aria-label="Actions" />
          </div>
          {flows.flows.map((f, i) => {
            const errTone = f.errorRate >= 0.05 ? 'danger' : f.errorRate > 0 ? 'warning' : 'success';
            return (
              <div className="net-tr" role="row" key={i}>
                <span role="cell" className="net-td-name"><span className="net-ns">{f.source.namespace}/</span>{f.source.workload}</span>
                <span role="cell" className="net-td-name">
                  <span className="net-ns">{f.destination.namespace}/</span>{f.destination.workload}
                  {f.destination.service && f.destination.service !== f.destination.workload && <span className="net-muted"> via {f.destination.service}</span>}
                </span>
                <span role="cell">{f.protocol}</span>
                <span role="cell" className="ap-tnum">{formatRate(f.rps)}</span>
                <span role="cell" className="ap-tnum"><span className={`ap-dot ap-dot--${errTone}`} /> {formatPct(f.errorRate)}</span>
                <span role="cell" className="ap-tnum">{f.p95Ms ? `${Math.round(f.p95Ms)} ms` : '–'}</span>
                <span role="cell">
                  {f.source.workload !== 'unknown' && f.destination.workload !== 'unknown' && (
                    <button type="button" className="ap-btn ap-btn--ghost ap-btn--sm" onClick={() => onCheck(f)}>Check path</button>
                  )}
                </span>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
};

export default TrafficView;
