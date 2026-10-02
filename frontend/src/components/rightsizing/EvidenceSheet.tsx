import React, { useDeferredValue, useEffect, useMemo, useState } from 'react';
import { createPortal } from 'react-dom';
import {
  CheckIcon,
  ClipboardCopyIcon,
  ExclamationTriangleIcon,
} from '@radix-ui/react-icons';
import api from '../../services/api';
import { useRightsizingProvider } from './useRightsizingReport';
import type {
  ContainerReport,
  Evidence,
  RightsizingProfile,
  RightsizingWindow,
  WorkloadReport,
} from '../../types/rightsizing';
import {
  ChartLegend,
  CPUChart,
  MemoryChart,
  type RefLine,
} from './EvidenceCharts';
import {
  ChangeSummary,
  ConfidenceMeter,
  VerdictBadge,
} from './RightsizingParts';
import {
  PROFILE_META,
  choiceFromRec,
  evaluateCandidate,
  formatCores,
  formatMem,
  formatMoney,
  formatPct,
  kubectlCommands,
  patchYAML,
  primaryContainer,
  quantileAt,
  repositoryPrompt,
  snapCPU,
  snapMem,
  startupBoostYAML,
  STARTUP_BOOST_INSTALL,
  type ContainerChoice,
} from './rightsizingView';

interface Props {
  cluster: string;
  workload: WorkloadReport;
  profile: RightsizingProfile;
  window: RightsizingWindow;
  onClose: () => void;
  /** Called after a dismissal changes, so lists can reload. */
  onChanged?: () => void;
}

type Candidate = { cpu: number; mem: number };

// CPU spans orders of magnitude, so its slider is logarithmic.
const toLog = (v: number, lo: number, hi: number) =>
  v <= lo ? 0 : v >= hi ? 1000 : (Math.log(v / lo) / Math.log(hi / lo)) * 1000;
const fromLog = (p: number, lo: number, hi: number) =>
  lo * Math.pow(hi / lo, p / 1000);

const toSlider = (v: number, lo: number, hi: number, recommended: number) => {
  if (recommended <= lo || recommended >= hi) return toLog(v, lo, hi);
  return v <= recommended
    ? toLog(v, lo, recommended) / 2
    : 500 + toLog(v, recommended, hi) / 2;
};
const fromSlider = (p: number, lo: number, hi: number, recommended: number) => {
  if (recommended <= lo || recommended >= hi) return fromLog(p, lo, hi);
  return p <= 500
    ? fromLog(p * 2, lo, recommended)
    : fromLog((p - 500) * 2, recommended, hi);
};

const sliderPosition = (fraction: number) =>
  `calc(${fraction * 100}% + ${8 - 16 * fraction}px)`;

const CurrentRequestMarker: React.FC<{
  value: number;
  lo: number;
  hi: number;
  recommended: number;
  label: string;
}> = ({ value, lo, hi, recommended, label }) => {
  if (value <= 0) return null;
  const fraction = toSlider(value, lo, hi, recommended) / 1000;
  return (
    <span
      className="rs-range-current"
      style={{ left: sliderPosition(fraction) }}
    >
      <span style={{ transform: `translateX(-${fraction * 100}%)` }}>
        Current {label}
      </span>
    </span>
  );
};

function rangeStyle(
  recommended: number,
  cautionFloor: number,
  lo: number,
  hi: number,
): React.CSSProperties {
  // Match the thumb's travel, which is inset by half its 16px width.
  const stop = (value: number) => {
    const fraction = toSlider(value, lo, hi, recommended) / 1000;
    return sliderPosition(fraction);
  };
  return {
    '--rs-caution-start': stop(Math.min(cautionFloor, recommended)),
    '--rs-recommended-start': stop(recommended),
  } as React.CSSProperties;
}

const CopyBlock: React.FC<{ text: string; label: string; prose?: boolean }> = ({
  text,
  label,
  prose = false,
}) => {
  const [copied, setCopied] = useState(false);
  return (
    <div className={`rs-code${prose ? ' rs-code--prose' : ''}`}>
      <button
        className="ap-btn ap-btn--sm rs-code-copy"
        onClick={() =>
          navigator.clipboard.writeText(text).then(() => {
            setCopied(true);
            setTimeout(() => setCopied(false), 1500);
          })
        }
        aria-label={`Copy ${label}`}
      >
        {copied ? <CheckIcon /> : <ClipboardCopyIcon />}{' '}
        {copied ? 'Copied' : 'Copy'}
      </button>
      <pre>{text}</pre>
    </div>
  );
};

const DISMISS_REASONS = [
  'Headroom is intentional',
  'Bursty or batch workload',
  'Managed elsewhere (Helm values, GitOps)',
  'Will handle later',
];

export const EvidenceSheet: React.FC<Props> = ({
  cluster,
  workload,
  profile,
  window,
  onClose,
  onChanged,
}) => {
  const provider = useRightsizingProvider(cluster);
  const [evidence, setEvidence] = useState<Evidence | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [active, setActive] = useState(
    primaryContainer(workload)?.container ??
      workload.containers[0]?.container ??
      '',
  );
  const [candidates, setCandidates] = useState<Record<string, Candidate>>({});
  const [output, setOutput] = useState<'yaml' | 'kubectl' | 'ai'>('ai');
  const [dismissing, setDismissing] = useState(false);
  const [reason, setReason] = useState(DISMISS_REASONS[0]);
  const [snooze, setSnooze] = useState(30);

  useEffect(() => {
    let live = true;
    setEvidence(null);
    setError(null);
    api
      .getRightsizingWorkload(
        cluster,
        {
          namespace: workload.namespace,
          kind: workload.kind,
          name: workload.name,
          vclusterNamespace: workload.vclusterNamespace,
        },
        profile,
        window,
        provider,
      )
      .then((ev) => live && setEvidence(ev))
      .catch(
        (e) =>
          live &&
          setError(
            e?.response?.data?.error || e?.message || 'Failed to load evidence',
          ),
      );
    return () => {
      live = false;
    };
  }, [
    cluster,
    workload.namespace,
    workload.kind,
    workload.name,
    workload.vclusterNamespace,
    profile,
    window,
    provider,
  ]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && onClose();
    globalThis.addEventListener('keydown', onKey);
    return () => globalThis.removeEventListener('keydown', onKey);
  }, [onClose]);

  // The evidence recomputes against the report's moment, so prefer its rows.
  const w = evidence?.workload ?? workload;
  const c: ContainerReport | undefined =
    w.containers.find((x) => x.container === active) ?? w.containers[0];
  const dist = evidence?.distributions[c?.container ?? ''];
  const hourly = evidence?.series[c?.container ?? ''];
  const events = evidence?.events[c?.container ?? ''] ?? [];
  const snapshots = evidence?.profiles[c?.container ?? ''];

  const cand: Candidate | null = c
    ? (candidates[c.container] ?? {
        cpu: c.cpu.recommended,
        mem: c.memory.recommended,
      })
    : null;
  const setCand = (next: Partial<Candidate>) =>
    c &&
    cand &&
    setCandidates({ ...candidates, [c.container]: { ...cand, ...next } });
  // The charts follow the candidate at low priority, so a slider drag stays
  // smooth however much they have to redraw.
  const chartCand = useDeferredValue(cand);
  // While a slider is held, its thumb follows the pointer; only the value it
  // stands for snaps to round steps. Bound to the snapped value, the thumb
  // jumped between the few positions a small range has.
  const [drag, setDrag] = useState<{ cpu?: number; mem?: number }>({});
  const endDrag = () => setDrag({});

  const choices: ContainerChoice[] = useMemo(
    () =>
      w.containers.map((x) => {
        const k = candidates[x.container];
        return choiceFromRec(
          x,
          k?.cpu ?? x.cpu.recommended,
          k?.mem ?? x.memory.recommended,
        );
      }),
    [w, candidates],
  );

  if (!c || !cand) return null;
  const ev = evaluateCandidate(c, dist, cand.cpu, cand.mem);
  const drawn = chartCand ?? cand;
  const target =
    1 -
    (profile === 'conservative' ? 0.99 : profile === 'aggressive' ? 0.9 : 0.95);

  // Slider domains come from the evidence alone, never from the candidate:
  // a range that followed its own value would run away from the pointer.
  const presetCPU = Object.values(snapshots ?? {}).map((s) => s.cpu);
  const presetMem = Object.values(snapshots ?? {}).map((s) => s.memory);
  const positive = (xs: number[]) => xs.filter((x) => x > 0);
  const cpuTop = dist ? quantileAt(dist.q, dist.cpu, 1) : c.cpu.peak;
  const cpuLo = Math.max(
    0.001,
    Math.min(
      ...positive([
        c.cpu.p50,
        c.cpu.request,
        c.cpu.recommended,
        c.cpu.burst?.idleRecommended ?? 0,
        ...presetCPU,
      ]),
      0.01,
    ) * 0.5,
  );
  const cpuHi =
    Math.max(
      cpuTop,
      c.cpu.request,
      c.cpu.recommended,
      ...presetCPU,
      cpuLo * 4,
    ) * 1.15;
  const memLo = Math.max(
    16 * 1024 * 1024,
    Math.min(
      ...positive([
        c.memory.peak,
        c.memory.recommended,
        c.memory.request,
        ...presetMem,
      ]),
      64 * 1024 * 1024,
    ) * 0.5,
  );
  const memHi =
    Math.max(
      c.memory.peak,
      c.memory.request,
      c.memory.limit,
      c.memory.recommended,
      ...presetMem,
      memLo * 2,
    ) * 1.15;
  const cpuCautionFloor = c.cpu.recommended * 0.8;
  const memCautionFloor = Math.max(
    c.memory.recommended * 0.8,
    c.memory.peak,
    c.oomKills > 0 ? c.memory.limit + 1 : 0,
  );
  const rangeStatus = (value: number, recommended: number, floor: number) =>
    value >= recommended
      ? 'Recommended or higher'
      : value >= floor
        ? 'Reduced headroom'
        : 'Too low';

  // A limit far above everything else would flatten the usage into the
  // floor; it stays in the legend, marked off chart.
  const cpuScale = Math.max(c.cpu.p99, c.cpu.request, drawn.cpu) * 1.6;
  const cpuLimitOnChart = c.cpu.limit > 0 && c.cpu.limit <= cpuScale;
  const memScale = Math.max(c.memory.peak, c.memory.request, drawn.mem) * 1.6;
  const memLimitShown =
    c.memory.limit > 0 && c.memory.limit !== c.memory.request;
  const memLimitOnChart = memLimitShown && c.memory.limit <= memScale;
  const cpuRefs: RefLine[] = [
    ...(c.cpu.request > 0
      ? [
          {
            label: 'Current request',
            value: c.cpu.request,
            kind: 'current' as const,
          },
        ]
      : []),
    { label: 'Candidate', value: drawn.cpu, kind: 'candidate' },
    ...(cpuLimitOnChart
      ? [{ label: 'Limit', value: c.cpu.limit, kind: 'limit' as const }]
      : []),
  ];
  const memRefs: RefLine[] = [
    ...(c.memory.request > 0
      ? [
          {
            label: 'Current request',
            value: c.memory.request,
            kind: 'current' as const,
          },
        ]
      : []),
    {
      label: 'Candidate (request = limit)',
      value: drawn.mem,
      kind: 'candidate',
    },
    ...(memLimitOnChart
      ? [
          {
            label: 'Current limit',
            value: c.memory.limit,
            kind: 'limit' as const,
          },
        ]
      : []),
  ];
  const isRec =
    Math.abs(cand.cpu - c.cpu.recommended) < 1e-9 &&
    Math.abs(cand.mem - c.memory.recommended) < 1;
  const availableChoices = choices.filter(
    (choice) => choice.cpu > 0 || choice.memory > 0,
  );
  const yaml = patchYAML(w.kind, availableChoices);
  const kubectl = kubectlCommands(w, availableChoices);
  const dismissedHere = (w.dismissed ?? []).find(
    (d) => !d.container || d.container === c.container,
  );
  const bt = c.backtest;

  const applyProfile = (p: RightsizingProfile) => {
    const s = snapshots?.[p];
    if (s) setCand({ cpu: s.cpu, mem: s.memory });
  };

  const dismiss = async () => {
    await api.dismissRightsizing(cluster, {
      namespace: w.namespace,
      kind: w.kind,
      name: w.name,
      vclusterNamespace: w.vclusterNamespace,
      container: w.containers.length > 1 ? c.container : '',
      reason,
      snoozeDays: snooze,
    });
    setDismissing(false);
    onChanged?.();
    onClose();
  };
  const undismiss = async () => {
    await api.undismissRightsizing(cluster, {
      namespace: w.namespace,
      kind: w.kind,
      name: w.name,
      vclusterNamespace: w.vclusterNamespace,
      container: dismissedHere?.container ?? '',
    });
    onChanged?.();
    onClose();
  };

  const sheet = (
    <div
      className="ap-overlay rs-overlay"
      onClick={onClose}
      role="presentation"
    >
      <div
        className="ap-sheet rs-sheet"
        role="dialog"
        aria-modal="true"
        aria-labelledby="rs-sheet-title"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="rs-sheet-header">
          <div className="rs-sheet-heading">
            <span className="wl-kind-badge">{w.kind}</span>
            <h3 className="ap-sheet-title" id="rs-sheet-title">
              {w.name}
            </h3>
            <span className="rs-sheet-ns">
              {w.vclusterNamespace
                ? `${w.namespace} › ${w.vclusterNamespace}`
                : w.namespace}
            </span>
            <VerdictBadge verdict={c.verdict} />
            {c.verdict !== 'insufficient-data' && (
              <ConfidenceMeter level={c.confidence} />
            )}
          </div>
          <button
            type="button"
            className="ap-icon-btn"
            onClick={onClose}
            aria-label="Close"
            title="Close"
          >
            <svg
              width="14"
              height="14"
              viewBox="0 0 16 16"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.6"
              strokeLinecap="round"
              aria-hidden="true"
            >
              <path d="M4 4l8 8M12 4l-8 8" />
            </svg>
          </button>
        </div>

        <div className="rs-sheet-body">
          {w.containers.length > 1 && (
            <div
              className="ap-segmented rs-containers"
              role="tablist"
              aria-label="Container"
            >
              {w.containers.map((x) => (
                <button
                  key={x.container}
                  role="tab"
                  aria-selected={x.container === c.container}
                  onClick={() => setActive(x.container)}
                >
                  <span
                    className={`ap-dot ap-dot--${x.verdict === 'under-provisioned' ? 'danger' : x.verdict === 'over-provisioned' ? 'info' : x.verdict === 'right-sized' ? 'success' : 'muted'}`}
                  />
                  {x.container}
                </button>
              ))}
            </div>
          )}

          {dismissedHere && (
            <div className="finops-error finops-info rs-inline-banner">
              <span>
                Dismissed: {dismissedHere.reason}
                {dismissedHere.until
                  ? `, until ${new Date(dismissedHere.until).toLocaleDateString()}`
                  : ''}
                .
              </span>
              <button className="finops-banner-action" onClick={undismiss}>
                Restore
              </button>
            </div>
          )}

          <ChangeSummary c={c} />

          {w.change && (
            <div className={`rs-change ${w.change.healthy ? 'ok' : 'bad'}`}>
              <div className="rs-change-title">
                {w.change.followedAdvice
                  ? 'You applied an earlier recommendation'
                  : 'Requests changed recently'}
              </div>
              <div>{w.change.summary}</div>
            </div>
          )}

          {c.verdict !== 'insufficient-data' && (
            <section className="rs-section">
              <div className="rs-section-head">
                <h4>Choose the requests</h4>
                <div
                  className="ap-segmented ap-segmented--sm"
                  role="group"
                  aria-label="Risk preset"
                >
                  {(Object.keys(PROFILE_META) as RightsizingProfile[]).map(
                    (p) => {
                      const s = snapshots?.[p];
                      const on =
                        !!s &&
                        Math.abs(s.cpu - cand.cpu) < 1e-9 &&
                        Math.abs(s.memory - cand.mem) < 1;
                      return (
                        <button
                          key={p}
                          aria-pressed={on}
                          disabled={!s}
                          onClick={() => applyProfile(p)}
                        >
                          {PROFILE_META[p].label}
                        </button>
                      );
                    },
                  )}
                </div>
              </div>

              <div className="rs-range-legend">
                <span className="rs-range-low">Too low</span>
                <span className="rs-range-caution">Reduced headroom</span>
                <span className="rs-range-recommended">
                  Recommended or higher
                </span>
              </div>

              <div className="rs-slider-row">
                <div className="rs-slider-label">
                  <span>CPU request</span>
                  <strong>{formatCores(cand.cpu)}</strong>
                  {c.cpu.request > 0 && (
                    <span className="rs-muted">
                      now {formatCores(c.cpu.request)}
                    </span>
                  )}
                </div>
                <div className="rs-range-control">
                  <input
                    type="range"
                    className="rs-range rs-range-cpu"
                    style={rangeStyle(
                      c.cpu.recommended,
                      cpuCautionFloor,
                      cpuLo,
                      cpuHi,
                    )}
                    min={0}
                    max={1000}
                    value={
                      drag.cpu ??
                      toSlider(cand.cpu, cpuLo, cpuHi, c.cpu.recommended)
                    }
                    onChange={(e) => {
                      const pos = Number(e.target.value);
                      setDrag({ cpu: pos });
                      setCand({
                        cpu: snapCPU(
                          fromSlider(pos, cpuLo, cpuHi, c.cpu.recommended),
                        ),
                      });
                    }}
                    onPointerUp={endDrag}
                    onKeyUp={endDrag}
                    onBlur={endDrag}
                    aria-label="CPU request"
                    aria-valuetext={`${formatCores(cand.cpu)}: ${rangeStatus(cand.cpu, c.cpu.recommended, cpuCautionFloor)}`}
                  />
                  <CurrentRequestMarker
                    value={c.cpu.request}
                    lo={cpuLo}
                    hi={cpuHi}
                    recommended={c.cpu.recommended}
                    label={formatCores(c.cpu.request)}
                  />
                </div>
                <div className="rs-readouts">
                  <span className={ev.cpuTimeAbove > 2 * target ? 'warn' : ''}>
                    Above it <strong>{formatPct(ev.cpuTimeAbove)}</strong> of
                    replica-time
                    <span className="rs-muted">
                      {' '}
                      (target {formatPct(target, 0)})
                    </span>
                  </span>
                  {c.cpu.burst && (
                    <span
                      className={
                        ev.cpuTimeAbove / c.cpu.burst.activeShare > 0.5
                          ? 'warn'
                          : ''
                      }
                    >
                      Covers{' '}
                      <strong>
                        {formatPct(
                          Math.max(
                            0,
                            1 - ev.cpuTimeAbove / c.cpu.burst.activeShare,
                          ),
                          0,
                        )}
                      </strong>{' '}
                      of busy time
                    </span>
                  )}
                  {c.cpu.limit > 0 && cand.cpu > c.cpu.limit && (
                    <span className="warn">
                      Above the {formatCores(c.cpu.limit)} limit: raise it too
                    </span>
                  )}
                  {c.hpa?.resource === 'cpu' &&
                    c.cpu.request > 0 &&
                    (() => {
                      const t = Math.round(
                        (c.hpa.targetUtilization * c.cpu.request) / cand.cpu,
                      );
                      return (
                        <span className={t > 90 ? 'warn' : ''}>
                          Keeps today's scaling at an HPA target of{' '}
                          <strong>{t}%</strong>
                        </span>
                      );
                    })()}
                </div>
                {c.cpu.burst && (
                  <div className="rs-burst">
                    <span>
                      Bursty: busy {formatPct(c.cpu.burst.activeShare)} of the
                      time, peaking at {formatCores(c.cpu.burst.peak)}. The
                      request covers the bursts; sized to idle time it would be{' '}
                      {formatCores(c.cpu.burst.idleRecommended)}, and bursts
                      would run on spare node CPU only.
                    </span>
                    {Math.abs(cand.cpu - c.cpu.burst.idleRecommended) > 1e-9 ? (
                      <button
                        className="ap-btn ap-btn--sm"
                        onClick={() =>
                          setCand({ cpu: c.cpu.burst!.idleRecommended })
                        }
                      >
                        Use idle-sized{' '}
                        {formatCores(c.cpu.burst.idleRecommended)}
                      </button>
                    ) : (
                      <button
                        className="ap-btn ap-btn--sm"
                        onClick={() => setCand({ cpu: c.cpu.recommended })}
                      >
                        Back to burst-sized {formatCores(c.cpu.recommended)}
                      </button>
                    )}
                  </div>
                )}
              </div>

              <div className="rs-slider-row">
                <div className="rs-slider-label">
                  <span>Memory request and limit</span>
                  <strong>{formatMem(cand.mem)}</strong>
                  {c.memory.request > 0 && (
                    <span className="rs-muted">
                      now {formatMem(c.memory.request)}
                      {c.memory.limit && c.memory.limit !== c.memory.request
                        ? ` / ${formatMem(c.memory.limit)}`
                        : ''}
                    </span>
                  )}
                </div>
                <div className="rs-range-control">
                  <input
                    type="range"
                    className="rs-range rs-range-mem"
                    style={rangeStyle(
                      c.memory.recommended,
                      memCautionFloor,
                      memLo,
                      memHi,
                    )}
                    min={0}
                    max={1000}
                    value={
                      drag.mem ??
                      toSlider(cand.mem, memLo, memHi, c.memory.recommended)
                    }
                    onChange={(e) => {
                      const pos = Number(e.target.value);
                      setDrag({ mem: pos });
                      setCand({
                        mem: snapMem(
                          fromSlider(pos, memLo, memHi, c.memory.recommended),
                        ),
                      });
                    }}
                    onPointerUp={endDrag}
                    onKeyUp={endDrag}
                    onBlur={endDrag}
                    aria-label="Memory request and limit"
                    aria-valuetext={`${formatMem(cand.mem)}: ${rangeStatus(cand.mem, c.memory.recommended, memCautionFloor)}`}
                  />
                  <CurrentRequestMarker
                    value={c.memory.request}
                    lo={memLo}
                    hi={memHi}
                    recommended={c.memory.recommended}
                    label={formatMem(c.memory.request)}
                  />
                </div>
                <div className="rs-readouts">
                  {ev.memPeakHeadroom < 0 ? (
                    <span className="danger">
                      <ExclamationTriangleIcon /> Below the observed peak: as a
                      limit it would have been OOM-killed
                    </span>
                  ) : (
                    <span>
                      <strong>{formatPct(ev.memPeakHeadroom, 0)}</strong>{' '}
                      headroom over the {formatMem(c.memory.peak)} peak
                    </span>
                  )}
                  {dist && dist.dailyMemPeaks.length > 0 && (
                    <span className={ev.memDaysOver > 0 ? 'danger' : ''}>
                      {ev.memDaysOver} of {dist.dailyMemPeaks.length} days
                      peaked above it
                    </span>
                  )}
                  {c.oomKills > 0 && cand.mem <= c.memory.limit && (
                    <span className="danger">
                      At or under the limit it was OOM-killed at
                    </span>
                  )}
                </div>
              </div>

              <div className="rs-candidate-total">
                {c.cpuMonthly > 0 || c.memMonthly > 0 ? (
                  <span className={ev.monthlyDelta >= 0 ? 'save' : 'add'}>
                    {ev.monthlyDelta >= 0 ? 'Saves' : 'Adds'}{' '}
                    <strong>{formatMoney(Math.abs(ev.monthlyDelta))}/mo</strong>
                    <span className="rs-muted">
                      {' '}
                      for this container across{' '}
                      {c.avgReplicas.toFixed(c.avgReplicas < 10 ? 1 : 0)}{' '}
                      replicas on average
                    </span>
                  </span>
                ) : (
                  <span className="rs-muted">
                    No node prices for this cluster, so no dollar figure.
                  </span>
                )}
                {!isRec && (
                  <button
                    className="ap-btn ap-btn--sm"
                    onClick={() =>
                      setCand({
                        cpu: c.cpu.recommended,
                        mem: c.memory.recommended,
                      })
                    }
                  >
                    Back to the recommendation
                  </button>
                )}
              </div>
            </section>
          )}

          {error && (
            <div className="finops-error" role="alert">
              <ExclamationTriangleIcon />
              <span>{error}</span>
            </div>
          )}
          {!evidence && !error && (
            <div className="rs-sheet-loading">
              <div className="ap-spinner" /> Loading {parseInt(window, 10)} days
              of history…
            </div>
          )}

          {hourly && (
            <div className="rs-evidence-charts">
              <section className="rs-section">
                <div className="rs-section-head">
                  <h4>CPU</h4>
                </div>
                <ChartLegend
                  resource="cpu"
                  items={[
                    { label: 'Median–P95 across replicas', swatch: 'band' },
                    ...(c.avgReplicas >= 1.5
                      ? [{ label: 'Busiest replica', swatch: 'thin' as const }]
                      : []),
                    ...(c.cpu.request > 0
                      ? [
                          {
                            label: 'Current request',
                            swatch: 'current' as const,
                            value: formatCores(c.cpu.request),
                          },
                        ]
                      : []),
                    {
                      label: 'Candidate',
                      swatch: 'candidate',
                      value: formatCores(cand.cpu),
                    },
                    ...(c.cpu.limit > 0
                      ? [
                          {
                            label: cpuLimitOnChart
                              ? 'Limit'
                              : 'Limit (off chart)',
                            swatch: 'limit' as const,
                            value: formatCores(c.cpu.limit),
                          },
                        ]
                      : []),
                  ]}
                />
                <CPUChart
                  hourly={hourly}
                  refs={cpuRefs}
                  events={events}
                  multiReplica={c.avgReplicas >= 1.5}
                />
              </section>

              <section className="rs-section">
                <div className="rs-section-head">
                  <h4>Memory</h4>
                </div>
                <ChartLegend
                  resource="memory"
                  items={[
                    { label: 'Peak', swatch: 'line' },
                    ...(c.memory.request > 0
                      ? [
                          {
                            label: 'Current request',
                            swatch: 'current' as const,
                            value: formatMem(c.memory.request),
                          },
                        ]
                      : []),
                    {
                      label: 'Candidate',
                      swatch: 'candidate',
                      value: formatMem(cand.mem),
                    },
                    ...(memLimitShown
                      ? [
                          {
                            label: memLimitOnChart
                              ? 'Current limit'
                              : 'Current limit (off chart)',
                            swatch: 'limit' as const,
                            value: formatMem(c.memory.limit),
                          },
                        ]
                      : []),
                  ]}
                />
                <MemoryChart hourly={hourly} refs={memRefs} events={events} />
              </section>
              {events.length > 0 && (
                <div
                  className="rs-event-key"
                  role="group"
                  aria-label="Events on CPU and memory charts"
                >
                  <span>
                    <span className="rs-mark oom" /> OOM kill
                  </span>
                  <span>
                    <span className="rs-mark restart" /> Restart
                  </span>
                  <span>
                    <span className="rs-mark shift" /> Behaviour changed
                  </span>
                  {events.some((e) => e.kind === 'request-change') && (
                    <span>
                      <span className="rs-mark request-change" /> Request
                      changed
                    </span>
                  )}
                </div>
              )}
            </div>
          )}

          <section className="rs-section">
            <div className="rs-section-head">
              <h4>Apply it yourself</h4>
              {availableChoices.length > 0 && (
                <div className="ap-segmented ap-segmented--sm" role="tablist">
                  <button
                    role="tab"
                    aria-selected={output === 'yaml'}
                    onClick={() => setOutput('yaml')}
                  >
                    Patch YAML
                  </button>
                  <button
                    role="tab"
                    aria-selected={output === 'kubectl'}
                    onClick={() => setOutput('kubectl')}
                    disabled={!kubectl}
                  >
                    kubectl
                  </button>
                  <button
                    role="tab"
                    aria-selected={output === 'ai'}
                    onClick={() => setOutput('ai')}
                  >
                    AI prompt
                  </button>
                </div>
              )}
            </div>
            {availableChoices.length > 0 ? (
              <>
                {output === 'ai' && (
                  <p className="rs-note">
                    Copy this prompt into your AI coding assistant with your
                    repository open. It includes the selected values and asks
                    the AI to update the source configuration.
                  </p>
                )}
                <CopyBlock
                  key={output}
                  text={
                    output === 'ai'
                      ? repositoryPrompt(w, availableChoices, cluster)
                      : output === 'kubectl' && kubectl
                        ? kubectl
                        : yaml
                  }
                  label={output === 'ai' ? 'AI prompt' : output}
                  prose={output === 'ai'}
                />
              </>
            ) : (
              <p className="rs-note">
                No resource values are available yet. Usage history is needed
                before Kanivet can suggest requests and limits.
              </p>
            )}
            {c.hpa && (
              <>
                <div className="rs-subhead">
                  And in HorizontalPodAutoscaler {c.hpa.name}, together with the
                  request
                </div>
                <CopyBlock
                  label="HPA target"
                  text={`# spec.metrics, the ${c.hpa.resource} entry (now ${c.hpa.targetUtilization}%)
target:
  type: Utilization
  averageUtilization: ${c.hpa.suggestedTarget}`}
                />
              </>
            )}
            {c.startupBoost &&
              !c.startupBoost.floor &&
              cand.cpu < c.startupBoost.request && (
                <>
                  <div className="rs-subhead">
                    And a startup boost, so starting keeps its CPU
                  </div>
                  <p className="rs-note">
                    It uses about {formatCores(c.startupBoost.startupRate)} in
                    its first minutes, more than the new {formatCores(cand.cpu)}{' '}
                    request. On a busy node a request is the CPU share a
                    container gets, so with the new one startup could take long
                    enough for probes to give up. This gives it{' '}
                    {formatCores(c.startupBoost.request)} while it starts and
                    returns it to {formatCores(cand.cpu)} once the pod is Ready,
                    resized in place
                    {c.startupBoost.inPlace
                      ? ' (Kubernetes 1.33+, which this cluster runs)'
                      : ' (needs Kubernetes 1.33+)'}
                    . Install the controller once with{' '}
                    <code>{STARTUP_BOOST_INSTALL}</code>. If you run the VPA
                    (1.7+), its <code>startupBoost</code> setting does the same.
                    {w.vclusterNamespace
                      ? ' This workload runs in a vcluster: the controller and this object go inside it, and the vcluster must pass in-place resizes through to the host pod, so check that your vcluster version supports it.'
                      : ''}
                  </p>
                  <CopyBlock
                    label="StartupCPUBoost"
                    text={startupBoostYAML(
                      w,
                      c.container,
                      c.startupBoost,
                      c.cpu.limit,
                    )}
                  />
                </>
              )}
            {c.startupBoost?.floor && (
              <p className="rs-note">
                CPU stays at {formatCores(c.cpu.recommended)}, its startup rate,
                rather than {formatCores(c.startupBoost.steadyRecommended ?? 0)}
                : this cluster can't resize pods in place (that needs Kubernetes
                1.33+), so there is no way to give it more CPU only while it
                starts. After an upgrade, a startup boost lets the request go
                down to the steady-state value.
              </p>
            )}
            <p className="rs-note">
              Kanivet never changes the cluster. If this workload is deployed by
              Helm or GitOps, put these values there, or the next sync puts the
              old ones back.
              {w.vclusterNamespace
                ? ' This workload lives inside a vcluster: run the command against the vcluster, not the host.'
                : ''}
            </p>
          </section>

          {c.verdict !== 'insufficient-data' && (
            <details className="rs-section rs-details">
              <summary className="rs-section-head">
                <h4>Why this number</h4>
                <span className="rs-muted">
                  {bt
                    ? bt.calibrated
                      ? 'backtest passed'
                      : 'backtest missed'
                    : 'method and data'}
                </span>
              </summary>
              <div className="rs-why-content">
                <div className="rs-why-grid">
                  <div className="rs-why-resource">
                    <div className="rs-why-title">
                      <span>CPU</span>
                      <strong>{formatCores(c.cpu.recommended)}</strong>
                    </div>
                    <ol className="rs-steps">
                      {(c.cpu.explain ?? []).map((s, i) => (
                        <li key={i}>{s}</li>
                      ))}
                    </ol>
                  </div>
                  <div className="rs-why-resource">
                    <div className="rs-why-title">
                      <span>Memory</span>
                      <strong>{formatMem(c.memory.recommended)}</strong>
                    </div>
                    <ol className="rs-steps">
                      {(c.memory.explain ?? []).map((s, i) => (
                        <li key={i}>{s}</li>
                      ))}
                    </ol>
                  </div>
                </div>
                {bt && (
                  <div
                    className={`rs-backtest ${bt.calibrated ? 'ok' : 'bad'}`}
                  >
                    <div className="rs-backtest-title">
                      {bt.calibrated ? (
                        <CheckIcon />
                      ) : (
                        <ExclamationTriangleIcon />
                      )}
                      Tested on days it never saw
                    </div>
                    {bt.folds > 1 ? (
                      <p>
                        Replayed week by week: for each of the last {bt.folds}{' '}
                        weeks, re-fitted on every day before it, then scored on
                        that week. CPU sat above what it would have recommended{' '}
                        <strong>{formatPct(bt.cpuExceedance)}</strong> of the{' '}
                        {bt.bursty ? 'busy ' : ''}time (target{' '}
                        {formatPct(bt.cpuTarget, 0)}). Memory went over in{' '}
                        <strong>
                          {bt.memBreaches} of {bt.folds}
                        </strong>{' '}
                        weeks; the closest week peaked at{' '}
                        {formatMem(bt.memTestPeak)} against{' '}
                        {formatMem(bt.memRecommended)}.
                        {!bt.calibrated &&
                          ' Some weeks looked different from the ones before, so treat this one with care.'}
                      </p>
                    ) : (
                      <p>
                        Re-fitted on the first {bt.trainDays} days, then
                        replayed over the next {bt.testDays}: CPU sat above the{' '}
                        {formatCores(bt.cpuRecommended)} it would have
                        recommended{' '}
                        <strong>{formatPct(bt.cpuExceedance)}</strong> of the{' '}
                        {bt.bursty ? 'busy ' : ''}time (target{' '}
                        {formatPct(bt.cpuTarget, 0)}), and memory peaked at{' '}
                        {formatMem(bt.memTestPeak)} against{' '}
                        {formatMem(bt.memRecommended)}
                        {bt.memBreached ? ', over the line.' : ', under it.'}
                        {!bt.calibrated &&
                          ' The recent days looked different from the earlier ones, so treat this one with care.'}
                      </p>
                    )}
                  </div>
                )}
                <div className="rs-quality">
                  <div className="rs-subhead">
                    Data behind the recommendation
                  </div>
                  <dl className="rs-quality-stats">
                    <div>
                      <dt>Observed</dt>
                      <dd>{c.data.days.toFixed(1)} days</dd>
                    </div>
                    <div>
                      <dt>Coverage</dt>
                      <dd>{formatPct(c.data.coverage, 0)}</dd>
                    </div>
                    <div>
                      <dt>Samples</dt>
                      <dd>{c.data.samples.toLocaleString()}</dd>
                    </div>
                    {c.data.runs ? (
                      <div>
                        <dt>Runs</dt>
                        <dd>{c.data.runs}</dd>
                      </div>
                    ) : null}
                  </dl>
                  {c.data.newPeakChance ? (
                    <p className="rs-quality-note">
                      <strong>Memory outlook</strong>
                      {formatPct(c.data.newPeakChance, 0)} chance the next 30
                      days beat the past memory peak (the headroom covers it)
                    </p>
                  ) : null}
                  {c.imbalance && c.imbalance > 1.2 ? (
                    <p className="rs-quality-note">
                      <strong>Replica balance</strong>
                      Busiest pod at {c.imbalance.toFixed(1)}× the typical
                    </p>
                  ) : null}
                </div>
              </div>
            </details>
          )}
        </div>

        <div className="ap-sheet-footer rs-sheet-footer">
          {dismissing ? (
            <div className="rs-dismiss-form">
              <select
                className="ap-select"
                value={reason}
                onChange={(e) => setReason(e.target.value)}
                aria-label="Reason"
              >
                {DISMISS_REASONS.map((r) => (
                  <option key={r}>{r}</option>
                ))}
              </select>
              <select
                className="ap-select"
                value={snooze}
                onChange={(e) => setSnooze(Number(e.target.value))}
                aria-label="For how long"
              >
                <option value={30}>for 30 days</option>
                <option value={90}>for 90 days</option>
                <option value={0}>for good</option>
              </select>
              <button className="ap-btn" onClick={() => setDismissing(false)}>
                Cancel
              </button>
              <button className="ap-btn ap-btn--primary" onClick={dismiss}>
                Dismiss
              </button>
            </div>
          ) : (
            <>
              {!dismissedHere &&
                c.verdict !== 'right-sized' &&
                c.verdict !== 'insufficient-data' && (
                  <button
                    className="ap-btn"
                    onClick={() => setDismissing(true)}
                  >
                    Dismiss{w.containers.length > 1 ? ` ${c.container}` : ''}…
                  </button>
                )}
              <button className="ap-btn ap-btn--primary" onClick={onClose}>
                Done
              </button>
            </>
          )}
        </div>
      </div>
    </div>
  );
  return createPortal(sheet, document.body);
};
