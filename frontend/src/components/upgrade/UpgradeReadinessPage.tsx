import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { getUpgradeReport } from '../../services/api/upgrade';
import type { UpgradeAPIFinding, UpgradeAffectedResource, UpgradeReport } from '../../types/upgrade';
import ResourceLink from '../ResourceLink';
import NodeLink from '../NodeLink';
import { useStore } from '../../store';
import { SEVERITY_LABEL, SEVERITY_TONE, SeverityFilter, matchesQuery, sourceCounts, sourceLabel } from './upgradeView';
import './UpgradeReadiness.css';

interface Props {
  cluster: string;
}

const FILTERS: { id: SeverityFilter; label: string }[] = [
  { id: 'all', label: 'All' },
  { id: 'blocker', label: 'Blockers' },
  { id: 'warning', label: 'Warnings' },
  { id: 'info', label: 'Info' },
];

const SOURCE_TONE: Record<UpgradeAffectedResource['source'], string> = {
  live: 'ap-badge--purple',
  'last-applied': '',
  helm: 'ap-badge--info',
};

const UpgradeReadinessPage: React.FC<Props> = ({ cluster }) => {
  const [target, setTarget] = useState('');
  const [report, setReport] = useState<UpgradeReport | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [filter, setFilter] = useState<SeverityFilter>('all');
  const [query, setQuery] = useState('');
  const seq = useRef(0);

  const load = useCallback(async (refresh = false) => {
    const id = ++seq.current;
    setLoading(true);
    setError(null);
    try {
      const rep = await getUpgradeReport(cluster, target, refresh);
      if (id !== seq.current) return;
      setReport(rep);
      if (!target) setTarget(rep.targetVersion);
    } catch (err: any) {
      if (id !== seq.current) return;
      setError(err?.response?.data?.error || err?.message || 'Could not compute the upgrade report');
    } finally {
      if (id === seq.current) setLoading(false);
    }
  }, [cluster, target]);

  useEffect(() => { void load(false); }, [load]);

  const apis = useMemo(
    () => (report?.apis || []).filter((f) => (filter === 'all' || f.severity === filter) && matchesQuery(f, query)),
    [report, filter, query],
  );
  const nodes = useMemo(() => (report?.nodes || []).filter((n) => filter === 'all' || n.severity === filter), [report, filter]);
  const crds = useMemo(() => (report?.crds || []).filter((c) => filter === 'all' || c.severity === filter), [report, filter]);

  const targets = report?.availableTargets || [];

  return (
    <div className="upgrade-page">
      <header className="upgrade-toolbar">
        <div className="upgrade-toolbar-title">
          <h2>Upgrade readiness</h2>
          <span>Deprecated APIs, Helm manifests, node skew and CRD versions</span>
        </div>
        <div className="upgrade-toolbar-controls">
          <span className="upgrade-version ap-tnum" title={report?.serverGitVersion}>{report?.currentVersion || '…'}</span>
          <span className="upgrade-arrow" aria-hidden="true">→</span>
          <select
            className="ap-select"
            aria-label="Target version"
            value={target}
            disabled={!targets.length}
            onChange={(e) => setTarget(e.target.value)}
          >
            {!targets.length && <option value={target}>{target || 'No newer release'}</option>}
            {targets.map((v) => <option key={v} value={v}>{v}</option>)}
          </select>
          <button type="button" className="ap-btn ap-btn--sm" onClick={() => void load(true)} disabled={loading}>
            {loading ? 'Checking…' : 'Recheck'}
          </button>
        </div>
      </header>

      <div className="upgrade-body">
        {error && !report && (
          <div className="ap-load-error">
            <span>{error}</span>
            <button type="button" className="ap-btn" onClick={() => void load(true)}>Try again</button>
          </div>
        )}
        {!report && !error && (
          <div className="upgrade-loading">
            <span className="ap-spinner" aria-hidden="true" />
            <span>Scanning APIs, annotations and Helm releases…</span>
          </div>
        )}
        {report && (
          <>
            {error && <div className="upgrade-inline-error">{error}</div>}
            <section className={`upgrade-summary upgrade-summary--${report.summary.verdict}`} aria-label="Readiness summary">
              <div className="upgrade-verdict">
                <span className={`ap-glyph ap-glyph--${report.summary.verdict === 'blocked' ? 'danger' : report.summary.verdict === 'warnings' ? 'warning' : 'success'}`} aria-hidden="true">
                  {report.summary.verdict === 'ready' ? '✓' : '!'}
                </span>
                <div>
                  <div className="upgrade-verdict-title">
                    {report.summary.verdict === 'blocked'
                      ? `Not ready for ${report.targetVersion}`
                      : report.summary.verdict === 'warnings'
                        ? `Ready for ${report.targetVersion}, with warnings`
                        : `Ready for ${report.targetVersion}`}
                  </div>
                  <div className="upgrade-verdict-sub">
                    {report.path && report.path.length > 1 ? `Path ${report.currentVersion} → ${report.path.join(' → ')}` : `${report.currentVersion} → ${report.targetVersion}`}
                    {report.nodeSummary.total > 0 && ` · ${report.nodeSummary.total} nodes, kubelet ${report.nodeSummary.minKubelet === report.nodeSummary.maxKubelet ? report.nodeSummary.minKubelet : `${report.nodeSummary.minKubelet}–${report.nodeSummary.maxKubelet}`}`}
                  </div>
                </div>
              </div>
              <div className="upgrade-stats">
                <button type="button" className="upgrade-stat" data-tone="danger" aria-pressed={filter === 'blocker'} onClick={() => setFilter(filter === 'blocker' ? 'all' : 'blocker')}>
                  <span className="upgrade-stat-value ap-tnum">{report.summary.blockers}</span><span>Blockers</span>
                </button>
                <button type="button" className="upgrade-stat" data-tone="warning" aria-pressed={filter === 'warning'} onClick={() => setFilter(filter === 'warning' ? 'all' : 'warning')}>
                  <span className="upgrade-stat-value ap-tnum">{report.summary.warnings}</span><span>Warnings</span>
                </button>
                <div className="upgrade-stat" data-tone="success" title="Deprecated APIs in this window that nothing uses">
                  <span className="upgrade-stat-value ap-tnum">{report.summary.ok}</span><span>APIs OK</span>
                </div>
              </div>
            </section>

            {report.notes.length > 0 && (
              <ul className="upgrade-notes">
                {report.notes.map((n) => <li key={n}>{n}</li>)}
              </ul>
            )}

            <div className="upgrade-filters">
              <div className="ap-segmented ap-segmented--sm" role="tablist" aria-label="Severity">
                {FILTERS.map((f) => (
                  <button key={f.id} type="button" role="tab" aria-selected={filter === f.id} onClick={() => setFilter(f.id)}>{f.label}</button>
                ))}
              </div>
              <input
                className="ap-input ap-input--sm upgrade-search"
                placeholder="Filter by API, kind or resource"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                aria-label="Filter findings"
              />
            </div>

            <div className="ap-group-label">Deprecated APIs</div>
            {apis.length === 0 ? (
              <div className="ap-card upgrade-empty">
                {report.apis.length === 0
                  ? `Nothing in this cluster uses an API deprecated or removed by ${report.targetVersion}.`
                  : 'No API findings match the filter.'}
              </div>
            ) : (
              apis.map((f) => <APIGroup key={`${f.apiVersion}|${f.kind}`} finding={f} cluster={cluster} />)
            )}

            {(nodes.length > 0 || filter === 'all') && (
              <>
                <div className="ap-group-label">Node version skew</div>
                <div className="ap-card">
                  {nodes.length === 0 ? (
                    <div className="upgrade-empty">
                      {report.nodeSummary.total
                        ? `All ${report.nodeSummary.total} kubelets stay within the supported skew of ${report.targetVersion}.`
                        : 'Node versions could not be read.'}
                    </div>
                  ) : nodes.map((n) => (
                    <div key={n.node} className="ap-card-row upgrade-row">
                      <span className={`ap-badge ap-badge--sm ap-badge--${SEVERITY_TONE[n.severity]}`}>{SEVERITY_LABEL[n.severity]}</span>
                      <span className="upgrade-row-name"><NodeLink nodeName={n.node} /></span>
                      <span className="upgrade-row-meta ap-mono">{n.kubeletVersion}</span>
                      <span className="upgrade-row-msg">{n.message}</span>
                    </div>
                  ))}
                </div>
              </>
            )}

            {crds.length > 0 && (
              <>
                <div className="ap-group-label">Custom resource versions</div>
                <div className="ap-card">
                  {crds.map((c) => (
                    <div key={`${c.name}|${c.version}|${c.message}`} className="ap-card-row upgrade-row">
                      <span className={`ap-badge ap-badge--sm ap-badge--${SEVERITY_TONE[c.severity]}`}>{SEVERITY_LABEL[c.severity]}</span>
                      <span className="upgrade-row-name">
                        <ResourceLink cluster={cluster} apiVersion="apiextensions.k8s.io/v1" kind="CustomResourceDefinition" name={c.name} openInDetailTab />
                      </span>
                      <span className="upgrade-row-meta ap-mono">{c.version}</span>
                      <span className="upgrade-row-msg">{c.message}</span>
                    </div>
                  ))}
                </div>
              </>
            )}

            <div className="ap-group-label">Evidence checked</div>
            <div className="ap-card">
              {report.checks.map((c) => (
                <div key={c.id} className="ap-card-row upgrade-check">
                  <span className={`ap-dot ap-dot--${c.status === 'ok' ? 'success' : c.status === 'partial' ? 'warning' : 'muted'}`} aria-hidden="true" />
                  <span>{c.label}</span>
                  <span className="ap-card-row-value">{c.detail || (c.status === 'ok' ? 'Checked' : 'Unavailable')}</span>
                </div>
              ))}
            </div>
            <p className="upgrade-footnote">
              Deprecation table reviewed {report.catalogReviewed}, from the{' '}
              <a href={report.catalogSource} target="_blank" rel="noreferrer">Kubernetes deprecation guide</a>.
              {' '}Evidence collected {new Date(report.collectedAt).toLocaleTimeString()}.
            </p>
          </>
        )}
      </div>
    </div>
  );
};

const APIGroup: React.FC<{ finding: UpgradeAPIFinding; cluster: string }> = ({ finding: f, cluster }) => {
  const counts = sourceCounts(f.resources);
  return (
    <section className="ap-card upgrade-api" data-severity={f.severity}>
      <header className="upgrade-api-header">
        <span className={`ap-badge ap-badge--sm ap-badge--${SEVERITY_TONE[f.severity]}`}>{SEVERITY_LABEL[f.severity]}</span>
        <span className="upgrade-api-title">
          <span className="upgrade-api-kind">{f.kind}</span>
          <span className="ap-mono">{f.apiVersion}</span>
          <span className="upgrade-arrow" aria-hidden="true">→</span>
          {f.replacement ? <span className="ap-mono upgrade-api-replacement">{f.replacement}</span> : <span className="upgrade-api-none">no replacement</span>}
        </span>
        <span className="upgrade-api-release ap-tnum">
          {f.removedIn ? `${f.alreadyRemoved ? 'Removed' : 'Removed in'} ${f.removedIn}` : `Deprecated ${f.deprecatedIn}`}
        </span>
      </header>
      <div className="upgrade-api-message">{f.message}</div>
      <div className="upgrade-api-hint"><span className="upgrade-api-hint-label">Migration</span>{f.hint}</div>
      <div className="upgrade-api-sources">
        {f.liveRequests && <span className="ap-badge ap-badge--sm ap-badge--purple" title="A client requested this API since the API server started">Requested live</span>}
        {counts.helm > 0 && <span className="ap-badge ap-badge--sm ap-badge--info">{counts.helm} in Helm releases</span>}
        {counts['last-applied'] > 0 && <span className="ap-badge ap-badge--sm">{counts['last-applied']} in last-applied</span>}
        {counts.live > 0 && <span className="ap-badge ap-badge--sm ap-badge--purple">{counts.live} live objects</span>}
        {f.served && <span className="upgrade-api-served">Still served by this cluster</span>}
      </div>
      {f.liveRequests && f.resources.length === 0 && (
        <div className="upgrade-row upgrade-row--note">
          The API server reports requests, but no stored manifest uses it. Find the client in the audit log (it sends a deprecation warning on every call).
        </div>
      )}
      {f.resources.map((r) => (
        <div key={`${r.source}|${r.helmNamespace}|${r.helmRelease}|${r.namespace}|${r.name}`} className="ap-card-row upgrade-row">
          <span className="upgrade-row-name">
            {r.liveApiVersion ? (
              <ResourceLink cluster={cluster} apiVersion={r.liveApiVersion} kind={r.kind} name={r.name} namespace={r.namespace} openInDetailTab />
            ) : (
              <span title="Not in the cluster under a served version">{r.name}</span>
            )}
          </span>
          <span className="upgrade-row-meta ap-mono">{r.namespace || 'cluster-scoped'}</span>
          <span className="upgrade-row-source">
            {r.source === 'helm' && r.helmRelease ? (
              <HelmReleaseLink cluster={cluster} name={r.helmRelease} namespace={r.helmNamespace || ''} />
            ) : (
              <span className={`ap-badge ap-badge--sm ${SOURCE_TONE[r.source]}`}>{sourceLabel(r)}</span>
            )}
          </span>
          <span className={`ap-dot ap-dot--${SEVERITY_TONE[r.severity] === 'info' ? 'info' : SEVERITY_TONE[r.severity]}`} title={SEVERITY_LABEL[r.severity]} aria-label={SEVERITY_LABEL[r.severity]} />
        </div>
      ))}
    </section>
  );
};

const HelmReleaseLink: React.FC<{ cluster: string; name: string; namespace: string }> = ({ cluster, name, namespace }) => {
  const openDetailTab = useStore((s) => s.openDetailTab);
  return (
    <button
      type="button"
      className="ap-badge ap-badge--sm ap-badge--info upgrade-helm-link"
      title={`Open Helm release ${namespace}/${name}`}
      onClick={() => openDetailTab({ kind: 'HelmRelease' }, { name, namespace, kind: 'HelmRelease', apiVersion: 'helm.sh/v1' }, cluster)}
    >
      Helm release {name}
    </button>
  );
};

export default UpgradeReadinessPage;
