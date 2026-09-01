import React, { useState, useEffect, useRef, useCallback, useMemo, Fragment } from 'react';
import * as Select from '@radix-ui/react-select';
import * as DropdownMenu from '@radix-ui/react-dropdown-menu';
import {
  MagnifyingGlassIcon,
  Cross2Icon,
  ChevronUpIcon,
  ChevronDownIcon,
  CheckIcon,
  PauseIcon,
  PlayIcon,
  CopyIcon,
  DownloadIcon,
  PinBottomIcon,
  ClockIcon,
  TextAlignLeftIcon,
} from '@radix-ui/react-icons';
import { useVirtualizer } from '@tanstack/react-virtual';
import { useLogFeed, ContainerInfo } from './useLogFeed';
import { LogLine, LogLevel, Matcher, buildMatcher } from './logEngine';
import useDebounce from '../../hooks/useDebounce';
import './LogViewer.css';

interface LogViewerProps {
  cluster: string;
  namespace: string;
  name: string;
  kind: string;
  containers?: ContainerInfo[];
}

const TAIL_OPTIONS = [100, 500, 1000, 2000, 5000, 10000];
const POD_COLORS = ['#3b82f6', '#10b981', '#f59e0b', '#ef4444', '#8b5cf6', '#ec4899', '#14b8a6', '#f97316'];
const LEVEL_CHIPS: { key: LogLevel; label: string }[] = [
  { key: 'error', label: 'ERR' },
  { key: 'warn', label: 'WRN' },
  { key: 'info', label: 'INF' },
  { key: 'debug', label: 'DBG' },
];

const PREFS_KEY = 'kanivet.logviewer.prefs';
const loadPrefs = (): { timestamps: boolean; wrap: boolean } => {
  try {
    return { timestamps: false, wrap: false, ...JSON.parse(localStorage.getItem(PREFS_KEY) || '{}') };
  } catch {
    return { timestamps: false, wrap: false };
  }
};

const shortPod = (pod: string) => {
  const parts = pod.split('-');
  return parts.length > 2 ? parts.slice(-2).join('-') : pod;
};

function renderContent(line: LogLine, matcher: Matcher | null) {
  if (!line.text) return ' ';
  const ranges = matcher ? matcher.ranges(line.text, line.lower) : null;
  if (!ranges || !ranges.length) {
    if (!line.segs) return line.text;
    return line.segs.map((s, i) => (s.cls ? <span key={i} className={s.cls}>{s.text}</span> : <Fragment key={i}>{s.text}</Fragment>));
  }
  const segs = line.segs || [{ text: line.text, cls: '' }];
  const pieces: { text: string; cls: string; mark: boolean }[] = [];
  let off = 0;
  let ri = 0;
  for (const seg of segs) {
    const len = seg.text.length;
    let s = 0;
    while (s < len) {
      while (ri < ranges.length && ranges[ri][1] <= off + s) ri++;
      const r = ranges[ri];
      if (!r || r[0] >= off + len) {
        pieces.push({ text: seg.text.slice(s), cls: seg.cls, mark: false });
        break;
      }
      const mStart = Math.max(r[0] - off, s);
      const mEnd = Math.min(r[1] - off, len);
      if (mStart > s) pieces.push({ text: seg.text.slice(s, mStart), cls: seg.cls, mark: false });
      pieces.push({ text: seg.text.slice(mStart, mEnd), cls: seg.cls, mark: true });
      s = mEnd;
    }
    off += len;
  }
  return pieces.map((p, i) =>
    p.mark ? (
      <mark key={i} className={p.cls || undefined}>{p.text}</mark>
    ) : p.cls ? (
      <span key={i} className={p.cls}>{p.text}</span>
    ) : (
      <Fragment key={i}>{p.text}</Fragment>
    ),
  );
}

const LogViewer = ({ cluster, namespace, name, kind, containers: ownContainers }: LogViewerProps) => {
  const isWorkload = kind !== 'pod';
  const [container, setContainer] = useState(() => {
    if (kind !== 'pod') return '';
    const list = ownContainers || [];
    return (list.find((c) => !c.init) || list[0])?.name || '';
  });
  const [tailLines, setTailLines] = useState(1000);
  const [previous, setPrevious] = useState(false);

  const { buffer, version, pods, containers: streamContainers, status, error } = useLogFeed({
    cluster,
    namespace,
    name,
    kind,
    container,
    tailLines,
    previous,
  });

  const containers = ownContainers?.length ? ownContainers : streamContainers;
  const effectiveContainer = container || containers.find((c) => !c.init)?.name || containers[0]?.name || '';

  const [prefs] = useState(loadPrefs);
  const [timestamps, setTimestamps] = useState(prefs.timestamps);
  const [wrap, setWrap] = useState(prefs.wrap);
  useEffect(() => {
    try {
      localStorage.setItem(PREFS_KEY, JSON.stringify({ timestamps, wrap }));
    } catch {}
  }, [timestamps, wrap]);

  const [levelFilter, setLevelFilter] = useState<Set<LogLevel> | null>(null);
  const [podFilter, setPodFilter] = useState<Set<string> | null>(null);

  const [query, setQuery] = useState('');
  const debouncedQuery = useDebounce(query, 150);
  const [caseSensitive, setCaseSensitive] = useState(false);
  const [useRegex, setUseRegex] = useState(false);
  const [filterMode, setFilterMode] = useState(false);
  const [activeId, setActiveId] = useState<number | null>(null);

  const [follow, setFollow] = useState(true);
  const [pausedAt, setPausedAt] = useState<number | null>(null);
  const [copied, setCopied] = useState(false);
  const detachCountRef = useRef(0);
  const lastTopRef = useRef(0);

  const rootRef = useRef<HTMLDivElement | null>(null);
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const rowsRef = useRef<HTMLDivElement | null>(null);
  const searchRef = useRef<HTMLInputElement | null>(null);

  const rawMatcher = useMemo(
    () => buildMatcher(debouncedQuery, caseSensitive, useRegex),
    [debouncedQuery, caseSensitive, useRegex],
  );
  const matcher = typeof rawMatcher === 'object' ? rawMatcher : null;
  const invalidQuery = rawMatcher === 'invalid';

  useEffect(() => {
    setActiveId(null);
  }, [debouncedQuery, caseSensitive, useRegex, filterMode]);

  const view = useMemo(() => {
    const all = buffer.lines;
    let end = all.length;
    if (pausedAt != null) {
      let lo = 0;
      let hi = all.length;
      while (lo < hi) {
        const mid = (lo + hi) >> 1;
        if (all[mid].id <= pausedAt) lo = mid + 1;
        else hi = mid;
      }
      end = lo;
    }
    const filtered = !!levelFilter || !!podFilter || (filterMode && !!matcher);
    if (!filtered && !matcher) return { rows: null as number[] | null, count: end, matches: [] as number[], end };
    const rows: number[] | null = filtered ? [] : null;
    const matches: number[] = [];
    for (let i = 0; i < end; i++) {
      const line = all[i];
      if (levelFilter && !levelFilter.has(line.level)) continue;
      if (podFilter && line.pod && !podFilter.has(line.pod)) continue;
      const m = matcher ? matcher.test(line) : false;
      if (filterMode && matcher && !m) continue;
      if (rows) {
        rows.push(i);
        if (m) matches.push(rows.length - 1);
      } else if (m) {
        matches.push(i);
      }
    }
    return { rows, count: rows ? rows.length : end, matches, end };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [version, buffer, pausedAt, levelFilter, podFilter, filterMode, matcher]);

  const lineAt = useCallback(
    (vi: number): LogLine | undefined => buffer.lines[view.rows ? view.rows[vi] : vi],
    [buffer, view],
  );

  const matchSet = useMemo(() => new Set(view.matches), [view]);

  const activePos = useMemo(() => {
    if (activeId == null) return -1;
    return view.matches.findIndex((p) => lineAt(p)?.id === activeId);
  }, [activeId, view, lineAt]);

  const virtualizer = useVirtualizer({
    count: view.count,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => 20,
    overscan: 20,
    getItemKey: (i) => lineAt(i)?.id ?? `i${i}`,
  });

  useEffect(() => {
    virtualizer.measure();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [wrap]);

  useEffect(() => {
    if (!follow || pausedAt != null || !view.count) return;
    const el = scrollRef.current;
    if (!el) return;
    virtualizer.scrollToIndex(view.count - 1, { align: 'end' });
    const raf = requestAnimationFrame(() => {
      el.scrollTop = el.scrollHeight;
      lastTopRef.current = el.scrollTop;
    });
    return () => cancelAnimationFrame(raf);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [version, follow, pausedAt, view.count, wrap]);

  const detach = useCallback(() => {
    setFollow(false);
    detachCountRef.current = buffer.lines.length;
  }, [buffer]);

  const onScroll = useCallback(() => {
    const el = scrollRef.current;
    if (!el) return;
    const dist = el.scrollHeight - el.scrollTop - el.clientHeight;
    const up = el.scrollTop < lastTopRef.current - 1;
    lastTopRef.current = el.scrollTop;
    if (up && dist > 40 && follow) detach();
    else if (dist < 4 && !follow && pausedAt == null) setFollow(true);
  }, [follow, pausedAt, detach]);

  const gotoMatch = useCallback(
    (dir: 1 | -1) => {
      const m = view.matches;
      if (!m.length) return;
      const next = activePos < 0 ? m.length - 1 : (activePos + dir + m.length) % m.length;
      const pos = m[next];
      const line = lineAt(pos);
      if (!line) return;
      setActiveId(line.id);
      detach();
      virtualizer.scrollToIndex(pos, { align: 'center' });
    },
    [view, activePos, lineAt, detach, virtualizer],
  );

  const jumpToLatest = useCallback(() => {
    setPausedAt(null);
    setFollow(true);
  }, []);

  const togglePause = useCallback(() => {
    if (pausedAt == null) {
      const last = buffer.lines[buffer.lines.length - 1];
      setPausedAt(last ? last.id : 0);
      setFollow(false);
    } else {
      jumpToLatest();
    }
  }, [pausedAt, buffer, jumpToLatest]);

  const toggleLevel = useCallback((lv: LogLevel) => {
    setLevelFilter((prev) => {
      if (!prev) return new Set([lv]);
      const next = new Set(prev);
      if (next.has(lv)) next.delete(lv);
      else next.add(lv);
      return next.size ? next : null;
    });
  }, []);

  const activePods = podFilter ?? new Set(pods.map((p) => p.name));
  const togglePod = useCallback(
    (podName: string) => {
      setPodFilter(() => {
        const next = new Set(activePods);
        if (next.has(podName)) next.delete(podName);
        else next.add(podName);
        return next.size === pods.length ? null : next;
      });
    },
    [activePods, pods],
  );

  const podColor = useCallback(
    (podName: string) => POD_COLORS[Math.max(0, pods.findIndex((p) => p.name === podName)) % POD_COLORS.length],
    [pods],
  );

  const viewText = useCallback(() => {
    const parts: string[] = [];
    for (let i = 0; i < view.count; i++) {
      const l = lineAt(i);
      if (!l) continue;
      let s = l.text;
      if (isWorkload && l.pod) s = `[${l.pod}] ${s}`;
      if (timestamps && l.tsStr) s = `${l.tsStr} ${s}`;
      parts.push(s);
    }
    return parts.join('\n');
  }, [view, lineAt, isWorkload, timestamps]);

  const copyView = useCallback(() => {
    navigator.clipboard.writeText(viewText()).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    });
  }, [viewText]);

  const downloadView = useCallback(() => {
    const blob = new Blob([viewText()], { type: 'text/plain' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `${name}${effectiveContainer ? `-${effectiveContainer}` : ''}.log`;
    a.click();
    URL.revokeObjectURL(url);
  }, [viewText, name, effectiveContainer]);

  const onKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === 'f') {
        e.preventDefault();
        searchRef.current?.focus();
        searchRef.current?.select();
      } else if ((e.metaKey || e.ctrlKey) && e.key === 'a' && !(e.target instanceof HTMLInputElement)) {
        e.preventDefault();
        const el = rowsRef.current;
        if (!el) return;
        const sel = window.getSelection();
        const range = document.createRange();
        range.selectNodeContents(el);
        sel?.removeAllRanges();
        sel?.addRange(range);
      }
    },
    [],
  );

  const newCount = follow || pausedAt != null ? 0 : buffer.lines.length - detachCountRef.current;
  const pausedNew = pausedAt != null ? buffer.lines.length - view.end : 0;
  const totalShown = view.rows ? `${view.count.toLocaleString()} of ${view.end.toLocaleString()}` : view.count.toLocaleString();
  const items = virtualizer.getVirtualItems();
  const showLoading = status === 'connecting' && buffer.lines.length === 0;

  return (
    <div
      ref={rootRef}
      className="lv-root"
      tabIndex={-1}
      onKeyDown={onKeyDown}
      onMouseDownCapture={(e) => {
        if (!(e.target as HTMLElement).closest('input,button,[role]')) {
          rootRef.current?.focus({ preventScroll: true });
        }
      }}
    >
      <div className="lv-toolbar">
        <div className="lv-group">
          {containers.length > 1 && (
            <Select.Root value={effectiveContainer} onValueChange={setContainer}>
              <Select.Trigger className="lv-select" title="Container">
                <Select.Value />
                <Select.Icon className="lv-select-icon">
                  <ChevronDownIcon />
                </Select.Icon>
              </Select.Trigger>
              <Select.Portal>
                <Select.Content className="lv-select-content" position="popper" sideOffset={4}>
                  <Select.Viewport>
                    {containers.map((c) => (
                      <Select.Item key={c.name} value={c.name} className="lv-select-item">
                        <Select.ItemText>
                          {c.init && <span className="lv-init-badge">INIT</span>}
                          {c.name}
                        </Select.ItemText>
                      </Select.Item>
                    ))}
                  </Select.Viewport>
                </Select.Content>
              </Select.Portal>
            </Select.Root>
          )}

          {isWorkload && pods.length > 1 && (
            <DropdownMenu.Root>
              <DropdownMenu.Trigger className="lv-select" title="Filter pods">
                Pods {activePods.size}/{pods.length}
                <ChevronDownIcon className="lv-select-icon" />
              </DropdownMenu.Trigger>
              <DropdownMenu.Portal>
                <DropdownMenu.Content className="lv-select-content lv-pod-menu" sideOffset={4} align="start">
                  <div className="lv-pod-menu-actions">
                    <button onClick={() => setPodFilter(null)}>All</button>
                    <button onClick={() => setPodFilter(new Set())}>None</button>
                  </div>
                  {pods.map((p) => (
                    <DropdownMenu.CheckboxItem
                      key={p.name}
                      className="lv-select-item lv-pod-item"
                      checked={activePods.has(p.name)}
                      onCheckedChange={() => togglePod(p.name)}
                      onSelect={(e) => e.preventDefault()}
                    >
                      <span className="lv-pod-check">
                        <DropdownMenu.ItemIndicator>
                          <CheckIcon />
                        </DropdownMenu.ItemIndicator>
                      </span>
                      <span className="lv-pod-dot" style={{ background: podColor(p.name) }} />
                      <span className="lv-pod-name" title={p.name}>
                        {shortPod(p.name)}
                      </span>
                      {p.restartCount > 0 && <span className="lv-pod-restarts">↻{p.restartCount}</span>}
                      {!p.ready && p.status !== 'Succeeded' && <span className="lv-pod-notready">{p.status}</span>}
                    </DropdownMenu.CheckboxItem>
                  ))}
                </DropdownMenu.Content>
              </DropdownMenu.Portal>
            </DropdownMenu.Root>
          )}

          <Select.Root value={String(tailLines)} onValueChange={(v) => setTailLines(parseInt(v, 10))}>
            <Select.Trigger className="lv-select" title="Tail lines">
              <Select.Value />
              <Select.Icon className="lv-select-icon">
                <ChevronDownIcon />
              </Select.Icon>
            </Select.Trigger>
            <Select.Portal>
              <Select.Content className="lv-select-content" position="popper" sideOffset={4}>
                <Select.Viewport>
                  {TAIL_OPTIONS.map((n) => (
                    <Select.Item key={n} value={String(n)} className="lv-select-item">
                      <Select.ItemText>Last {n.toLocaleString()}</Select.ItemText>
                    </Select.Item>
                  ))}
                </Select.Viewport>
              </Select.Content>
            </Select.Portal>
          </Select.Root>

          <button
            className={`lv-chip ${previous ? 'lv-on' : ''}`}
            onClick={() => setPrevious((p) => !p)}
            title="Logs from the previous container instance (before last restart)"
          >
            Previous
          </button>
        </div>

        <div className={`lv-search ${invalidQuery ? 'lv-invalid' : ''}`}>
          <MagnifyingGlassIcon className="lv-search-icon" />
          <input
            ref={searchRef}
            type="text"
            placeholder="Search logs…  (⌘F)"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') {
                e.preventDefault();
                gotoMatch(e.shiftKey ? -1 : 1);
              } else if (e.key === 'Escape') {
                setQuery('');
              }
            }}
          />
          <button
            className={`lv-mini ${caseSensitive ? 'lv-on' : ''}`}
            onClick={() => setCaseSensitive((v) => !v)}
            title="Match case"
          >
            Aa
          </button>
          <button
            className={`lv-mini ${useRegex ? 'lv-on' : ''}`}
            onClick={() => setUseRegex((v) => !v)}
            title="Regular expression"
          >
            .*
          </button>
          <button
            className={`lv-mini ${filterMode ? 'lv-on' : ''}`}
            onClick={() => setFilterMode((v) => !v)}
            title="Show only matching lines"
          >
            ⊜
          </button>
          {query && (
            <>
              <span className="lv-search-count">
                {view.matches.length
                  ? activePos >= 0
                    ? `${activePos + 1}/${view.matches.length}`
                    : view.matches.length.toLocaleString()
                  : '0'}
              </span>
              <button className="lv-mini" onClick={() => gotoMatch(-1)} disabled={!view.matches.length} title="Previous match (⇧↵)">
                <ChevronUpIcon />
              </button>
              <button className="lv-mini" onClick={() => gotoMatch(1)} disabled={!view.matches.length} title="Next match (↵)">
                <ChevronDownIcon />
              </button>
              <button className="lv-mini" onClick={() => setQuery('')} title="Clear (Esc)">
                <Cross2Icon />
              </button>
            </>
          )}
        </div>

        <div className="lv-group">
          {LEVEL_CHIPS.map(({ key, label }) => (
            <button
              key={key}
              className={`lv-chip lv-chip-${key} ${levelFilter?.has(key) ? 'lv-on' : ''}`}
              onClick={() => toggleLevel(key)}
              title={`Show only ${label} lines (toggle)`}
            >
              {label}
              {(key === 'error' || key === 'warn') && buffer.counts[key] > 0 && (
                <span className="lv-chip-count">{buffer.counts[key].toLocaleString()}</span>
              )}
            </button>
          ))}
          <span className="lv-sep" />
          <button className={`lv-icon ${timestamps ? 'lv-on' : ''}`} onClick={() => setTimestamps((v) => !v)} title="Timestamps">
            <ClockIcon />
          </button>
          <button className={`lv-icon ${wrap ? 'lv-on' : ''}`} onClick={() => setWrap((v) => !v)} title="Wrap lines">
            <TextAlignLeftIcon />
          </button>
          <button className={`lv-icon ${pausedAt != null ? 'lv-on' : ''}`} onClick={togglePause} title={pausedAt != null ? 'Resume' : 'Pause (keeps buffering)'}>
            {pausedAt != null ? <PlayIcon /> : <PauseIcon />}
          </button>
          <button className="lv-icon" onClick={copyView} title={copied ? 'Copied!' : 'Copy visible logs'}>
            {copied ? <CheckIcon /> : <CopyIcon />}
          </button>
          <button className="lv-icon" onClick={downloadView} title="Download logs">
            <DownloadIcon />
          </button>
          <button className={`lv-icon ${follow && pausedAt == null ? 'lv-on' : ''}`} onClick={jumpToLatest} title="Follow latest">
            <PinBottomIcon />
          </button>
        </div>
      </div>

      {error && (
        <div className="lv-error">
          <span>⚠</span>
          <span>{error}</span>
        </div>
      )}

      <div className="lv-body-wrap">
        <div className={`lv-body ${wrap ? 'lv-wrapped' : ''}`} ref={scrollRef} onScroll={onScroll}>
          {showLoading ? (
          <div className="lv-loading">
            <div className="lv-spinner" />
            <span>Loading logs…</span>
          </div>
          ) : view.count === 0 ? (
            <div className="lv-empty">
              {buffer.lines.length === 0 ? 'No logs available' : 'No lines match the current filters'}
            </div>
          ) : (
            <div className="lv-sizer" style={{ height: virtualizer.getTotalSize() }}>
              <div ref={rowsRef}>
                {items.map((item) => {
                  const line = lineAt(item.index);
                  if (!line) return null;
                  const isMatch = matchSet.has(item.index);
                  const isActive = isMatch && activePos >= 0 && view.matches[activePos] === item.index;
                  return (
                    <div
                      key={item.key}
                      data-index={item.index}
                      ref={virtualizer.measureElement}
                      className={`lv-line lv-${line.level}${isMatch ? ' lv-hit' : ''}${isActive ? ' lv-hit-active' : ''}`}
                      style={{ transform: `translateY(${item.start}px)` }}
                    >
                      {timestamps && (
                        <span className="lv-ts" title={line.ts ? new Date(line.ts).toISOString() : ''}>
                          {line.tsStr || '—'}
                        </span>
                      )}
                      {isWorkload && line.pod && (
                        <span className="lv-pod-tag" style={{ color: podColor(line.pod) }} title={line.pod}>
                          {shortPod(line.pod)}
                        </span>
                      )}
                      <span className="lv-text">{renderContent(line, isMatch ? matcher : null)}</span>
                    </div>
                  );
                })}
              </div>
            </div>
          )}
        </div>

        {newCount > 0 && (
          <button className="lv-jump" onClick={jumpToLatest}>
            <ChevronDownIcon /> {newCount.toLocaleString()} new line{newCount === 1 ? '' : 's'}
          </button>
        )}
      </div>

      <div className="lv-status">
        <span className={`lv-dot lv-dot-${status}`} />
        <span className="lv-status-text">
          {status === 'connecting' && 'Connecting…'}
          {status === 'live' && 'Live'}
          {status === 'ended' && (previous ? 'Previous instance' : 'Stream ended')}
          {status === 'error' && 'Error'}
        </span>
        <span className="lv-status-lines">{totalShown} lines</span>
        {pausedAt != null && (
          <span className="lv-status-paused">
            Paused{pausedNew > 0 ? ` · ${pausedNew.toLocaleString()} new` : ''}
          </span>
        )}
        {query && !invalidQuery && (
          <span className="lv-status-matches">{view.matches.length.toLocaleString()} matches</span>
        )}
        {invalidQuery && <span className="lv-status-invalid">Invalid regex</span>}
      </div>
    </div>
  );
};

export default LogViewer;
