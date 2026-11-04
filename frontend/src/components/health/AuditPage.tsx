import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { ReloadIcon } from '@radix-ui/react-icons';
import { addHealthIgnore, getHealthAudit, listHealthIgnores, removeHealthIgnore } from '../../services/api/health';
import type { HealthAuditReport, HealthCategory, HealthCheckResult, HealthFinding, HealthIgnore } from '../../types/health';
import { findingsByResource, ignoreFor, refKey, SEVERITY_BADGE, SEVERITY_LABEL } from './healthView';
import { FindingRow } from './FindingRow';
import AIButton from '../ai/AIButton';
import { auditBulkFixSeed } from '../ai/aiContext';
import { HealthEmpty, RefLink, ScoreRing, SeverityDot, UnavailableNote } from './HealthParts';
import './Health.css';

type View = 'checks' | 'resources' | 'namespaces';

const CATEGORY_LABEL: Record<HealthCategory, string> = { security: 'Security', reliability: 'Reliability', efficiency: 'Efficiency' };

export function useAudit(cluster: string) {
  const [report, setReport] = useState<HealthAuditReport | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const load = useCallback(async (opts: { refresh?: boolean; includeSystem?: boolean } = {}) => {
    if (!cluster) return;
    setLoading(true);
    try {
      setReport(await getHealthAudit(cluster, opts));
      setError(null);
    } catch (e: any) {
      setError(e?.response?.data?.error || e?.message || 'Failed to run the audit');
    } finally {
      setLoading(false);
    }
  }, [cluster]);
  return { report, error, loading, load };
}

const AuditPage: React.FC<{ cluster: string }> = ({ cluster }) => {
  const { report, error, loading, load } = useAudit(cluster);
  const [view, setView] = useState<View>('checks');
  const [includeSystem, setIncludeSystem] = useState(false);
  const [selectedCheck, setSelectedCheck] = useState<string | null>(null);
  const [ignores, setIgnores] = useState<HealthIgnore[]>([]);
  const [showIgnored, setShowIgnored] = useState(false);

  useEffect(() => { void load({ includeSystem }); }, [load, includeSystem]);
  useEffect(() => { listHealthIgnores(cluster).then(setIgnores).catch(() => {}); }, [cluster]);

  const checkById = useMemo(() => new Map((report?.checks || []).map((c) => [c.id, c])), [report]);
  const findings = useMemo(
    () => (report?.findings || []).filter((f) => (showIgnored || !f.ignored) && (!selectedCheck || f.checkId === selectedCheck)),
    [report, showIgnored, selectedCheck],
  );
  const byResource = useMemo(() => findingsByResource(findings), [findings]);

  const ignore = async (f: HealthFinding, scope: 'resource' | 'namespace') => {
    setIgnores(await addHealthIgnore(ignoreFor(cluster, f, scope)));
    await load({ includeSystem });
  };
  const unignore = async (ig: HealthIgnore) => {
    setIgnores(await removeHealthIgnore(ig));
    await load({ includeSystem });
  };

  return (
    <div className="health-page">
      <div className="health-toolbar">
        <div className="health-toolbar-title">
          <h2>Audit</h2>
          <span className="health-toolbar-sub">
            {report ? `${report.resources} workloads · ${report.findings.filter((f) => !f.ignored).length} findings` : cluster}
          </span>
        </div>
        <div className="health-toolbar-controls">
          <div className="ap-segmented" role="tablist">
            {(['checks', 'resources', 'namespaces'] as View[]).map((v) => (
              <button key={v} type="button" role="tab" data-active={view === v} className={view === v ? 'is-active' : undefined} onClick={() => setView(v)}>
                {v === 'checks' ? 'Checks' : v === 'resources' ? 'Resources' : 'Namespaces'}
              </button>
            ))}
          </div>
          <label className="ap-toggle-label health-check-label">
            <input type="checkbox" className="ap-checkbox" checked={includeSystem} onChange={(e) => setIncludeSystem(e.target.checked)} />
            System namespaces
          </label>
          <label className="ap-toggle-label health-check-label">
            <input type="checkbox" className="ap-checkbox" checked={showIgnored} onChange={(e) => setShowIgnored(e.target.checked)} />
            Show ignored
          </label>
          <button type="button" className="ap-icon-btn" title="Re-run audit" aria-label="Re-run audit" onClick={() => void load({ refresh: true, includeSystem })}>
            <ReloadIcon className={loading ? 'health-spin' : undefined} />
          </button>
        </div>
      </div>

      <UnavailableNote kinds={report?.unavailable} />

      <div className="health-body">
        {error && !report && (
          <HealthEmpty title="Could not run the audit" detail={error}
            action={<button type="button" className="ap-btn" onClick={() => void load({ refresh: true, includeSystem })}>Try again</button>} />
        )}
        {!error && !report && (
          <div className="health-skeleton">{[0, 1, 2].map((i) => <div key={i} className="ap-skeleton health-skeleton-row" />)}</div>
        )}
        {report && (
          <>
            <div className="health-scores">
              <div className="ap-card health-score-card health-score-card--main">
                <ScoreRing score={report.score} size={84} label="Cluster score" />
                <div>
                  <div className="health-score-title">Cluster score</div>
                  <div className="health-score-sub">{report.resources} workloads in {report.namespaces.length} namespaces</div>
                </div>
              </div>
              {(Object.keys(CATEGORY_LABEL) as HealthCategory[]).map((cat) => report.categories[cat] !== undefined && (
                <div key={cat} className="ap-card health-score-card">
                  <ScoreRing score={report.categories[cat]!} size={52} label={CATEGORY_LABEL[cat]} />
                  <div className="health-score-title">{CATEGORY_LABEL[cat]}</div>
                </div>
              ))}
            </div>

            {report.resources === 0 && <HealthEmpty title="No workloads to audit" detail="Deployments, StatefulSets, DaemonSets, Jobs and CronJobs are audited." />}

            {view === 'checks' && report.resources > 0 && (
              <ChecksView checks={report.checks} selected={selectedCheck} onSelect={setSelectedCheck} />
            )}
            {view === 'namespaces' && report.resources > 0 && (
              <div className="ap-card health-card">
                {report.namespaces.map((n) => (
                  <div key={n.namespace} className="ap-card-row health-ns-row">
                    <ScoreRing score={n.score} size={30} label={n.namespace} />
                    <span className="health-ns-name">{n.namespace}</span>
                    <span className="ap-card-row-value">
                      {n.resources} workloads
                      {n.critical > 0 && <span className="ap-badge ap-badge--sm ap-badge--danger">{n.critical} critical</span>}
                      {n.warning > 0 && <span className="ap-badge ap-badge--sm ap-badge--warning">{n.warning} warning</span>}
                    </span>
                  </div>
                ))}
              </div>
            )}
            {(view === 'resources' || (view === 'checks' && selectedCheck)) && report.resources > 0 && (
              <>
                <div className="ap-group-label health-section-label">
                  {selectedCheck ? `Resources failing "${checkById.get(selectedCheck)?.title}"` : 'Findings by resource'}
                  {selectedCheck && <button type="button" className="ap-btn ap-btn--ghost ap-btn--sm" onClick={() => setSelectedCheck(null)}>Show all</button>}
                  {selectedCheck && checkById.get(selectedCheck) && findings.some((f) => !f.ignored) && (
                    <AIButton label="Fix all failing with AI" title={`Fix every resource failing "${checkById.get(selectedCheck)?.title}"`}
                      seed={() => auditBulkFixSeed(cluster, checkById.get(selectedCheck)!, findings.filter((f) => !f.ignored))} />
                  )}
                </div>
                {byResource.length === 0 && <HealthEmpty title="No findings" />}
                {byResource.slice(0, 200).map((g) => (
                  <div key={refKey(g.resource)} className="ap-card health-card health-res-card">
                    <div className="health-res-head">
                      <SeverityDot severity={g.worst} />
                      <RefLink cluster={cluster} r={g.resource} />
                      <span className="health-issue-ns">{g.resource.namespace}</span>
                    </div>
                    {g.findings.map((f, i) => (
                      <FindingRow key={`${f.checkId}-${f.container}-${i}`} cluster={cluster} f={f} check={checkById.get(f.checkId)}
                        onIgnore={ignore} />
                    ))}
                  </div>
                ))}
              </>
            )}

            {ignores.length > 0 && (
              <>
                <div className="ap-group-label health-section-label">Ignored checks</div>
                <div className="ap-card health-card">
                  {ignores.map((ig) => (
                    <div key={`${ig.checkId}/${ig.namespace}/${ig.kind}/${ig.name}`} className="ap-card-row">
                      <span>{checkById.get(ig.checkId)?.title || ig.checkId}</span>
                      <span className="ap-card-row-value">
                        {ig.name ? `${ig.kind} ${ig.namespace}/${ig.name}` : ig.namespace ? `namespace ${ig.namespace}` : 'whole cluster'}
                        <button type="button" className="ap-btn ap-btn--ghost ap-btn--sm" onClick={() => void unignore(ig)}>Restore</button>
                      </span>
                    </div>
                  ))}
                </div>
              </>
            )}
          </>
        )}
      </div>
    </div>
  );
};

const ChecksView: React.FC<{ checks: HealthCheckResult[]; selected: string | null; onSelect: (id: string | null) => void }> = ({ checks, selected, onSelect }) => (
  <div className="ap-card health-card">
    {checks.map((c) => {
      const total = c.failed + c.passed;
      const pct = total ? Math.round((c.passed / total) * 100) : 100;
      return (
        <div key={c.id} className={`ap-card-row is-clickable health-check-row${selected === c.id ? ' is-selected' : ''}`}
          onClick={() => onSelect(selected === c.id ? null : c.id)}>
          <span className={`ap-badge ap-badge--sm ${SEVERITY_BADGE[c.severity]}`}>{SEVERITY_LABEL[c.severity]}</span>
          <div className="health-check-text">
            <div className="health-check-title">{c.title}</div>
            <div className="health-check-expl">{c.explanation}</div>
          </div>
          <div className="health-check-bar" title={`${c.passed} pass, ${c.failed} fail${c.ignored ? `, ${c.ignored} ignored` : ''}`}>
            <div className="health-check-bar-fill" style={{ width: `${pct}%` }} />
          </div>
          <span className="ap-card-row-value health-check-count">{c.failed > 0 ? `${c.failed} failing` : 'All pass'}</span>
        </div>
      );
    })}
  </div>
);

export default AuditPage;
