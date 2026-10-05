import React, {
  memo,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import { useVirtualizer } from '@tanstack/react-virtual';
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
  workloadId,
  type GroupKey,
  type WorkloadGroup,
} from './rightsizingView';

const ROWS_PER_GROUP = 10;
const ROWS_FLAT = 100;
/** First guesses at item heights; each item is measured once drawn. */
const ESTIMATE = { group: 30, row: 41, more: 28 } as const;
/** Until the scroller is measured: a screen, so the first render has rows. */
const FIRST_SCREEN = { width: 0, height: 1000 };

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
  canOpenWorkload: boolean;
  onOpen: (w: WorkloadReport) => void;
  onToggle: (id: string) => void;
  onOpenWorkload?: (w: WorkloadReport) => void;
}

/** Memoised, with callbacks that never change: a cursor move, a selection or
 * a report poll re-renders the rows that changed, not every row. */
const Row = memo(function Row({
  w,
  cursor,
  selected,
  showNamespace,
  canOpenWorkload,
  onOpen,
  onToggle,
  onOpenWorkload,
}: RowProps) {
  const p = primaryContainer(w);
  const others = w.containers.length - 1;
  const { tags, more } = reasonTags(w);
  const meta = VERDICT_META[w.verdict];
  // Tooltips mount on the row's first hover: a Radix tooltip per tag on
  // every row was about half the cost of drawing the table.
  const [hovered, setHovered] = useState(false);
  const tip = (content: React.ReactNode) => (hovered ? content : null);
  const release = releaseOf(w);
  return (
    <div
      className={`rs-row${cursor ? ' is-cursor' : ''}${selected ? ' is-selected' : ''}${isDismissed(w) ? ' is-dismissed' : ''}`}
      role="row"
      aria-selected={selected}
      onClick={() => onOpen(w)}
      onPointerEnter={hovered ? undefined : () => setHovered(true)}
    >
      <div className="rs-cell rs-cell-check" role="cell">
        <button
          className={`rs-check${selected ? ' on' : ''}`}
          onClick={(e) => {
            e.stopPropagation();
            onToggle(workloadId(w));
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
            {canOpenWorkload && onOpenWorkload && (
              <button
                className="row-open-btn"
                onClick={(e) => {
                  e.stopPropagation();
                  onOpenWorkload(w);
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
          <Tooltip key={t.title} content={tip(t.message)}>
            <span className={`rs-tag rs-tag-${t.severity}`}>{t.title}</span>
          </Tooltip>
        ))}
        {more > 0 && <span className="rs-tag rs-tag-more">+{more}</span>}
        {w.change && (
          <Tooltip content={tip(w.change.summary)}>
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
          <Tooltip content={tip(<Breakdown w={w} />)}>
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
          <ConfidenceMeter
            level={w.confidence}
            label={false}
            tooltip={hovered}
          />
        )}
      </div>
    </div>
  );
});

type VisibleGroup = WorkloadGroup & { shown: WorkloadReport[] };

/** What the table draws, top to bottom, as one list: only what is in view
 * is rendered. */
type Item =
  | { kind: 'group'; key: string; g: VisibleGroup }
  | { kind: 'row'; key: string; w: WorkloadReport }
  | { kind: 'more'; key: string; g: VisibleGroup };

interface Props {
  groups: WorkloadGroup[];
  groupBy: GroupKey;
  selected: Set<string>;
  onToggle: (id: string) => void;
  onToggleMany: (ids: string[], on: boolean) => void;
  onOpen: (w: WorkloadReport) => void;
  /** Whether a row links to its workload's detail view. */
  canOpenWorkload?: (w: WorkloadReport) => boolean;
  onOpenWorkload?: (w: WorkloadReport) => void;
  /** The element the table scrolls in. */
  scrollRef: React.RefObject<HTMLElement>;
  /** While a sheet is open over the table, the keyboard is the sheet's. */
  keyboardDisabled?: boolean;
  emptyText: string;
}

/** The triage table: grouped or flat, keyboard-driven (j/k, Enter, x). */
export const RightsizingTable = memo(function RightsizingTable({
  groups,
  groupBy,
  selected,
  onToggle,
  onToggleMany,
  onOpen,
  canOpenWorkload,
  onOpenWorkload,
  scrollRef,
  keyboardDisabled,
  emptyText,
}: Props) {
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
  const items = useMemo(() => {
    const out: Item[] = [];
    for (const g of visible) {
      if (groupBy !== 'none')
        out.push({ kind: 'group', key: `group:${g.key}`, g });
      for (const w of g.shown) out.push({ kind: 'row', key: workloadId(w), w });
      if (!collapsed.has(g.key) && g.workloads.length > limit)
        out.push({ kind: 'more', key: `more:${g.key}`, g });
    }
    return out;
  }, [visible, groupBy, collapsed, limit]);
  const allIds = useMemo(
    () => groups.flatMap((g) => g.workloads.map(workloadId)),
    [groups],
  );
  const allSelected =
    allIds.length > 0 && allIds.every((id) => selected.has(id));

  // The rows scroll with the page above them, so the list starts some way
  // down its scroller, and moves as filters and pills come and go. Measured
  // after the commit: the scroller is an ancestor, and when both mount
  // together (a revisit with the report held) React attaches its ref only
  // after this table's layout effects have run.
  const list = useRef<HTMLDivElement>(null);
  const [scrollMargin, setScrollMargin] = useState(0);
  useEffect(() => {
    const el = list.current;
    const scroller = scrollRef.current;
    if (!el || !scroller) return;
    const measure = () => {
      const top = Math.round(
        el.getBoundingClientRect().top -
          scroller.getBoundingClientRect().top -
          scroller.clientTop +
          scroller.scrollTop,
      );
      setScrollMargin((m) => (m === top ? m : top));
    };
    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(scroller);
    const page = el.closest('.rs-table')?.parentElement;
    if (page) ro.observe(page);
    return () => ro.disconnect();
  }, [scrollRef]);

  const estimateSize = useCallback(
    (i: number) => ESTIMATE[items[i].kind],
    [items],
  );
  const getItemKey = useCallback((i: number) => items[i].key, [items]);
  const virtualizer = useVirtualizer({
    count: items.length,
    getScrollElement: () => scrollRef.current,
    estimateSize,
    getItemKey,
    overscan: 10,
    scrollMargin,
    initialRect: FIRST_SCREEN,
  });

  // Keep the cursor in view as j/k moves it.
  useEffect(() => {
    if (!cursor) return;
    const i = items.findIndex((it) => it.key === cursor);
    if (i >= 0) virtualizer.scrollToIndex(i, { align: 'auto' });
  }, [cursor]); // eslint-disable-line react-hooks/exhaustive-deps

  const openRow = useCallback(
    (w: WorkloadReport) => {
      setCursor(workloadId(w));
      onOpen(w);
    },
    [onOpen],
  );

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (keyboardDisabled) return;
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

  const draw = (item: Item) => {
    switch (item.kind) {
      case 'group': {
        const g = item.g;
        return (
          <button
            className="rs-group-header"
            onClick={() => toggleSet(collapsed, g.key, setCollapsed)}
            aria-expanded={!collapsed.has(g.key)}
          >
            {collapsed.has(g.key) ? <ChevronRightIcon /> : <ChevronDownIcon />}
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
        );
      }
      case 'row': {
        const id = item.key;
        return (
          <Row
            w={item.w}
            cursor={cursor === id}
            selected={selected.has(id)}
            showNamespace={groupBy !== 'namespace'}
            canOpenWorkload={!!canOpenWorkload?.(item.w)}
            onOpen={openRow}
            onToggle={onToggle}
            onOpenWorkload={onOpenWorkload}
          />
        );
      }
      case 'more': {
        const g = item.g;
        return (
          <button
            className="rs-more"
            onClick={() => toggleSet(expanded, g.key, setExpanded)}
          >
            {expanded.has(g.key)
              ? 'Show fewer'
              : `Show ${g.workloads.length - limit} more`}
          </button>
        );
      }
    }
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
      <div
        ref={list}
        className="rs-rows"
        role="presentation"
        style={{ height: virtualizer.getTotalSize() }}
      >
        {virtualizer.getVirtualItems().map((v) => (
          <div
            key={v.key}
            ref={virtualizer.measureElement}
            data-index={v.index}
            className="rs-rows-item"
            role="presentation"
            style={{ transform: `translateY(${v.start - scrollMargin}px)` }}
          >
            {draw(items[v.index])}
          </div>
        ))}
      </div>
    </div>
  );
});
