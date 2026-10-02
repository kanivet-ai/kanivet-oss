import React, { useEffect, useMemo, useRef, useState } from 'react';
import {
  Cross2Icon,
  DownloadIcon,
  ExclamationTriangleIcon,
  InfoCircledIcon,
  MagnifyingGlassIcon,
  ReloadIcon,
} from '@radix-ui/react-icons';
import type {
  RightsizingReport,
  Verdict,
  WorkloadReport,
} from '../../types/rightsizing';
import { parseClusterName } from '../../utils/clusterUtils';
import { Tooltip } from '../common/Tooltip';
import { workloadResource } from '../finops/finopsView';
import { useFinOpsNavigation } from '../finops/useFinOpsNavigation';
import { HistorySourceState } from './RightsizingParts';
import { EvidenceSheet } from './EvidenceSheet';
import { MethodologySheet } from './MethodologySheet';
import { FacetMenu } from './FacetMenu';
import { RightsizingTable, workloadId } from './RightsizingTable';
import {
  PROFILE_META,
  VERDICT_META,
  bulkKubectl,
  bulkYAML,
  emptyFilters,
  facetValues,
  filterWorkloads,
  formatMoney,
  formatPct,
  groupWorkloads,
  hasChange,
  namespaceOf,
  relativeTime,
  releaseOf,
  sortWorkloads,
  teamOf,
  toCSV,
  toMarkdown,
  type GroupKey,
  type SortKey,
  type TriageFilters,
} from './rightsizingView';
import {
  useRightsizingPrefs,
  useRightsizingReport,
} from './useRightsizingReport';
import './Rightsizing.css';

const windowWords = (w?: string) => `${parseInt(w ?? '14', 10)} days`;

const FILTER_ORDER: Verdict[] = [
  'under-provisioned',
  'no-requests',
  'over-provisioned',
  'hpa-coupled',
  'right-sized',
  'insufficient-data',
];

const SUMMARY_COUNT: Record<Verdict, keyof RightsizingReport['summary']> = {
  'under-provisioned': 'under',
  'no-requests': 'noRequests',
  'over-provisioned': 'over',
  'hpa-coupled': 'hpaCoupled',
  'right-sized': 'right',
  'insufficient-data': 'insufficient',
};

const SORTS: { key: SortKey; label: string }[] = [
  { key: 'risk', label: 'Most urgent' },
  { key: 'savings', label: 'Biggest savings' },
  { key: 'cpu', label: 'Largest CPU cut' },
  { key: 'memory', label: 'Largest memory cut' },
  { key: 'name', label: 'Name' },
];

const MIN_SAVINGS = [0, 5, 20, 100];

function download(name: string, text: string, type: string) {
  const url = URL.createObjectURL(new Blob([text], { type }));
  const a = document.createElement('a');
  a.href = url;
  a.download = name;
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

// ---- Remembered view ---------------------------------------------------------

interface ViewState {
  filters: TriageFilters;
  sort: SortKey;
  group: GroupKey;
}

const viewKey = (cluster: string) => `kanivet.rightsizing.view:${cluster}`;

function loadView(cluster: string): ViewState {
  const fallback: ViewState = {
    filters: emptyFilters(),
    sort: 'risk',
    group: 'namespace',
  };
  try {
    const raw = localStorage.getItem(viewKey(cluster));
    if (!raw) return fallback;
    const v = JSON.parse(raw);
    return {
      filters: {
        ...emptyFilters(),
        verdicts: new Set(v.verdicts ?? []),
        namespaces: new Set(v.namespaces ?? []),
        releases: new Set(v.releases ?? []),
        teams: new Set(v.teams ?? []),
        kinds: new Set(v.kinds ?? []),
        minSavings: v.minSavings ?? 0,
        showDismissed: !!v.showDismissed,
      },
      sort: v.sort ?? 'risk',
      group: v.group ?? 'namespace',
    };
  } catch {
    return fallback;
  }
}

function saveView(cluster: string, v: ViewState) {
  try {
    const f = v.filters;
    localStorage.setItem(
      viewKey(cluster),
      JSON.stringify({
        verdicts: [...f.verdicts],
        namespaces: [...f.namespaces],
        releases: [...f.releases],
        teams: [...f.teams],
        kinds: [...f.kinds],
        minSavings: f.minSavings,
        showDismissed: f.showDismissed,
        sort: v.sort,
        group: v.group,
      }),
    );
  } catch {
    // A remembered view is a convenience; storage may be unavailable.
  }
}

const activeCount = (f: TriageFilters) =>
  f.verdicts.size +
  f.namespaces.size +
  f.releases.size +
  f.teams.size +
  f.kinds.size +
  (f.minSavings > 0 ? 1 : 0);

// ---- Pieces ------------------------------------------------------------------------

const Progress: React.FC<{
  report: RightsizingReport | null;
  slim?: boolean;
}> = ({ report, slim }) => {
  const p = report?.progress;
  const pct = p && p.total > 0 ? (p.done / p.total) * 100 : 0;
  if (slim) {
    return (
      <div
        className={`rs-progress-slim${p?.paused ? ' paused' : ''}`}
        role="status"
      >
        <span>
          {p?.stage ?? 'Refreshing'}
          {p && p.total > 0 ? ` · ${p.done} of ${p.total} namespaces` : ''}
        </span>
        <div className="rs-progress-track">
          <div style={{ width: `${pct}%` }} />
        </div>
      </div>
    );
  }
  return (
    <div
      className={`rs-progress-card${p?.paused ? ' paused' : ''}`}
      // Only a report actually computing: before the first answer the
      // dashboard doesn't yet know whether the cluster has metrics.
      data-tour={report ? 'rs-progress' : undefined}
      role="status"
    >
      <div className="rs-progress-title">Analysing usage history</div>
      <div className="rs-progress-stage">
        {p?.stage ?? 'Starting'}
        {p && p.total > 0 ? `: ${p.done} of ${p.total} namespaces` : '…'}
      </div>
      <div className="rs-progress-track">
        <div style={{ width: `${pct}%` }} />
      </div>
      <p>
        Reading {windowWords(report?.window)} of CPU and memory for every
        container. Large clusters take a minute; the result is kept, so next
        time it opens at once.
      </p>
    </div>
  );
};

const Signal: React.FC<{
  on: boolean;
  partial?: boolean;
  label: string;
  detail: string;
  note?: string;
}> = ({ on, partial, label, detail, note }) => (
  <Tooltip content={note}>
    <div className={`rs-signal ${partial ? 'partial' : on ? 'on' : 'off'}`}>
      <span
        className={`ap-dot ap-dot--${partial ? 'warning' : on ? 'success' : 'muted'}`}
        aria-hidden="true"
      />
      <span className="rs-signal-label">{label}</span>
      <span className="rs-signal-detail">
        {on || partial ? detail : 'not available'}
      </span>
    </div>
  </Tooltip>
);

const Pill: React.FC<{ label: string; onClear: () => void }> = ({
  label,
  onClear,
}) => (
  <span className="rs-pill">
    {label}
    <button onClick={onClear} aria-label={`Remove filter ${label}`}>
      <Cross2Icon />
    </button>
  </span>
);

// ---- Dashboard -----------------------------------------------------------------

export const RightsizingDashboard: React.FC<{ cluster: string }> = ({
  cluster,
}) => {
  const { profile, window, setProfile, setWindow } = useRightsizingPrefs();
  const { report, error, refresh, reload } = useRightsizingReport(
    cluster,
    profile,
    window,
  );
  const [view, setView] = useState<ViewState>(() => loadView(cluster));
  const [search, setSearch] = useState('');
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [open, setOpen] = useState<WorkloadReport | null>(null);
  const [methodology, setMethodology] = useState(false);
  const [exportOpen, setExportOpen] = useState(false);
  const [copied, setCopied] = useState<string | null>(null);
  const searchRef = useRef<HTMLInputElement>(null);
  const nav = useFinOpsNavigation(cluster);

  useEffect(() => setView(loadView(cluster)), [cluster]);
  useEffect(() => saveView(cluster, view), [cluster, view]);

  const f = view.filters;
  const setFilters = (patch: Partial<TriageFilters>) =>
    setView((v) => ({ ...v, filters: { ...v.filters, ...patch } }));

  const workloads = report?.workloads ?? [];
  const filters = useMemo(() => ({ ...f, search }), [f, search]);
  const filtered = useMemo(
    () => filterWorkloads(workloads, filters),
    [workloads, filters],
  );
  const sorted = useMemo(
    () => sortWorkloads(filtered, view.sort),
    [filtered, view.sort],
  );
  const groups = useMemo(
    () => groupWorkloads(sorted, view.group),
    [sorted, view.group],
  );

  // Facet counts come from everything else that is filtered, so a facet
  // never offers values that would show nothing.
  const without = (patch: Partial<TriageFilters>) =>
    filterWorkloads(workloads, { ...filters, ...patch });
  const nsValues = useMemo(
    () => facetValues(without({ namespaces: new Set() }), namespaceOf),
    [workloads, filters],
  ); // eslint-disable-line react-hooks/exhaustive-deps
  const releaseValues = useMemo(
    () => facetValues(without({ releases: new Set() }), releaseOf),
    [workloads, filters],
  ); // eslint-disable-line react-hooks/exhaustive-deps
  const teamValues = useMemo(
    () => facetValues(without({ teams: new Set() }), teamOf),
    [workloads, filters],
  ); // eslint-disable-line react-hooks/exhaustive-deps
  const kindValues = useMemo(
    () => facetValues(without({ kinds: new Set() }), (w) => w.kind),
    [workloads, filters],
  ); // eslint-disable-line react-hooks/exhaustive-deps

  const selectedWorkloads = useMemo(
    () => workloads.filter((w) => selected.has(workloadId(w))),
    [workloads, selected],
  );
  const selectedSavings = selectedWorkloads.reduce(
    (s, w) => s + Math.max(0, w.monthlySavings),
    0,
  );
  const selectedChanges = selectedWorkloads.filter(hasChange).length;

  const ready = report && report.status === 'ready';
  const s = report?.summary;
  const cal = report?.calibration;
  const calPct =
    cal && cal.containers > 0 ? cal.calibrated / cal.containers : 0;
  const clusterName = parseClusterName(cluster).displayName;
  const busy = !!report?.progress || report?.status === 'computing';

  const copy = (what: string, text: string) =>
    navigator.clipboard.writeText(text).then(() => {
      setCopied(what);
      setTimeout(() => setCopied(null), 1600);
    });

  const toggleVerdict = (v: Verdict) => {
    const next = new Set(f.verdicts);
    if (next.has(v)) next.delete(v);
    else next.add(v);
    setFilters({ verdicts: next });
  };

  const onRootKey = (e: React.KeyboardEvent) => {
    const typing = (e.target as HTMLElement).closest('input, select, textarea');
    if (e.key === '/' && !typing) {
      e.preventDefault();
      searchRef.current?.focus();
    } else if (e.key === 'Escape' && !open) {
      if (typing) (e.target as HTMLElement).blur();
      if (selected.size > 0) setSelected(new Set());
      else if (search) setSearch('');
    }
  };

  const pills: { label: string; clear: () => void }[] = [
    ...[...f.verdicts].map((v) => ({
      label: VERDICT_META[v].short,
      clear: () => toggleVerdict(v),
    })),
    ...[...f.namespaces].map((n) => ({
      label: `ns: ${n}`,
      clear: () =>
        setFilters({
          namespaces: new Set([...f.namespaces].filter((x) => x !== n)),
        }),
    })),
    ...[...f.releases].map((n) => ({
      label: `release: ${n}`,
      clear: () =>
        setFilters({
          releases: new Set([...f.releases].filter((x) => x !== n)),
        }),
    })),
    ...[...f.teams].map((n) => ({
      label: `team: ${n}`,
      clear: () =>
        setFilters({ teams: new Set([...f.teams].filter((x) => x !== n)) }),
    })),
    ...[...f.kinds].map((n) => ({
      label: n,
      clear: () =>
        setFilters({ kinds: new Set([...f.kinds].filter((x) => x !== n)) }),
    })),
    ...(f.minSavings > 0
      ? [
          {
            label: `saves ≥ $${f.minSavings}/mo`,
            clear: () => setFilters({ minSavings: 0 }),
          },
        ]
      : []),
  ];

  return (
    <div
      className="rs-dashboard"
      data-tour="rs-dashboard"
      onKeyDown={onRootKey}
    >
      <div className="rs-header">
        <div className="rs-title">
          <svg
            viewBox="0 0 16 16"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.6"
            strokeLinecap="round"
            aria-hidden="true"
          >
            <path d="M2 13.5h12M4 11V7M8 11V3M12 11V8.5" />
          </svg>
          <h2>Rightsizing</h2>
          {report?.source?.type && ready && (
            <Tooltip
              content={`History from ${report.source.flavor || report.source.type} ${report.source.namespace}/${report.source.service}, ${report.step} samples`}
            >
              <span className="rs-source-chip">
                {report.source.flavor || report.source.type} ·{' '}
                {windowWords(report.window)}
              </span>
            </Tooltip>
          )}
        </div>
        <div className="rs-actions">
          <div
            className="ap-segmented"
            role="group"
            aria-label="Risk profile"
            data-tour="rs-profile"
          >
            {(Object.keys(PROFILE_META) as (keyof typeof PROFILE_META)[]).map(
              (p) => (
                <Tooltip
                  key={p}
                  content={`CPU covers ${PROFILE_META[p].cpu} of replica-time; memory gets ${PROFILE_META[p].mem} over its peak`}
                >
                  <button
                    aria-pressed={profile === p}
                    onClick={() => setProfile(p)}
                  >
                    {PROFILE_META[p].label}
                  </button>
                </Tooltip>
              ),
            )}
          </div>
          <select
            className="ap-select rs-window"
            value={window}
            onChange={(e) => setWindow(e.target.value as typeof window)}
            aria-label="History window"
          >
            <option value="7d">7 days</option>
            <option value="14d">14 days</option>
            <option value="28d">28 days</option>
          </select>
          {ready && report && (
            <span className="rs-updated">
              Updated {relativeTime(report.computedAt)}
            </span>
          )}
          <Tooltip content="How recommendations are made">
            <button
              className="finops-refresh"
              onClick={() => setMethodology(true)}
              aria-label="How recommendations are made"
            >
              <InfoCircledIcon />
            </button>
          </Tooltip>
          <div className="rs-export">
            <Tooltip content="Export">
              <button
                className="finops-refresh"
                onClick={() => setExportOpen(!exportOpen)}
                disabled={!ready}
                aria-label="Export"
                aria-expanded={exportOpen}
              >
                <DownloadIcon />
              </button>
            </Tooltip>
            {exportOpen && ready && (
              <div
                className="ap-menu rs-export-menu"
                onMouseLeave={() => setExportOpen(false)}
              >
                <button
                  className="ap-menu-item"
                  onClick={() => {
                    download(
                      `rightsizing-${clusterName}.csv`,
                      toCSV(sorted),
                      'text/csv',
                    );
                    setExportOpen(false);
                  }}
                >
                  CSV of {sorted.length} shown
                </button>
                <button
                  className="ap-menu-item"
                  onClick={() => {
                    copy('md', toMarkdown(sorted, clusterName));
                    setExportOpen(false);
                  }}
                >
                  Copy as Markdown table
                </button>
              </div>
            )}
          </div>
          <Tooltip content="Recompute now">
            <button
              className="finops-refresh"
              onClick={refresh}
              disabled={busy}
              aria-label="Recompute"
            >
              <ReloadIcon className={busy ? 'spinning' : undefined} />
            </button>
          </Tooltip>
        </div>
      </div>

      {error && !report && (
        <div className="finops-error" role="alert" data-tour="rs-error">
          <ExclamationTriangleIcon />
          <span>{error}</span>
          <button className="finops-banner-action" onClick={reload}>
            Retry
          </button>
        </div>
      )}
      {report?.status === 'error' && (
        <div className="finops-error" role="alert" data-tour="rs-error">
          <ExclamationTriangleIcon />
          <span>Couldn't compute recommendations: {report.error}</span>
          <button className="finops-banner-action" onClick={refresh}>
            Retry
          </button>
        </div>
      )}
      {report?.refreshError && (
        <div className="finops-error finops-info" role="status">
          <InfoCircledIcon />
          <span>
            Showing results from {relativeTime(report.computedAt)}; the latest
            refresh failed: {report.refreshError}
          </span>
          <button className="finops-banner-action" onClick={refresh}>
            Retry
          </button>
        </div>
      )}
      {ready && report.error && (
        <div className="finops-error finops-info" role="status">
          <InfoCircledIcon />
          <span>{report.error}</span>
        </div>
      )}
      {ready && report.progress && <Progress report={report} slim />}

      <div className="rs-content">
        {!report && !error && <Progress report={null} />}
        {report &&
          (report.status === 'computing' ||
            (report.status !== 'ready' && report.progress)) && (
            <Progress report={report} />
          )}
        {report &&
          !report.progress &&
          (report.status === 'no-history-source' ||
            report.status === 'needs-tenant' ||
            report.status === 'no-container-data') && (
            <HistorySourceState
              report={report}
              cluster={cluster}
              onRetry={refresh}
            />
          )}

        {ready && s && cal && (
          <div className="rs-body">
            <div className="rs-stats" data-tour="rs-stats">
              <button
                className={`stat-card rs-stat-btn${s.monthlySavings > 0 ? ' savings' : ''}`}
                onClick={() => setView((v) => ({ ...v, sort: 'savings' }))}
              >
                <div className="stat-label">Potential savings</div>
                <div className="stat-value">
                  {s.monthlySavings > 0 ? formatMoney(s.monthlySavings) : '—'}
                  <span className="rs-unit">/mo</span>
                </div>
                <div className="stat-sub">
                  {s.monthlySavings > 0
                    ? `${s.shrinking} workloads · ${formatMoney(s.savingsLow)}–${formatMoney(s.savingsHigh)}`
                    : 'Nothing to trim'}
                </div>
              </button>
              <button
                className={`stat-card rs-stat-btn${s.under > 0 ? ' danger' : ''}`}
                onClick={() =>
                  setFilters({ verdicts: new Set(['under-provisioned']) })
                }
              >
                <div className="stat-label">At risk</div>
                <div className="stat-value">{s.under}</div>
                <div className="stat-sub">
                  {s.under > 0
                    ? 'OOM kills or resource pressure'
                    : 'No reliability risks'}
                </div>
              </button>
              <Tooltip
                content={`Each recommendation is re-fitted without the last days of the window and scored on them. ${formatPct(calPct, 0)} stayed within their target: CPU at most twice its allowed time over, memory never over.`}
              >
                <div className="stat-card rs-trust">
                  <div className="stat-label">
                    Track record <InfoCircledIcon className="stat-info" />
                  </div>
                  <div className="stat-value">
                    {cal.containers > 0 ? formatPct(calPct, 0) : '—'}
                  </div>
                  <div className="stat-sub">
                    {cal.containers > 0
                      ? 'held up on unseen days'
                      : 'needs 5+ days'}
                  </div>
                </div>
              </Tooltip>
              <div className="stat-card rs-coverage">
                <div className="stat-label">Evidence</div>
                <div className="rs-signals">
                  <Signal
                    on
                    label="Usage history"
                    detail={`${windowWords(report.window)}, ${report.step}`}
                  />
                  <Signal
                    on={report.signals.oomKills}
                    label="OOM kills, restarts"
                    detail="kube-state-metrics"
                  />
                  <Signal
                    on={report.signals.memoryMetric === 'working-set'}
                    partial={report.signals.memoryMetric === 'usage'}
                    label="Memory working set"
                    detail={
                      report.signals.memoryMetric === 'usage'
                        ? 'total usage only'
                        : 'cAdvisor'
                    }
                    note={
                      report.signals.memoryMetric === 'usage'
                        ? 'This cluster reports total memory usage, page cache included. Memory recommendations only go down, never up, unless an OOM kill shows real pressure.'
                        : undefined
                    }
                  />
                  <Signal
                    on={
                      report.signals.throttling &&
                      report.signals.throttleKind === 'periods'
                    }
                    partial={report.signals.throttleKind === 'seconds'}
                    label="CPU throttling"
                    detail={
                      report.signals.throttleKind === 'seconds'
                        ? 'seconds only'
                        : 'CFS periods'
                    }
                  />
                </div>
              </div>
            </div>

            <div className="rs-toolbar">
              <div className="ap-search rs-search">
                <MagnifyingGlassIcon />
                <input
                  ref={searchRef}
                  value={search}
                  onChange={(e) => setSearch(e.target.value)}
                  placeholder="Search workloads, labels, containers"
                  aria-label="Search workloads"
                />
                {search ? (
                  <button
                    className="rs-search-clear"
                    onClick={() => setSearch('')}
                    aria-label="Clear search"
                  >
                    <Cross2Icon />
                  </button>
                ) : (
                  <kbd className="rs-kbd">/</kbd>
                )}
              </div>
              <FacetMenu
                label="Namespace"
                values={nsValues}
                selected={f.namespaces}
                onChange={(namespaces) => setFilters({ namespaces })}
              />
              <FacetMenu
                label="Release"
                values={releaseValues}
                selected={f.releases}
                onChange={(releases) => setFilters({ releases })}
                emptyHint="No app.kubernetes.io/instance labels"
              />
              {(teamValues.length > 0 || f.teams.size > 0) && (
                <FacetMenu
                  label="Team"
                  values={teamValues}
                  selected={f.teams}
                  onChange={(teams) => setFilters({ teams })}
                />
              )}
              <FacetMenu
                label="Kind"
                values={kindValues}
                selected={f.kinds}
                onChange={(kinds) => setFilters({ kinds })}
              />
              <select
                className="ap-select rs-min-savings"
                value={f.minSavings}
                onChange={(e) =>
                  setFilters({ minSavings: Number(e.target.value) })
                }
                aria-label="Minimum savings"
              >
                {MIN_SAVINGS.map((m) => (
                  <option key={m} value={m}>
                    {m === 0 ? 'Any savings' : `Saves ≥ $${m}/mo`}
                  </option>
                ))}
              </select>
              <div className="rs-toolbar-right">
                <div
                  className="ap-segmented ap-segmented--sm"
                  role="group"
                  aria-label="Group by"
                >
                  {(['namespace', 'release', 'none'] as GroupKey[]).map((g) => (
                    <button
                      key={g}
                      aria-pressed={view.group === g}
                      onClick={() => setView((v) => ({ ...v, group: g }))}
                    >
                      {g === 'none'
                        ? 'No groups'
                        : g === 'namespace'
                          ? 'By namespace'
                          : 'By release'}
                    </button>
                  ))}
                </div>
                <select
                  className="ap-select rs-sort"
                  value={view.sort}
                  onChange={(e) =>
                    setView((v) => ({ ...v, sort: e.target.value as SortKey }))
                  }
                  aria-label="Sort"
                >
                  {SORTS.map((o) => (
                    <option key={o.key} value={o.key}>
                      {o.label}
                    </option>
                  ))}
                </select>
              </div>
            </div>

            <div
              className="rs-filters"
              data-tour="rs-filters"
              role="group"
              aria-label="Filter by verdict"
            >
              {FILTER_ORDER.map((v) => {
                const n = s[SUMMARY_COUNT[v]] as number;
                if (n === 0) return null;
                return (
                  <button
                    key={v}
                    className={`rs-filter${f.verdicts.has(v) ? ' is-active' : ''}`}
                    onClick={() => toggleVerdict(v)}
                  >
                    <span
                      className={`ap-dot ap-dot--${VERDICT_META[v].tone === 'neutral' ? 'muted' : VERDICT_META[v].tone}`}
                    />
                    {VERDICT_META[v].short}{' '}
                    <span className="rs-filter-count">{n}</span>
                  </button>
                );
              })}
              {s.dismissed > 0 && (
                <button
                  className={`rs-filter${f.showDismissed ? ' is-active' : ''}`}
                  onClick={() =>
                    setFilters({ showDismissed: !f.showDismissed })
                  }
                >
                  Dismissed{' '}
                  <span className="rs-filter-count">{s.dismissed}</span>
                </button>
              )}
              <span className="rs-shown">
                {sorted.length === workloads.length
                  ? `${sorted.length} workloads`
                  : `${sorted.length} of ${workloads.length} workloads`}
              </span>
            </div>

            {(pills.length > 0 || search) && (
              <div className="rs-pills">
                {pills.map((p) => (
                  <Pill key={p.label} label={p.label} onClear={p.clear} />
                ))}
                {search && (
                  <Pill label={`“${search}”`} onClear={() => setSearch('')} />
                )}
                {activeCount(f) + (search ? 1 : 0) > 1 && (
                  <button
                    className="rs-clear-all"
                    onClick={() => {
                      setSearch('');
                      setFilters({
                        ...emptyFilters(),
                        showDismissed: f.showDismissed,
                      });
                    }}
                  >
                    Clear all
                  </button>
                )}
              </div>
            )}

            <RightsizingTable
              groups={groups}
              groupBy={view.group}
              selected={selected}
              onToggle={(id) =>
                setSelected((prev) => {
                  const next = new Set(prev);
                  if (next.has(id)) next.delete(id);
                  else next.add(id);
                  return next;
                })
              }
              onToggleMany={(ids, on) =>
                setSelected((prev) => {
                  const next = new Set(prev);
                  ids.forEach((id) => (on ? next.add(id) : next.delete(id)));
                  return next;
                })
              }
              onOpen={setOpen}
              openWorkload={(w) =>
                workloadResource(w.kind) && !w.vclusterNamespace
                  ? () => nav.openWorkload(w.kind, w.namespace, w.name)
                  : undefined
              }
              emptyText={
                workloads.length === 0
                  ? 'No running workloads'
                  : 'No workloads match these filters'
              }
            />
            <div className="rs-hint">
              <kbd className="rs-kbd">/</kbd> search ·{' '}
              <kbd className="rs-kbd">j</kbd> <kbd className="rs-kbd">k</kbd>{' '}
              move · <kbd className="rs-kbd">Enter</kbd> open ·{' '}
              <kbd className="rs-kbd">x</kbd> select ·{' '}
              <kbd className="rs-kbd">Esc</kbd> clear
            </div>
          </div>
        )}
      </div>

      {selected.size > 0 && (
        <div
          className="rs-selection"
          role="region"
          aria-label="Selected workloads"
        >
          <span className="rs-selection-count">
            {selected.size} selected
            {selectedSavings > 0 && (
              <> · saves {formatMoney(selectedSavings)}/mo</>
            )}
          </span>
          <button
            className="ap-btn"
            disabled={selectedChanges === 0}
            onClick={() => copy('sh', bulkKubectl(selectedWorkloads))}
          >
            {copied === 'sh'
              ? 'Copied'
              : `Copy kubectl script (${selectedChanges})`}
          </button>
          <button
            className="ap-btn"
            disabled={selectedChanges === 0}
            onClick={() => copy('yaml', bulkYAML(selectedWorkloads))}
          >
            {copied === 'yaml' ? 'Copied' : 'Copy patches as YAML'}
          </button>
          <button
            className="ap-btn"
            onClick={() =>
              download(
                `rightsizing-${clusterName}-selected.csv`,
                toCSV(selectedWorkloads),
                'text/csv',
              )
            }
          >
            CSV
          </button>
          <button
            className="ap-btn ap-btn--ghost"
            onClick={() => setSelected(new Set())}
          >
            Clear
          </button>
        </div>
      )}
      {copied === 'md' && <div className="rs-toast">Markdown table copied</div>}

      {open && (
        <EvidenceSheet
          cluster={cluster}
          workload={open}
          profile={profile}
          window={window}
          onClose={() => setOpen(null)}
          onChanged={reload}
        />
      )}
      {methodology && report && (
        <MethodologySheet
          report={report}
          onClose={() => setMethodology(false)}
        />
      )}
    </div>
  );
};

export default RightsizingDashboard;
