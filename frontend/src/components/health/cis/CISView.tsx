import React, {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import { DownloadIcon, ReloadIcon } from '@radix-ui/react-icons';
import { getCisReport, setCisOverride } from '../../../services/api/cis';
import type {
  CisControl,
  CisReport,
  CisStatus,
  ProviderInfo,
} from '../../../types/cis';
import AnchoredPopover from '../../common/AnchoredPopover';
import ExpandIcon from '../../icons/ExpandIcon';
import AIButton from '../../ai/AIButton';
import { FixSnippet, HealthEmpty, RefLink, ScoreRing } from '../HealthParts';
import {
  EMPTY_FILTER,
  STATUS_BADGE,
  STATUS_LABEL,
  STATUS_ORDER,
  SOURCE_LABEL,
  agoLabel,
  cisExplainSeed,
  cisFixSeed,
  countOf,
  exportFileName,
  quietGroup,
  filterControls,
  groupControls,
  providerChipLabel,
  reportCSV,
  reportJSON,
  sourceLabel,
  type CisFilter,
} from './cisModel';
import '../Health.css';
import './CIS.css';

const PROVIDER_NAMES: Record<string, string> = {
  eks: 'EKS',
  gke: 'GKE',
  aks: 'AKS',
  openshift: 'OpenShift',
  k3s: 'k3s',
  rke2: 'RKE2',
  kind: 'kind',
  minikube: 'minikube',
  'docker-desktop': 'Docker Desktop',
  kubeadm: 'kubeadm (self-managed)',
};

function download(name: string, text: string, type: string) {
  const url = URL.createObjectURL(new Blob([text], { type }));
  const a = document.createElement('a');
  a.href = url;
  a.download = name;
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

export function useCis(cluster: string) {
  const [report, setReport] = useState<CisReport | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const seq = useRef(0);
  const load = useCallback(
    async (opts: { profile?: string; refresh?: boolean } = {}) => {
      if (!cluster) return;
      const n = ++seq.current;
      setLoading(true);
      try {
        const r = await getCisReport(cluster, opts);
        if (n === seq.current) {
          setReport(r);
          setError(null);
        }
      } catch (e: any) {
        if (n === seq.current)
          setError(
            e?.response?.data?.error ||
              e?.message ||
              'Failed to run the CIS benchmark',
          );
      } finally {
        if (n === seq.current) setLoading(false);
      }
    },
    [cluster],
  );
  return { report, error, loading, load };
}

/** The provider chip: detection summary, evidence popover and manual override. */
const ProviderChip: React.FC<{
  provider: ProviderInfo;
  onOverride: (p: string) => void;
}> = ({ provider, onOverride }) => {
  const [open, setOpen] = useState(false);
  const btn = useRef<HTMLButtonElement>(null);
  const pop = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      const t = e.target as Node;
      if (btn.current?.contains(t) || pop.current?.contains(t)) return;
      setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false);
    };
    document.addEventListener('mousedown', onDown);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDown);
      document.removeEventListener('keydown', onKey);
    };
  }, [open]);
  const conf = Math.round(provider.confidence * 100);
  return (
    <>
      <button
        ref={btn}
        type="button"
        className="cis-provider-chip"
        aria-expanded={open}
        aria-haspopup="dialog"
        onClick={() => setOpen((o) => !o)}
        title="How the provider was detected"
      >
        <span
          className={`ap-dot ${provider.overridden ? 'ap-dot--info' : conf >= 70 ? 'ap-dot--success' : 'ap-dot--warning'}`}
        />
        {providerChipLabel(provider)}
        {provider.overridden && (
          <span className="cis-provider-manual">set by you</span>
        )}
      </button>
      <AnchoredPopover
        anchor={btn}
        open={open}
        role="dialog"
        aria-label="Provider detection"
        minWidth={340}
        maxHeight={420}
        className="cis-provider-pop"
      >
        <div ref={pop} className="cis-provider-pop-body">
          <div className="cis-provider-pop-head">
            <div className="cis-provider-pop-title">
              {provider.distribution}
              {provider.version ? ` ${provider.version}` : ''}
            </div>
            <div className="cis-provider-pop-sub">
              {provider.overridden
                ? 'Chosen by you'
                : `Detected with ${conf}% confidence`}
              {provider.gitVersion ? ` · ${provider.gitVersion}` : ''}
            </div>
          </div>
          <div className="ap-group-label cis-pop-label">Evidence</div>
          {provider.evidence.length === 0 && (
            <div className="cis-pop-empty">No signals could be read.</div>
          )}
          {provider.evidence.map((e, i) => (
            <div key={i} className="cis-evidence-row">
              <span className="cis-evidence-signal">{e.signal}</span>
              <span className="cis-evidence-value ap-mono" title={e.value}>
                {e.value}
              </span>
              <span className="ap-badge ap-badge--sm">
                {PROVIDER_NAMES[e.provider]?.split(' ')[0] || e.provider}
              </span>
            </div>
          ))}
          <div className="cis-provider-facts">
            <span>
              {provider.managedControlPlane
                ? 'Control plane run by the provider'
                : provider.controlPlaneVisible
                  ? 'Control-plane static pods visible'
                  : 'Control plane not visible'}
            </span>
          </div>
          <label className="cis-override">
            <span>Provider</span>
            <select
              className="ap-select"
              value={provider.overridden ? provider.provider : ''}
              onChange={(e) => {
                onOverride(e.target.value);
                setOpen(false);
              }}
            >
              <option value="">Detect automatically</option>
              {Object.entries(PROVIDER_NAMES).map(([id, name]) => (
                <option key={id} value={id}>
                  {name}
                </option>
              ))}
            </select>
          </label>
        </div>
      </AnchoredPopover>
    </>
  );
};

const StatusPill: React.FC<{ status: CisStatus }> = ({ status }) => (
  <span
    className={`ap-badge ap-badge--sm cis-status ${STATUS_BADGE[status]}`}
    data-status={status}
  >
    {STATUS_LABEL[status]}
  </span>
);

const ControlRow: React.FC<{
  cluster: string;
  c: CisControl;
  report: CisReport;
}> = ({ cluster, c, report }) => {
  const [open, setOpen] = useState(false);
  const src = sourceLabel(c);
  const target = c.resources?.find((r) => r.kind !== 'Namespace');
  const actionable =
    c.status === 'automated-fail' ||
    c.status === 'manual' ||
    c.status === 'error';
  const sub = c.message || c.manualCheck;
  return (
    <div
      className={`cis-control${open ? ' is-open' : ''}`}
      data-status={c.status}
    >
      <div
        className="cis-control-main"
        role="button"
        tabIndex={0}
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
        onKeyDown={(e) => {
          if (e.key === 'Enter' || e.key === ' ') {
            e.preventDefault();
            setOpen((o) => !o);
          }
        }}
      >
        <span className="cis-control-id ap-tnum">{c.id}</span>
        <div className="cis-control-text">
          <span className="cis-control-title">{c.title}</span>
          {sub && (
            <span className="cis-control-msg" title={open ? undefined : sub}>
              {sub}
            </span>
          )}
        </div>
        {src && <span className="cis-control-src">{src}</span>}
        {c.disagreement && (
          <span
            className="ap-badge ap-badge--sm ap-badge--warning"
            title="A scanner disagrees with Kanivet's reading"
          >
            disputed
          </span>
        )}
        {!c.scored && (
          <span
            className="cis-control-scored"
            title="CIS marks this control as a manual assessment (not scored)"
          >
            not scored
          </span>
        )}
        <StatusPill status={c.status} />
        <span
          className="cis-control-ai"
          onClick={(e) => e.stopPropagation()}
          onKeyDown={(e) => e.stopPropagation()}
        >
          <AIButton
            variant="icon"
            label="Explain with AI"
            seed={() =>
              cisExplainSeed(cluster, c, report.provider, report.profile.title)
            }
          />
          {actionable && (
            <AIButton
              label="Fix with AI"
              seed={() =>
                cisFixSeed(cluster, c, report.provider, report.profile.title)
              }
            />
          )}
        </span>
        <span className="health-finding-chevron" aria-hidden="true">
          <ExpandIcon expanded={open} />
        </span>
      </div>
      {open && (
        <div className="cis-control-more">
          <p className="health-check-expl">{c.description}</p>
          {c.manualCheck && c.message && (
            <div className="cis-manual">
              <span className="cis-more-label">Check by hand</span>
              {c.manualCheck}
            </div>
          )}
          {(c.values?.length || 0) > 0 && (
            <div className="cis-evidence">
              <div className="cis-more-label">Flags</div>
              {c.values!.map((v, i) => (
                <div key={i} className="cis-value-row">
                  <span
                    className={`ap-dot ${v.ok ? 'ap-dot--success' : 'ap-dot--danger'}`}
                  />
                  <span className="ap-mono cis-value-name">{v.name}</span>
                  <span className="ap-mono cis-value-val">{v.value}</span>
                  {v.source && (
                    <RefLink
                      cluster={cluster}
                      r={{
                        apiVersion: 'v1',
                        kind: 'Pod',
                        namespace: 'kube-system',
                        name: v.source,
                      }}
                      showKind={false}
                    />
                  )}
                </div>
              ))}
            </div>
          )}
          {(c.nodeGroups?.length || 0) > 0 && (
            <div className="cis-evidence">
              <div className="cis-more-label">Kubelet by node pool</div>
              {c.nodeGroups!.map((g, i) => (
                <div key={i} className="cis-pool-row">
                  <span
                    className={`ap-dot ${g.error ? 'ap-dot--warning' : g.ok ? 'ap-dot--success' : 'ap-dot--danger'}`}
                  />
                  <span className="cis-pool-name">{g.pool}</span>
                  <span className="cis-pool-count ap-tnum">
                    {g.nodes.length} of {g.totalNodes} read
                  </span>
                  <span className="ap-mono cis-value-val">
                    {g.error || g.value}
                  </span>
                  <span className="cis-pool-nodes">
                    {g.nodes.slice(0, 3).map((n) => (
                      <RefLink
                        key={n}
                        cluster={cluster}
                        r={{ apiVersion: 'v1', kind: 'Node', name: n }}
                        showKind={false}
                      />
                    ))}
                  </span>
                </div>
              ))}
            </div>
          )}
          {(c.resources?.length || 0) > 0 && (
            <div className="cis-evidence">
              <div className="cis-more-label">
                {c.status === 'automated-pass' ? 'Related' : 'Affected'} (
                {c.resourcesTotal || c.resources!.length})
              </div>
              <div className="cis-resources">
                {c.resources!.map((r) => (
                  <RefLink
                    key={`${r.kind}/${r.namespace}/${r.name}`}
                    cluster={cluster}
                    r={r}
                  />
                ))}
                {(c.resourcesTotal || 0) > c.resources!.length && (
                  <span className="ap-muted">
                    and {c.resourcesTotal! - c.resources!.length} more
                  </span>
                )}
              </div>
            </div>
          )}
          {(c.scanners?.length || 0) > 0 && (
            <div className="cis-evidence">
              <div className="cis-more-label">Scanners</div>
              {c.scanners!.map((s, i) => (
                <div key={i} className="cis-value-row">
                  <StatusPill status={s.status} />
                  <span>{SOURCE_LABEL[s.source] || s.source}</span>
                  <span className="cis-value-val">{s.detail}</span>
                  <span className="cis-control-src">
                    {agoLabel(s.scannedAt)}
                  </span>
                </div>
              ))}
            </div>
          )}
          {c.remediation &&
            c.status !== 'automated-pass' &&
            c.status !== 'provider-responsibility' && (
              <div className="cis-remediation">
                <div className="cis-more-label">Remediation</div>
                <p className="health-check-expl">{c.remediation}</p>
                {c.snippet && (
                  <FixSnippet
                    cluster={cluster}
                    fix={c.snippet}
                    target={
                      c.nodeGroups?.length || c.values?.length
                        ? undefined
                        : target
                    }
                    path={
                      c.nodeGroups?.length
                        ? 'the kubelet configuration file'
                        : c.values?.length
                          ? 'the static pod command'
                          : undefined
                    }
                  />
                )}
              </div>
            )}
        </div>
      )}
    </div>
  );
};

const CISView: React.FC<{ cluster: string; segmented: React.ReactNode }> = ({
  cluster,
  segmented,
}) => {
  const { report, error, loading, load } = useCis(cluster);
  const [profile, setProfile] = useState<string>('');
  const [filter, setFilter] = useState<CisFilter>(EMPTY_FILTER);
  const [expanded, setExpanded] = useState<Record<string, boolean>>({});

  useEffect(() => {
    void load({ profile });
  }, [load, profile]);

  const visible = useMemo(
    () => filterControls(report?.controls || [], filter),
    [report, filter],
  );
  const groups = useMemo(() => groupControls(visible), [visible]);

  const chooseProfile = async (p: string) => {
    setProfile(p);
    try {
      await setCisOverride(cluster, {
        provider: report?.provider.overridden
          ? report.provider.provider
          : undefined,
        profile: p || undefined,
      });
    } catch {
      /* the choice still applies to this view */
    }
  };
  const override = async (p: string) => {
    await setCisOverride(cluster, {
      provider: p || undefined,
      profile: undefined,
    }).catch(() => {});
    setProfile('');
    await load({ refresh: false });
  };
  const toggleStatus = (s: CisStatus) =>
    setFilter((f) => ({
      ...f,
      statuses: f.statuses.includes(s)
        ? f.statuses.filter((x) => x !== s)
        : [...f.statuses, s],
    }));

  const pass = countOf(report, 'automated-pass');
  const failN = countOf(report, 'automated-fail');

  return (
    <div className="health-page cis-page">
      <div className="health-toolbar">
        <div className="health-toolbar-title">
          <h2>Audit</h2>
          <span className="health-toolbar-sub">
            {report
              ? `${report.profile.title} · ${report.controls.length} controls`
              : 'CIS benchmark'}
          </span>
        </div>
        <div className="health-toolbar-controls">
          {segmented}
          {report && (
            <ProviderChip
              provider={report.provider}
              onOverride={(p) => void override(p)}
            />
          )}
          {report && (
            <select
              className="ap-select cis-profile-select"
              aria-label="Benchmark profile"
              value={profile || report.profile.id}
              onChange={(e) =>
                void chooseProfile(
                  e.target.value === report.autoProfile ? '' : e.target.value,
                )
              }
            >
              {report.profiles.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.title}
                  {p.id === report.autoProfile ? ' (detected)' : ''}
                </option>
              ))}
            </select>
          )}
          <button
            type="button"
            className="ap-icon-btn"
            title="Export CSV"
            aria-label="Export CSV"
            disabled={!report}
            onClick={() =>
              report &&
              download(
                exportFileName(report, 'csv'),
                reportCSV(report),
                'text/csv',
              )
            }
          >
            <DownloadIcon />
          </button>
          <button
            type="button"
            className="ap-btn ap-btn--ghost ap-btn--sm"
            disabled={!report}
            onClick={() =>
              report &&
              download(
                exportFileName(report, 'json'),
                reportJSON(report),
                'application/json',
              )
            }
          >
            JSON
          </button>
          <button
            type="button"
            className="ap-icon-btn"
            title="Re-run benchmark"
            aria-label="Re-run benchmark"
            onClick={() => void load({ profile, refresh: true })}
          >
            <ReloadIcon className={loading ? 'health-spin' : undefined} />
          </button>
        </div>
      </div>

      {report?.notes.map((n, i) => (
        <div
          key={i}
          className={`health-unavailable cis-note cis-note--${n.kind}`}
          role="note"
        >
          <span
            className={`ap-dot ${n.kind === 'rbac' ? 'ap-dot--warning' : 'ap-dot--info'}`}
          />
          {n.text}
        </div>
      ))}

      <div className="health-body">
        {error && !report && (
          <HealthEmpty
            title="Could not run the CIS benchmark"
            detail={error}
            action={
              <button
                type="button"
                className="ap-btn"
                onClick={() => void load({ profile, refresh: true })}
              >
                Try again
              </button>
            }
          />
        )}
        {!error && !report && (
          <div className="health-skeleton">
            <div className="cis-loading-hint">
              Reading kubelet configuration, control-plane flags and scanner
              reports…
            </div>
            {[0, 1, 2, 3].map((i) => (
              <div key={i} className="ap-skeleton health-skeleton-row" />
            ))}
          </div>
        )}
        {report && (
          <>
            <div className="health-scores cis-scores">
              <div className="ap-card health-score-card health-score-card--main">
                {report.score !== null ? (
                  <ScoreRing
                    score={report.score}
                    size={84}
                    label="CIS compliance"
                  />
                ) : (
                  <div className="cis-ring-empty">–</div>
                )}
                <div>
                  <div className="health-score-title">CIS compliance</div>
                  <div className="health-score-sub ap-tnum">
                    {pass} pass · {failN} fail of {pass + failN} automated
                  </div>
                  <div className="cis-score-others ap-tnum">
                    {(
                      [
                        'manual',
                        'provider-responsibility',
                        'not-visible',
                      ] as CisStatus[]
                    )
                      .filter((s) => countOf(report, s) > 0)
                      .map((s) => (
                        <button
                          key={s}
                          type="button"
                          title={`Show only ${STATUS_LABEL[s].toLowerCase()} controls; they do not count toward the score`}
                          onClick={() =>
                            setFilter({ ...EMPTY_FILTER, statuses: [s] })
                          }
                        >
                          {countOf(report, s)} {STATUS_LABEL[s].toLowerCase()}
                        </button>
                      ))}
                  </div>
                </div>
              </div>
              <div className="cis-sections">
                {report.sections.map((s) => (
                  <button
                    key={s.id}
                    type="button"
                    className={`ap-card health-score-card cis-section-card${filter.section === s.id ? ' is-selected' : ''}`}
                    onClick={() =>
                      setFilter((f) => ({
                        ...f,
                        section: f.section === s.id ? '' : s.id,
                      }))
                    }
                    title={`Show only section ${s.id}`}
                  >
                    {s.score !== null ? (
                      <ScoreRing score={s.score} size={44} label={s.title} />
                    ) : (
                      <div
                        className="cis-ring-empty cis-ring-empty--sm"
                        title="Nothing automated in this section"
                      >
                        –
                      </div>
                    )}
                    <div className="cis-section-text">
                      <div className="health-score-title">
                        {s.id}. {s.title}
                      </div>
                      <div className="health-score-sub ap-tnum">
                        {(s.counts['automated-fail'] || 0) > 0
                          ? `${s.counts['automated-fail']} fail`
                          : s.score !== null
                            ? 'no failures'
                            : 'not automated'}
                      </div>
                    </div>
                  </button>
                ))}
              </div>
            </div>

            <div className="cis-filters">
              <div
                className="cis-status-pills"
                role="group"
                aria-label="Filter by status"
              >
                {STATUS_ORDER.filter((s) => countOf(report, s) > 0).map((s) => (
                  <button
                    key={s}
                    type="button"
                    className={`ap-pill${filter.statuses.includes(s) ? ' is-active' : ''}`}
                    aria-pressed={filter.statuses.includes(s)}
                    onClick={() => toggleStatus(s)}
                  >
                    {STATUS_LABEL[s]}{' '}
                    <span className="ap-pill-count">{countOf(report, s)}</span>
                  </button>
                ))}
              </div>
              <select
                className="ap-select cis-section-select"
                aria-label="Section"
                value={filter.section}
                onChange={(e) =>
                  setFilter((f) => ({ ...f, section: e.target.value }))
                }
              >
                <option value="">All sections</option>
                {report.sections.map((s) => (
                  <option key={s.id} value={s.id}>
                    {s.id}. {s.title}
                  </option>
                ))}
              </select>
              <label className="ap-toggle-label health-check-label">
                <input
                  type="checkbox"
                  className="ap-checkbox"
                  checked={filter.scoredOnly}
                  onChange={(e) =>
                    setFilter((f) => ({ ...f, scoredOnly: e.target.checked }))
                  }
                />
                Scored only
              </label>
              <input
                className="ap-input cis-search"
                type="search"
                placeholder="Filter controls"
                value={filter.search}
                onChange={(e) =>
                  setFilter((f) => ({ ...f, search: e.target.value }))
                }
                aria-label="Filter controls"
              />
              {(filter.statuses.length > 0 ||
                filter.section ||
                filter.scoredOnly ||
                filter.search) && (
                <button
                  type="button"
                  className="ap-btn ap-btn--ghost ap-btn--sm"
                  onClick={() => setFilter(EMPTY_FILTER)}
                >
                  Clear
                </button>
              )}
            </div>

            {groups.length === 0 && (
              <HealthEmpty
                title="No controls match"
                detail="Clear the filters to see every control."
              />
            )}
            {groups.map((g) => {
              const isOpen = expanded[g.id] ?? !quietGroup(g);
              return (
                <div
                  key={g.id}
                  className={`cis-group${isOpen ? '' : ' is-collapsed'}`}
                >
                  <button
                    type="button"
                    className="ap-group-label health-section-label cis-group-label"
                    aria-expanded={isOpen}
                    onClick={() =>
                      setExpanded((m) => ({ ...m, [g.id]: !isOpen }))
                    }
                  >
                    <span className="cis-group-name">
                      <ExpandIcon expanded={isOpen} />
                      <span className="cis-group-id ap-tnum">{g.id}</span>{' '}
                      {g.title}
                    </span>
                    <span className="cis-group-counts ap-tnum">
                      {(g.counts['automated-fail'] || 0) > 0 && (
                        <span className="cis-count cis-count--fail">
                          {g.counts['automated-fail']} fail
                        </span>
                      )}
                      {(g.counts['automated-pass'] || 0) > 0 && (
                        <span className="cis-count">
                          {g.counts['automated-pass']} pass
                        </span>
                      )}
                      {!isOpen &&
                        (
                          [
                            'manual',
                            'not-visible',
                            'provider-responsibility',
                          ] as CisStatus[]
                        )
                          .filter((s) => g.counts[s])
                          .map((s) => (
                            <span key={s} className="cis-count">
                              {g.counts[s]} {STATUS_LABEL[s].toLowerCase()}
                            </span>
                          ))}
                    </span>
                  </button>
                  {isOpen && (
                    <div className="ap-card health-card">
                      {g.controls.map((c) => (
                        <ControlRow
                          key={c.id}
                          cluster={cluster}
                          c={c}
                          report={report}
                        />
                      ))}
                    </div>
                  )}
                </div>
              );
            })}

            {report.scanners.length > 0 && (
              <>
                <div className="ap-group-label health-section-label">
                  Scanner reports read
                </div>
                <div className="ap-card health-card">
                  {report.scanners.map((s, i) => (
                    <div key={i} className="ap-card-row">
                      <span>
                        {SOURCE_LABEL[s.source] || s.source}{' '}
                        <span className="ap-muted">{s.report}</span>
                      </span>
                      <span className="ap-card-row-value ap-tnum">
                        {s.controls} controls ·{' '}
                        {agoLabel(s.scannedAt) || 'time unknown'}
                        {!s.used && ' · other profile, not merged'}
                      </span>
                    </div>
                  ))}
                </div>
              </>
            )}
            <p className="cis-legal">
              Control numbers follow the CIS Benchmarks; titles, descriptions
              and remediation notes are Kanivet's own summaries, not the
              benchmark text. Results are read-only observations and are not a
              certified CIS assessment.
            </p>
          </>
        )}
      </div>
    </div>
  );
};

export default CISView;
