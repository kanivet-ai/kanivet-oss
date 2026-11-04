import React, { useEffect, useMemo, useState } from 'react';
import { MagnifyingGlassIcon, ReloadIcon } from '@radix-ui/react-icons';
import { refreshIssues, useIssues } from './healthStore';
import { groupBySeverity, matchIssue, SEVERITY_LABEL } from './healthView';
import { HealthEmpty, IssueRow, SeverityDot, UnavailableNote } from './HealthParts';
import { openHealthPage } from './healthActions';
import type { HealthSeverity } from '../../types/health';
import './Health.css';

const IssuesPage: React.FC<{ cluster: string }> = ({ cluster }) => {
  const { report, loading, error, updatedAt } = useIssues(cluster);
  const [query, setQuery] = useState('');
  const [namespace, setNamespace] = useState('');
  const [hidden, setHidden] = useState<Set<HealthSeverity>>(new Set());
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 15_000);
    return () => clearInterval(id);
  }, []);

  const issues = report?.issues || [];
  const namespaces = useMemo(
    () => Array.from(new Set(issues.map((i) => i.resource.namespace || '').filter(Boolean))).sort(),
    [issues],
  );
  const visible = useMemo(() => issues.filter((i) => matchIssue(i, query, namespace)), [issues, query, namespace]);
  const groups = groupBySeverity(visible);
  const summary = report?.summary;

  const toggle = (s: HealthSeverity) =>
    setHidden((prev) => {
      const next = new Set(prev);
      if (next.has(s)) next.delete(s);
      else next.add(s);
      return next;
    });

  return (
    <div className="health-page">
      <div className="health-toolbar">
        <div className="health-toolbar-title">
          <h2>Issues</h2>
          <span className="health-toolbar-sub">
            {summary ? `${summary.total} open · ${summary.critical} critical · ${summary.warning} warning` : cluster}
            {updatedAt > 0 && ' · live'}
          </span>
        </div>
        <div className="health-toolbar-controls">
          <label className="ap-search health-search">
            <MagnifyingGlassIcon />
            <input type="text" placeholder="Filter issues" value={query}
              onChange={(e) => setQuery(e.target.value)} aria-label="Filter issues" />
          </label>
          <select className="ap-select health-select" value={namespace} onChange={(e) => setNamespace(e.target.value)} aria-label="Namespace">
            <option value="">All namespaces</option>
            {namespaces.map((n) => <option key={n} value={n}>{n}</option>)}
          </select>
          <button type="button" className="ap-btn" onClick={() => void openHealthPage(cluster, 'audit')}>Audit</button>
          <button type="button" className="ap-icon-btn" title="Refresh" aria-label="Refresh" onClick={() => void refreshIssues(cluster, true)}>
            <ReloadIcon className={loading ? 'health-spin' : undefined} />
          </button>
        </div>
      </div>

      <UnavailableNote kinds={report?.unavailable} />

      <div className="health-body">
        {error && !report && (
          <HealthEmpty title="Could not load issues" detail={error}
            action={<button type="button" className="ap-btn" onClick={() => void refreshIssues(cluster, true)}>Try again</button>} />
        )}
        {!error && !report && (
          <div className="health-skeleton">
            {[0, 1, 2, 3].map((i) => <div key={i} className="ap-skeleton health-skeleton-row" />)}
          </div>
        )}
        {report && issues.length === 0 && (
          <HealthEmpty title="Nothing is broken right now" detail="Pods, workloads, Services, Ingresses, volumes, nodes, autoscalers and jobs all look healthy." />
        )}
        {report && issues.length > 0 && visible.length === 0 && <HealthEmpty title="No issue matches the filter" />}
        {groups.map((g) => (
          <section key={g.severity} className="health-group">
            <button type="button" className="health-group-label" onClick={() => toggle(g.severity)} aria-expanded={!hidden.has(g.severity)}>
              <SeverityDot severity={g.severity} />
              <span>{SEVERITY_LABEL[g.severity]}</span>
              <span className="health-group-count ap-tnum">{g.items.length}</span>
            </button>
            {!hidden.has(g.severity) && (
              <div className="ap-card health-card">
                {g.items.map((i) => <IssueRow key={i.id} cluster={cluster} issue={i} now={now} />)}
              </div>
            )}
          </section>
        ))}
      </div>
    </div>
  );
};

export default IssuesPage;
