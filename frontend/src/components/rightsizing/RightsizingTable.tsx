import React, { useEffect, useMemo, useRef, useState } from 'react';
import {
  CheckIcon,
  ChevronDownIcon,
  ChevronRightIcon,
  ExternalLinkIcon,
} from '@radix-ui/react-icons';
import type { WorkloadReport } from '../../types/rightsizing';
import { Tooltip } from '../common/Tooltip';
import { ConfidenceMeter, ResourceDelta } from './RightsizingParts';
import {
  VERDICT_META,
  formatMoney,
  formatPct,
  isDismissed,
  namespaceOf,
  pctChange,
  primaryContainer,
  reasonTags,
  releaseOf,
  type GroupKey,
  type WorkloadGroup,
} from './rightsizingView';

export const workloadId = (w: WorkloadReport) =>
  `${w.namespace}/${w.vclusterNamespace ?? ''}/${w.kind}/${w.name}`;

const ROWS_PER_GROUP = 10;
const ROWS_FLAT = 100;

const Delta: React.FC<{
  resource: 'cpu' | 'memory';
  from: number;
  to: number;
}> = ({ resource, from, to }) => {
  const pct = pctChange(from, to);
  return (
    <span className="rs-delta-cell">
      <ResourceDelta resource={resource} from={from} to={to} />
      {from > 0 && Math.abs(pct) >= 0.02 && (
        <span className={`rs-pct ${pct < 0 ? 'down' : 'up'}`}>
          {pct > 0 ? '+' : '−'}
          {formatPct(Math.abs(pct), 0)}
        </span>
      )}
    </span>
  );
};

const Breakdown: React.FC<{ w: WorkloadReport }> = ({ w }) => (
  <div className="rs-breakdown">
    {w.containers.map((c) => (
      <div key={c.container} className="rs-breakdown-row">
        <span className="rs-breakdown-name">{c.container}</span>
        <ResourceDelta
          resource="cpu"
          from={c.cpu.request}
          to={c.cpu.recommended}
        />
        <ResourceDelta
          resource="memory"
          from={c.memory.request}
          to={c.memory.recommended}
        />
      </div>
    ))}
  </div>
);

interface RowProps {
  w: WorkloadReport;
  cursor: boolean;
  selected: boolean;
  showNamespace: boolean;
  onOpen: () => void;
  onToggle: () => void;
  onOpenWorkload?: () => void;
}

const Row: React.FC<RowProps> = ({
  w,
  cursor,
  selected,
  showNamespace,
  onOpen,
  onToggle,
  onOpenWorkload,
}) => {
  const p = primaryContainer(w);
  const others = w.containers.length - 1;
  const { tags, more } = reasonTags(w);
  const meta = VERDICT_META[w.verdict];
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (cursor) ref.current?.scrollIntoView({ block: 'nearest' });
  }, [cursor]);
  const release = releaseOf(w);
  return (
    <div
      ref={ref}
      className={`rs-row${cursor ? ' is-cursor' : ''}${selected ? ' is-selected' : ''}${isDismissed(w) ? ' is-dismissed' : ''}`}
      role="row"
      aria-selected={selected}
      onClick={onOpen}
    >
      <div className="rs-cell rs-cell-check" role="cell">
        <button
          className={`rs-check${selected ? ' on' : ''}`}
          onClick={(e) => {
            e.stopPropagation();
            onToggle();
          }}
          aria-label={selected ? `Deselect ${w.name}` : `Select ${w.name}`}
        >
          {selected && <CheckIcon />}
        </button>
      </div>
      <div className="rs-cell rs-cell-name" role="cell">
        <div className="rs-name-block">
          <span className="rs-name">
            {w.name}
            {onOpenWorkload && (
              <button
                className="row-open-btn"
                onClick={(e) => {
                  e.stopPropagation();
                  onOpenWorkload();
                }}
                title={`Open ${w.name}`}
                aria-label={`Open ${w.name}`}
              >
                <ExternalLinkIcon />
              </button>
            )}
          </span>
          <span className="rs-sub">
            {w.kind}
            {showNamespace && <> · {namespaceOf(w)}</>}
            {release && release !== w.name && <> · {release}</>}
          </span>
        </div>
      </div>
      <div className="rs-cell rs-cell-verdict" role="cell">
        <span className={`rs-chip rs-tone-${meta.tone}`}>{meta.short}</span>
      </div>
      <div className="rs-cell rs-cell-tags" role="cell">
        {tags.map((t) => (
          <Tooltip key={t.title} content={t.message}>
            <span className={`rs-tag rs-tag-${t.severity}`}>{t.title}</span>
          </Tooltip>
        ))}
        {more > 0 && <span className="rs-tag rs-tag-more">+{more}</span>}
        {w.change && (
          <Tooltip content={w.change.summary}>
            <span
              className={`rs-tag ${w.change.healthy ? 'rs-tag-good' : 'rs-tag-warning'}`}
            >
              {w.change.followedAdvice ? 'Applied' : 'Changed'}
            </span>
          </Tooltip>
        )}
      </div>
      <div className="rs-cell rs-cell-res rs-cell-cpu" role="cell">
        {p && (
          <Delta resource="cpu" from={p.cpu.request} to={p.cpu.recommended} />
        )}
      </div>
      <div className="rs-cell rs-cell-res rs-cell-mem" role="cell">
        {p && (
          <Delta
            resource="memory"
            from={p.memory.request}
            to={p.memory.recommended}
          />
        )}
        {others > 0 && (
          <Tooltip content={<Breakdown w={w} />}>
            <span
              className="rs-more-containers"
              onClick={(e) => e.stopPropagation()}
            >
              +{others}
            </span>
          </Tooltip>
        )}
      </div>
      <div className="rs-cell rs-cell-money" role="cell">
        {w.priced && Math.abs(w.monthlySavings) >= 0.01 ? (
          <span
            className={w.monthlySavings > 0 ? 'rs-money-save' : 'rs-money-add'}
          >
            {w.monthlySavings > 0
              ? formatMoney(w.monthlySavings)
              : `+${formatMoney(-w.monthlySavings)}`}
          </span>
        ) : (
          <span className="rs-muted">—</span>
        )}
      </div>
      <div className="rs-cell rs-cell-conf" role="cell">
        {w.verdict !== 'insufficient-data' && (
          <ConfidenceMeter level={w.confidence} label={false} />
        )}
      </div>
    </div>
  );
};

interface Props {
  groups: WorkloadGroup[];
  groupBy: GroupKey;
  selected: Set<string>;
  onToggle: (id: string) => void;
  onToggleMany: (ids: string[], on: boolean) => void;
  onOpen: (w: WorkloadReport) => void;
  openWorkload?: (w: WorkloadReport) => (() => void) | undefined;
  emptyText: string;
}

/** The triage table: grouped or flat, keyboard-driven (j/k, Enter, x). */
export const RightsizingTable: React.FC<Props> = ({
  groups,
  groupBy,
  selected,
  onToggle,
  onToggleMany,
  onOpen,
  openWorkload,
  emptyText,
}) => {
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [cursor, setCursor] = useState<string | null>(null);

  const limit = groupBy === 'none' ? ROWS_FLAT : ROWS_PER_GROUP;
  const visible = useMemo(
    () =>
      groups.map((g) => ({
        ...g,
        shown: collapsed.has(g.key)
          ? []
          : expanded.has(g.key)
            ? g.workloads
            : g.workloads.slice(0, limit),
      })),
    [groups, collapsed, expanded, limit],
  );
  const flat = useMemo(() => visible.flatMap((g) => g.shown), [visible]);
  const allIds = useMemo(
    () => groups.flatMap((g) => g.workloads.map(workloadId)),
    [groups],
  );
  const allSelected =
    allIds.length > 0 && allIds.every((id) => selected.has(id));

  const onKeyDown = (e: React.KeyboardEvent) => {
    if ((e.target as HTMLElement).closest('input, select, textarea')) return;
    const i = flat.findIndex((w) => workloadId(w) === cursor);
    const move = (d: number) => {
      const next =
        flat[Math.min(Math.max((i < 0 ? -1 : i) + d, 0), flat.length - 1)];
      if (next) setCursor(workloadId(next));
      e.preventDefault();
    };
    switch (e.key) {
      case 'j':
      case 'ArrowDown':
        move(1);
        break;
      case 'k':
      case 'ArrowUp':
        move(-1);
        break;
      case 'Enter':
        if (i >= 0) {
          onOpen(flat[i]);
          e.preventDefault();
        }
        break;
      case 'x':
      case ' ':
        if (i >= 0) {
          onToggle(workloadId(flat[i]));
          e.preventDefault();
        }
        break;
    }
  };

  const toggleSet = (
    set: Set<string>,
    key: string,
    update: (s: Set<string>) => void,
  ) => {
    const next = new Set(set);
    if (next.has(key)) next.delete(key);
    else next.add(key);
    update(next);
  };

  return (
    <div
      className="rs-table"
      role="table"
      aria-label="Rightsizing recommendations"
      tabIndex={0}
      onKeyDown={onKeyDown}
    >
      <div className="rs-row rs-head" role="row">
        <div className="rs-cell rs-cell-check" role="columnheader">
          <button
            className={`rs-check${allSelected ? ' on' : ''}`}
            onClick={() => onToggleMany(allIds, !allSelected)}
            aria-label={allSelected ? 'Deselect all' : 'Select all shown'}
          >
            {allSelected && <CheckIcon />}
          </button>
        </div>
        <div className="rs-cell rs-cell-name" role="columnheader">
          Workload
        </div>
        <div className="rs-cell rs-cell-verdict" role="columnheader">
          Verdict
        </div>
        <div className="rs-cell rs-cell-tags" role="columnheader">
          Why
        </div>
        <div className="rs-cell rs-cell-res rs-cell-cpu" role="columnheader">
          CPU
        </div>
        <div className="rs-cell rs-cell-res rs-cell-mem" role="columnheader">
          Memory
        </div>
        <div className="rs-cell rs-cell-money" role="columnheader">
          $/mo
        </div>
        <div className="rs-cell rs-cell-conf" role="columnheader">
          Conf.
        </div>
      </div>
      {groups.length === 0 && <div className="rs-empty-rows">{emptyText}</div>}
      {visible.map((g) => (
        <div key={g.key || 'all'} className="rs-group">
          {groupBy !== 'none' && (
            <button
              className="rs-group-header"
              onClick={() => toggleSet(collapsed, g.key, setCollapsed)}
              aria-expanded={!collapsed.has(g.key)}
            >
              {collapsed.has(g.key) ? (
                <ChevronRightIcon />
              ) : (
                <ChevronDownIcon />
              )}
              <span className="rs-group-name">{g.key}</span>
              <span className="rs-group-meta">
                {g.workloads.length} workload
                {g.workloads.length === 1 ? '' : 's'}
                {g.atRisk > 0 && (
                  <span className="rs-group-risk"> · {g.atRisk} at risk</span>
                )}
              </span>
              {g.savings > 0 && (
                <span className="rs-group-savings">
                  {formatMoney(g.savings)}/mo
                </span>
              )}
            </button>
          )}
          {g.shown.map((w) => {
            const id = workloadId(w);
            return (
              <Row
                key={id}
                w={w}
                cursor={cursor === id}
                selected={selected.has(id)}
                showNamespace={groupBy !== 'namespace'}
                onOpen={() => {
                  setCursor(id);
                  onOpen(w);
                }}
                onToggle={() => onToggle(id)}
                onOpenWorkload={openWorkload?.(w)}
              />
            );
          })}
          {!collapsed.has(g.key) && g.workloads.length > limit && (
            <button
              className="rs-more"
              onClick={() => toggleSet(expanded, g.key, setExpanded)}
            >
              {expanded.has(g.key)
                ? 'Show fewer'
                : `Show ${g.workloads.length - limit} more`}
            </button>
          )}
        </div>
      ))}
    </div>
  );
};
