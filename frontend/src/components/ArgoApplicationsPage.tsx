import { useEffect, useState, useCallback, useMemo, useRef, memo } from 'react';
import { useStore } from '../store';
import api from '../services/api';
import { ArgoAppListEntry, ArgoStats, ArgoSyncOptions } from '../services/api/resources';
import { formatAge } from '../utils/formatters';
import { useVisibleInterval } from '../hooks/useVisibleInterval';
import './ArgoApplicationsPage.css';

interface Props {
  cluster: string;
}

type SortKey = 'name' | 'namespace' | 'project' | 'syncStatus' | 'health' | 'lastSyncedAt' | 'createdAt' | 'destNamespace';
type GroupBy = 'none' | 'health' | 'sync' | 'project' | 'destNamespace' | 'name';

interface PersistedState {
  search: string;
  filterSync: string[];
  filterHealth: string[];
  filterProject: string[];
  sortKey: SortKey;
  sortDir: 'asc' | 'desc';
  groupBy: GroupBy;
  density: 'compact' | 'cozy';
  collapsedGroups: string[];
}

const defaultState: PersistedState = {
  search: '',
  filterSync: [],
  filterHealth: [],
  filterProject: [],
  sortKey: 'name',
  sortDir: 'asc',
  groupBy: 'health',
  density: 'compact',
  collapsedGroups: [],
};

const HEALTH_RANK: Record<string, number> = {
  Degraded: 0,
  Missing: 1,
  Progressing: 2,
  Suspended: 3,
  Healthy: 4,
  Unknown: 5,
};
const SYNC_RANK: Record<string, number> = {
  OutOfSync: 0,
  Synced: 1,
  Unknown: 2,
};

const loadPersisted = (cluster: string): PersistedState => {
  try {
    const raw = localStorage.getItem(`kanivet.argoAppsPage.${cluster}`);
    if (!raw) return { ...defaultState };
    const parsed = JSON.parse(raw);
    return { ...defaultState, ...parsed };
  } catch {
    return { ...defaultState };
  }
};

const ArgoApplicationsPage = ({ cluster }: Props) => {
  const [items, setItems] = useState<ArgoAppListEntry[]>([]);
  const [stats, setStats] = useState<ArgoStats | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [busy, setBusy] = useState(false);
  const [rowBusy, setRowBusy] = useState<Map<string, 'sync' | 'refresh'>>(new Map());
  const [rowFlash, setRowFlash] = useState<Map<string, 'sync' | 'refresh'>>(new Map());
  const [focusedKey, setFocusedKey] = useState<string | null>(null);
  const [contextMenu, setContextMenu] = useState<{ x: number; y: number; entry: ArgoAppListEntry } | null>(null);
  const searchRef = useRef<HTMLInputElement | null>(null);
  const tableScrollRef = useRef<HTMLDivElement | null>(null);

  const [persisted, setPersisted] = useState<PersistedState>(() => loadPersisted(cluster));
  useEffect(() => {
    setPersisted(loadPersisted(cluster));
    setSelected(new Set());
    setFocusedKey(null);
    setStale(false);
    setItems([]);
    setStats(null);
  }, [cluster]);
  useEffect(() => {
    try {
      localStorage.setItem(`kanivet.argoAppsPage.${cluster}`, JSON.stringify(persisted));
    } catch {}
  }, [cluster, persisted]);

  const updateState = (patch: Partial<PersistedState>) => setPersisted((s) => ({ ...s, ...patch }));

  const [stale, setStale] = useState(false);
  const load = useCallback(async (silent = false) => {
    if (!silent) setLoading(true);
    try {
      const [list, s] = await Promise.all([
        api.getArgoApplicationsSummary(cluster),
        api.getArgoStats(cluster),
      ]);
      const hadDataBefore = items.length > 0;
      const gotEmpty = (!list || list.length === 0) && (!s || s.total === 0);
      if (gotEmpty && hadDataBefore) {
        setStale(true);
      } else {
        setItems((prev) => mergeStable(prev, list));
        setStats(s);
        setStale(false);
        setError(null);
      }
    } catch (e: any) {
      if (items.length === 0) setError(e?.message || 'Failed to load Argo applications');
      else setStale(true);
    } finally {
      if (!silent) setLoading(false);
    }
  }, [cluster, items.length]);

  useEffect(() => { load(); }, [load]);
  useVisibleInterval(() => load(true), rowBusy.size > 0 ? 2000 : 10000, { targetRef: tableScrollRef });

  const projects = useMemo(() => {
    const set = new Set<string>();
    items.forEach((it) => it.project && set.add(it.project));
    return Array.from(set).sort();
  }, [items]);

  const showProjectColumn = projects.length > 1;
  const destNamespaces = useMemo(() => {
    const set = new Set<string>();
    items.forEach((it) => it.destNamespace && set.add(it.destNamespace));
    return set;
  }, [items]);
  const showSingleDestNs = destNamespaces.size === 1;

  const filterSync = useMemo(() => new Set(persisted.filterSync), [persisted.filterSync]);
  const filterHealth = useMemo(() => new Set(persisted.filterHealth), [persisted.filterHealth]);
  const filterProject = useMemo(() => new Set(persisted.filterProject), [persisted.filterProject]);
  const collapsedGroups = useMemo(() => new Set(persisted.collapsedGroups), [persisted.collapsedGroups]);

  const filtered = useMemo(() => {
    const q = persisted.search.trim().toLowerCase();
    return items.filter((it) => {
      if (q) {
        const hay = `${it.name} ${it.namespace} ${it.project || ''} ${it.destNamespace || ''} ${it.repoUrl || ''}`.toLowerCase();
        if (!hay.includes(q)) return false;
      }
      if (filterSync.size > 0 && !filterSync.has(it.syncStatus || 'Unknown')) return false;
      if (filterHealth.size > 0 && !filterHealth.has(it.health || 'Unknown')) return false;
      if (filterProject.size > 0 && !filterProject.has(it.project || '')) return false;
      return true;
    });
  }, [items, persisted.search, filterSync, filterHealth, filterProject]);

  const sorted = useMemo(() => {
    const arr = filtered.slice();
    const dir = persisted.sortDir === 'asc' ? 1 : -1;
    arr.sort((a, b) => {
      const av = (a[persisted.sortKey] || '') as string;
      const bv = (b[persisted.sortKey] || '') as string;
      if (av === bv) return 0;
      return av < bv ? -1 * dir : 1 * dir;
    });
    return arr;
  }, [filtered, persisted.sortKey, persisted.sortDir]);

  const groups = useMemo(() => {
    if (persisted.groupBy === 'none') {
      return [{ key: '', label: '', entries: sorted, rank: 0 }];
    }
    const map = new Map<string, ArgoAppListEntry[]>();
    for (const it of sorted) {
      let key: string;
      switch (persisted.groupBy) {
        case 'health': key = it.health || 'Unknown'; break;
        case 'sync': key = it.syncStatus || 'Unknown'; break;
        case 'project': key = it.project || '—'; break;
        case 'destNamespace': key = it.destNamespace || '—'; break;
        case 'name': key = it.name; break;
        default: key = '';
      }
      if (!map.has(key)) map.set(key, []);
      map.get(key)!.push(it);
    }
    const arr = Array.from(map.entries()).map(([key, entries]) => {
      let rank = 0;
      if (persisted.groupBy === 'health') rank = HEALTH_RANK[key] ?? 99;
      else if (persisted.groupBy === 'sync') rank = SYNC_RANK[key] ?? 99;
      return { key, label: key, entries, rank };
    });
    arr.sort((a, b) => (a.rank !== b.rank ? a.rank - b.rank : a.label.localeCompare(b.label)));
    return arr;
  }, [sorted, persisted.groupBy]);

  const flatRows: { entry: ArgoAppListEntry; groupKey: string }[] = useMemo(() => {
    const out: { entry: ArgoAppListEntry; groupKey: string }[] = [];
    for (const g of groups) {
      if (collapsedGroups.has(g.key)) continue;
      for (const e of g.entries) out.push({ entry: e, groupKey: g.key });
    }
    return out;
  }, [groups, collapsedGroups]);

  const refKey = (e: ArgoAppListEntry) => `${e.namespace}/${e.name}`;

  const toggleSelect = (e: ArgoAppListEntry) => {
    setSelected((prev) => {
      const next = new Set(prev);
      const k = refKey(e);
      if (next.has(k)) next.delete(k);
      else next.add(k);
      return next;
    });
  };

  const allVisibleSelected = flatRows.length > 0 && flatRows.every((r) => selected.has(refKey(r.entry)));
  const toggleAll = () => {
    if (allVisibleSelected) {
      setSelected(new Set());
    } else {
      const next = new Set(selected);
      flatRows.forEach((r) => next.add(refKey(r.entry)));
      setSelected(next);
    }
  };

  const toggleSort = (key: SortKey) => {
    if (persisted.sortKey === key) {
      updateState({ sortDir: persisted.sortDir === 'asc' ? 'desc' : 'asc' });
    } else {
      updateState({ sortKey: key, sortDir: 'asc' });
    }
  };

  const togglePivot = (kind: 'sync' | 'health', value: string) => {
    if (kind === 'sync') {
      const next = new Set(filterSync);
      if (next.has(value)) next.delete(value);
      else next.add(value);
      updateState({ filterSync: Array.from(next) });
    } else {
      const next = new Set(filterHealth);
      if (next.has(value)) next.delete(value);
      else next.add(value);
      updateState({ filterHealth: Array.from(next) });
    }
  };

  const toggleGroupCollapsed = (key: string) => {
    const next = new Set(collapsedGroups);
    if (next.has(key)) next.delete(key);
    else next.add(key);
    updateState({ collapsedGroups: Array.from(next) });
  };

  const openApp = useCallback((e: ArgoAppListEntry) => {
    const apiVersion = 'argoproj.io/v1alpha1';
    const resource = { name: 'applications', group: 'argoproj.io', version: 'v1alpha1', kind: 'Application', namespaced: true };
    const item = { name: e.name, namespace: e.namespace, uid: `${e.namespace}-${e.name}`, kind: 'Application', apiVersion };
    const { openDetailTab, loadDetails } = useStore.getState();
    openDetailTab(resource, item, cluster, false);
    loadDetails(cluster, resource, item).catch(() => {});
  }, [cluster]);

  const markRowBusy = (entries: ArgoAppListEntry[], state: 'sync' | 'refresh') => {
    setRowBusy((prev) => {
      const next = new Map(prev);
      entries.forEach((e) => next.set(`${e.namespace}/${e.name}`, state));
      return next;
    });
  };

  const clearRowBusy = (entries: ArgoAppListEntry[]) => {
    setRowBusy((prev) => {
      const next = new Map(prev);
      entries.forEach((e) => next.delete(`${e.namespace}/${e.name}`));
      return next;
    });
  };

  const flashRows = (entries: ArgoAppListEntry[], state: 'sync' | 'refresh') => {
    setRowFlash((prev) => {
      const next = new Map(prev);
      entries.forEach((e) => next.set(`${e.namespace}/${e.name}`, state));
      return next;
    });
    setTimeout(() => {
      setRowFlash((prev) => {
        const next = new Map(prev);
        entries.forEach((e) => next.delete(`${e.namespace}/${e.name}`));
        return next;
      });
    }, 1800);
  };

  const runSync = useCallback(async (entries: ArgoAppListEntry[], opts: ArgoSyncOptions = {}) => {
    if (entries.length === 0) return;
    setBusy(true);
    markRowBusy(entries, 'sync');
    window.dispatchEvent(new CustomEvent('toast:success', { detail: { message: `Sync triggered for ${entries.length} app${entries.length === 1 ? '' : 's'}` } }));
    const minSpin = new Promise((r) => setTimeout(r, 2000));
    try {
      await Promise.allSettled(entries.map((e) => api.syncArgoApplication(cluster, e.namespace, e.name, opts)));
      load(true);
      await minSpin;
      clearRowBusy(entries);
      flashRows(entries, 'sync');
    } finally {
      setBusy(false);
    }
  }, [cluster, load]);

  const runRefresh = useCallback(async (entries: ArgoAppListEntry[]) => {
    if (entries.length === 0) return;
    setBusy(true);
    markRowBusy(entries, 'refresh');
    window.dispatchEvent(new CustomEvent('toast:success', { detail: { message: `Refresh triggered for ${entries.length} app${entries.length === 1 ? '' : 's'}` } }));
    const minSpin = new Promise((r) => setTimeout(r, 2000));
    try {
      await Promise.allSettled(entries.map((e) => api.refreshArgoApplication(cluster, e.namespace, e.name, false)));
      load(true);
      await minSpin;
      clearRowBusy(entries);
      flashRows(entries, 'refresh');
    } finally {
      setBusy(false);
    }
  }, [cluster, load]);

  const selectedEntries = useMemo(() => flatRows.filter((r) => selected.has(refKey(r.entry))).map((r) => r.entry), [flatRows, selected]);

  const handleToggleSelect = useCallback((e: ArgoAppListEntry) => toggleSelect(e), []);
  const handleOpen = useCallback((e: ArgoAppListEntry) => openApp(e), [openApp]);
  const handleSyncOne = useCallback((e: ArgoAppListEntry) => runSync([e]), [runSync]);
  const handleRefreshOne = useCallback((e: ArgoAppListEntry) => runRefresh([e]), [runRefresh]);
  const handleContext = useCallback((entry: ArgoAppListEntry, x: number, y: number) => setContextMenu({ x, y, entry }), []);

  // Keyboard shortcuts
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const tag = (e.target as HTMLElement)?.tagName?.toLowerCase();
      const inField = tag === 'input' || tag === 'textarea';
      if (e.key === '/' && !inField) {
        e.preventDefault();
        searchRef.current?.focus();
        return;
      }
      if (inField) return;
      const idx = focusedKey ? flatRows.findIndex((r) => refKey(r.entry) === focusedKey) : -1;
      if (e.key === 'j' || e.key === 'ArrowDown') {
        e.preventDefault();
        const ni = idx < 0 ? 0 : Math.min(idx + 1, flatRows.length - 1);
        if (flatRows[ni]) setFocusedKey(refKey(flatRows[ni].entry));
      } else if (e.key === 'k' || e.key === 'ArrowUp') {
        e.preventDefault();
        const ni = idx < 0 ? flatRows.length - 1 : Math.max(idx - 1, 0);
        if (flatRows[ni]) setFocusedKey(refKey(flatRows[ni].entry));
      } else if (e.key === 'Enter' && focusedKey) {
        e.preventDefault();
        const r = flatRows.find((r) => refKey(r.entry) === focusedKey);
        if (r) openApp(r.entry);
      } else if (e.key === ' ' && focusedKey) {
        e.preventDefault();
        const r = flatRows.find((r) => refKey(r.entry) === focusedKey);
        if (r) toggleSelect(r.entry);
      } else if (e.key.toLowerCase() === 's') {
        const targets = selectedEntries.length > 0 ? selectedEntries : focusedKey ? flatRows.filter((r) => refKey(r.entry) === focusedKey).map((r) => r.entry) : [];
        if (targets.length > 0) {
          e.preventDefault();
          runSync(targets);
        }
      } else if (e.key.toLowerCase() === 'r') {
        const targets = selectedEntries.length > 0 ? selectedEntries : focusedKey ? flatRows.filter((r) => refKey(r.entry) === focusedKey).map((r) => r.entry) : [];
        if (targets.length > 0) {
          e.preventDefault();
          runRefresh(targets);
        }
      } else if (e.key === 'Escape') {
        if (contextMenu) setContextMenu(null);
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [flatRows, focusedKey, selectedEntries, openApp, runSync, runRefresh, contextMenu]);

  // Close context menu on outside click
  useEffect(() => {
    if (!contextMenu) return;
    const onDown = (e: MouseEvent) => {
      const target = e.target as HTMLElement;
      if (!target.closest('.argo-apps-ctx-menu')) setContextMenu(null);
    };
    window.addEventListener('mousedown', onDown);
    return () => window.removeEventListener('mousedown', onDown);
  }, [contextMenu]);

  const sortArrow = (key: SortKey) => (persisted.sortKey === key ? (persisted.sortDir === 'asc' ? '▲' : '▼') : '');

  const totalCount = stats?.total ?? items.length;
  const synced = stats?.bySync['Synced'] || 0;
  const outOfSync = stats?.bySync['OutOfSync'] || 0;
  const healthy = stats?.byHealth['Healthy'] || 0;
  const degraded = stats?.byHealth['Degraded'] || 0;
  const progressing = stats?.byHealth['Progressing'] || 0;
  const missing = stats?.byHealth['Missing'] || 0;
  const syncingNow = stats?.syncing || 0;

  const isPivotActive = (kind: 'sync' | 'health', value: string) => (kind === 'sync' ? filterSync.has(value) : filterHealth.has(value));

  return (
    <div className={`argo-apps-page density-${persisted.density}`}>
      <div className="argo-apps-pivots">
        <Pivot value={totalCount} label="Total" active={filterSync.size === 0 && filterHealth.size === 0} onClick={() => updateState({ filterSync: [], filterHealth: [] })} />
        <Pivot value={synced} label="Synced" tone={syncTone('Synced')} active={isPivotActive('sync', 'Synced')} onClick={() => togglePivot('sync', 'Synced')} />
        <Pivot value={outOfSync} label={statusLabel('OutOfSync')} tone={syncTone('OutOfSync')} active={isPivotActive('sync', 'OutOfSync')} onClick={() => togglePivot('sync', 'OutOfSync')} />
        <span className="argo-apps-pivot-sep" />
        <Pivot value={healthy} label="Healthy" tone={healthTone('Healthy')} active={isPivotActive('health', 'Healthy')} onClick={() => togglePivot('health', 'Healthy')} />
        <Pivot value={degraded} label="Degraded" tone={healthTone('Degraded')} active={isPivotActive('health', 'Degraded')} onClick={() => togglePivot('health', 'Degraded')} />
        <Pivot value={progressing} label="Progressing" tone={healthTone('Progressing')} active={isPivotActive('health', 'Progressing')} onClick={() => togglePivot('health', 'Progressing')} />
        <Pivot value={missing} label="Missing" tone={healthTone('Missing')} active={isPivotActive('health', 'Missing')} onClick={() => togglePivot('health', 'Missing')} />
        <span className="argo-apps-pivot-sep" />
        <Pivot value={syncingNow} label="Syncing" tone="info" active={false} />
      </div>

      <div className="argo-apps-toolbar">
        <label className="ap-search argo-apps-search">
          <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" aria-hidden="true">
            <circle cx="7" cy="7" r="4.5" />
            <path d="M10.5 10.5L14 14" />
          </svg>
          <input
            ref={searchRef}
            placeholder="Search by name, namespace, project or repo"
            value={persisted.search}
            onChange={(e) => updateState({ search: e.target.value })}
            aria-label="Search applications"
          />
          <kbd aria-hidden="true">/</kbd>
        </label>
        {showProjectColumn && (
          <FilterChips label="Project" values={projects} active={filterProject} onToggle={(v) => {
            const next = new Set(filterProject);
            if (next.has(v)) next.delete(v);
            else next.add(v);
            updateState({ filterProject: Array.from(next) });
          }} />
        )}
        <div className="argo-apps-control">
          <label>Group</label>
          <select className="ap-select" value={persisted.groupBy} onChange={(e) => updateState({ groupBy: e.target.value as GroupBy, collapsedGroups: [] })}>
            <option value="health">Health</option>
            <option value="sync">Sync</option>
            <option value="project">Project</option>
            <option value="destNamespace">Target namespace</option>
            <option value="name">Name</option>
            <option value="none">None</option>
          </select>
        </div>
        <div className="argo-apps-control argo-apps-density ap-segmented ap-segmented--sm" role="group" aria-label="Row density">
          <button type="button" aria-pressed={persisted.density === 'compact'} onClick={() => updateState({ density: 'compact' })} title="Compact rows">Compact</button>
          <button type="button" aria-pressed={persisted.density === 'cozy'} onClick={() => updateState({ density: 'cozy' })} title="Cozy rows">Cozy</button>
        </div>
        {selected.size > 0 && (
          <div className="argo-apps-bulk">
            <span className="argo-apps-bulk-count">{selected.size} selected</span>
            <button disabled={busy} onClick={() => runSync(selectedEntries)}>Sync</button>
            <button disabled={busy} onClick={() => runSync(selectedEntries, { prune: true })}>Sync and prune</button>
            <button disabled={busy} onClick={() => runRefresh(selectedEntries)}>Refresh</button>
            <button className="argo-apps-bulk-clear" onClick={() => setSelected(new Set())}>Clear</button>
          </div>
        )}
      </div>

      {error && <div className="argo-apps-error">{error}</div>}
      {stale && !error && (
        <div className="argo-apps-stale-banner">
          <span className="argo-apps-stale-dot" />
          <span>Live updates paused — showing last known state.</span>
          <button onClick={() => load()}>Retry now</button>
        </div>
      )}

      <div className="argo-apps-table-wrap" ref={tableScrollRef}>
        <table className="argo-apps-table">
          <thead>
            <tr>
              <th className="argo-apps-checkbox-col">
                <input type="checkbox" checked={allVisibleSelected} onChange={toggleAll} />
              </th>
              <th onClick={() => toggleSort('name')}>Name<span className="sort-arrow" aria-hidden="true">{sortArrow('name')}</span></th>
              <th className="is-static">Destination</th>
              {showProjectColumn && <th onClick={() => toggleSort('project')}>Project<span className="sort-arrow" aria-hidden="true">{sortArrow('project')}</span></th>}
              <th className="argo-apps-col-status" onClick={() => toggleSort('syncStatus')}>Sync<span className="sort-arrow" aria-hidden="true">{sortArrow('syncStatus')}</span></th>
              <th className="argo-apps-col-status" onClick={() => toggleSort('health')}>Health<span className="sort-arrow" aria-hidden="true">{sortArrow('health')}</span></th>
              <th className="argo-apps-col-rev is-static">Revision</th>
              <th className="argo-apps-col-time" onClick={() => toggleSort('lastSyncedAt')}>Last sync<span className="sort-arrow" aria-hidden="true">{sortArrow('lastSyncedAt')}</span></th>
              <th className="argo-apps-col-time" onClick={() => toggleSort('createdAt')}>Age<span className="sort-arrow" aria-hidden="true">{sortArrow('createdAt')}</span></th>
            </tr>
          </thead>
          <tbody>
            {loading && items.length === 0 ? (
              Array.from({ length: 8 }).map((_, i) => (
                <tr key={`sk-${i}`} className="argo-apps-skeleton-row">
                  <td colSpan={showProjectColumn ? 9 : 8}><span className="argo-apps-skeleton" /></td>
                </tr>
              ))
            ) : flatRows.length === 0 && groups.every((g) => g.entries.length === 0) ? (
              <tr><td colSpan={showProjectColumn ? 9 : 8} className="argo-apps-empty">No applications match the current filters.</td></tr>
            ) : (
              groups.flatMap((g) => {
                const rows: JSX.Element[] = [];
                if (persisted.groupBy !== 'none') {
                  const isCollapsed = collapsedGroups.has(g.key);
                  const tone = groupTone(persisted.groupBy, g.key);
                  rows.push(
                    <tr key={`group-${g.key}`} className="argo-apps-group-row" onClick={() => toggleGroupCollapsed(g.key)} aria-expanded={!isCollapsed}>
                      <td colSpan={showProjectColumn ? 9 : 8}>
                        <span className="argo-apps-group-inner">
                          <svg className={`argo-apps-group-chevron${isCollapsed ? '' : ' open'}`} viewBox="0 0 10 10" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                            <path d="M3.5 2l3 3-3 3" />
                          </svg>
                          {tone && <span className={`ap-dot ap-dot--${tone === 'neutral' ? 'muted' : tone}`} aria-hidden="true" />}
                          <span className="argo-apps-group-label">{persisted.groupBy === 'health' || persisted.groupBy === 'sync' ? statusLabel(g.label) : g.label || '–'}</span>
                          <span className="argo-apps-group-count">{g.entries.length}</span>
                        </span>
                      </td>
                    </tr>
                  );
                  if (isCollapsed) return rows;
                }
                for (const e of g.entries) {
                  const k = refKey(e);
                  rows.push(
                    <AppRow
                      key={k}
                      entry={e}
                      isSel={selected.has(k)}
                      isFocused={focusedKey === k}
                      busyState={rowBusy.get(k)}
                      flashState={rowFlash.get(k)}
                      showProjectColumn={showProjectColumn}
                      showSingleDestNs={showSingleDestNs}
                      onToggleSelect={handleToggleSelect}
                      onOpen={handleOpen}
                      onSync={handleSyncOne}
                      onRefresh={handleRefreshOne}
                      onContext={handleContext}
                    />
                  );
                }
                return rows;
              })
            )}
          </tbody>
        </table>
      </div>

      {contextMenu && (
        <div className="argo-apps-ctx-menu ap-menu" style={{ top: contextMenu.y, left: contextMenu.x }}>
          <button onClick={() => { openApp(contextMenu.entry); setContextMenu(null); }}>Open</button>
          <button onClick={() => { runSync([contextMenu.entry]); setContextMenu(null); }}>Sync</button>
          <button onClick={() => { runSync([contextMenu.entry], { prune: true }); setContextMenu(null); }}>Sync and prune</button>
          <button onClick={() => { runRefresh([contextMenu.entry]); setContextMenu(null); }}>Refresh</button>
        </div>
      )}
    </div>
  );
};

// Preserve referential equality for entries whose content didn't change between polls.
const mergeStable = (prev: ArgoAppListEntry[], next: ArgoAppListEntry[]): ArgoAppListEntry[] => {
  if (!prev || prev.length === 0) return next;
  const prevMap = new Map<string, ArgoAppListEntry>();
  for (const p of prev) prevMap.set(`${p.namespace}/${p.name}`, p);
  let changed = prev.length !== next.length;
  const merged = next.map((n) => {
    const key = `${n.namespace}/${n.name}`;
    const p = prevMap.get(key);
    if (p && shallowEqualEntry(p, n)) return p;
    changed = true;
    return n;
  });
  return changed ? merged : prev;
};

const shallowEqualEntry = (a: ArgoAppListEntry, b: ArgoAppListEntry): boolean => {
  return (
    a.name === b.name &&
    a.namespace === b.namespace &&
    a.project === b.project &&
    a.syncStatus === b.syncStatus &&
    a.health === b.health &&
    a.revision === b.revision &&
    a.targetRevision === b.targetRevision &&
    a.repoUrl === b.repoUrl &&
    a.path === b.path &&
    a.destNamespace === b.destNamespace &&
    a.destName === b.destName &&
    a.destServer === b.destServer &&
    a.reconciledAt === b.reconciledAt &&
    a.lastSyncedAt === b.lastSyncedAt &&
    a.lastSyncPhase === b.lastSyncPhase &&
    a.createdAt === b.createdAt &&
    a.operationPhase === b.operationPhase
  );
};

interface AppRowProps {
  entry: ArgoAppListEntry;
  isSel: boolean;
  isFocused: boolean;
  busyState: 'sync' | 'refresh' | undefined;
  flashState: 'sync' | 'refresh' | undefined;
  showProjectColumn: boolean;
  showSingleDestNs: boolean;
  onToggleSelect: (e: ArgoAppListEntry) => void;
  onOpen: (e: ArgoAppListEntry) => void;
  onSync: (e: ArgoAppListEntry) => void;
  onRefresh: (e: ArgoAppListEntry) => void;
  onContext: (e: ArgoAppListEntry, x: number, y: number) => void;
}

const AppRow = memo(({
  entry: e,
  isSel,
  isFocused,
  busyState,
  flashState,
  showProjectColumn,
  showSingleDestNs,
  onToggleSelect,
  onOpen,
  onSync,
  onRefresh,
  onContext,
}: AppRowProps) => {
  const opRunning = e.operationPhase === 'Running' || e.operationPhase === 'Terminating';
  const showSpinner = !!busyState || opRunning;
  const spinnerLabel = busyState === 'sync' || opRunning ? 'Syncing…' : busyState === 'refresh' ? 'Refreshing…' : '';
  return (
    <tr
      className={`${isSel ? 'selected' : ''} ${isFocused ? 'focused' : ''} ${showSpinner ? 'row-busy' : ''} ${flashState ? `row-flash row-flash-${flashState}` : ''}`}
      onContextMenu={(ev) => {
        ev.preventDefault();
        onContext(e, ev.clientX, ev.clientY);
      }}
    >
      <td className="argo-apps-checkbox-col">
        <input type="checkbox" checked={isSel} onChange={() => onToggleSelect(e)} />
      </td>
      <td className="argo-apps-name-cell">
        <button className="argo-apps-name-link" onClick={() => onOpen(e)}>{e.name}</button>
        {showSpinner && (
          <span className="argo-apps-row-spinner-wrap">
            <span className="argo-apps-row-spinner" />
            <span className="argo-apps-row-spinner-label">{spinnerLabel}</span>
          </span>
        )}
        <span className="argo-apps-row-actions">
          <button title="Sync (s)" disabled={!!busyState} onClick={(ev) => { ev.stopPropagation(); onSync(e); }}>
            {busyState === 'sync' ? '…' : 'Sync'}
          </button>
          <button title="Refresh (r)" disabled={!!busyState} onClick={(ev) => { ev.stopPropagation(); onRefresh(e); }}>
            {busyState === 'refresh' ? '…' : 'Refresh'}
          </button>
        </span>
      </td>
      <td className="argo-apps-mono argo-apps-dest" title={destinationTitle(e)}>
        {showSingleDestNs ? (
          <span className="argo-apps-ns">{e.namespace}</span>
        ) : (
          <>
            <span className="argo-apps-ns">{e.namespace}</span>
            <span className="argo-apps-arrow" aria-hidden="true">→</span>
            {e.destNamespace ? <span className="argo-apps-dest-ns">{e.destNamespace}</span> : <EmptyValue />}
          </>
        )}
        {destinationTarget(e) && <span className="argo-apps-dest-target">{destinationTarget(e)}</span>}
      </td>
      {showProjectColumn && <td>{e.project || <EmptyValue />}</td>}
      <td><StatusBadge tone={syncTone(e.syncStatus)} label={statusLabel(e.syncStatus)} /></td>
      <td><StatusBadge tone={healthTone(e.health)} label={statusLabel(e.health)} /></td>
      <td className="argo-apps-mono argo-apps-rev" title={e.revision || ''}>{e.revision ? e.revision.slice(0, 8) : <EmptyValue />}</td>
      <td className="argo-apps-time">{e.lastSyncedAt ? formatAge(e.lastSyncedAt) : <EmptyValue />}</td>
      <td className="argo-apps-time">{e.createdAt ? formatAge(e.createdAt) : <EmptyValue />}</td>
    </tr>
  );
}, (prev, next) => {
  return (
    prev.entry === next.entry &&
    prev.isSel === next.isSel &&
    prev.isFocused === next.isFocused &&
    prev.busyState === next.busyState &&
    prev.flashState === next.flashState &&
    prev.showProjectColumn === next.showProjectColumn &&
    prev.showSingleDestNs === next.showSingleDestNs &&
    prev.onToggleSelect === next.onToggleSelect &&
    prev.onOpen === next.onOpen &&
    prev.onSync === next.onSync &&
    prev.onRefresh === next.onRefresh &&
    prev.onContext === next.onContext
  );
});

/* Status semantics — one mapping for tiles, badges and group headers.
   Missing is amber rather than red: Argo itself shows it as a warning, and a
   missing app is usually a pending sync, not an outage. */
type Tone = 'success' | 'warning' | 'danger' | 'info' | 'neutral';

const syncTone = (status?: string): Tone => {
  switch ((status || '').toLowerCase()) {
    case 'synced': return 'success';
    case 'outofsync': return 'warning';
    default: return 'neutral';
  }
};

const healthTone = (health?: string): Tone => {
  switch ((health || '').toLowerCase()) {
    case 'healthy': return 'success';
    case 'degraded': return 'danger';
    case 'progressing': return 'info';
    case 'missing': return 'warning';
    default: return 'neutral';
  }
};

/** Argo's status identifiers in sentence case ("OutOfSync" → "Out of sync"). */
const statusLabel = (status?: string): string => {
  if (!status) return 'Unknown';
  if (status === 'OutOfSync') return 'Out of sync';
  return status;
};

const groupTone = (groupBy: GroupBy, key: string): Tone | null => {
  if (groupBy === 'health') return healthTone(key);
  if (groupBy === 'sync') return syncTone(key);
  return null;
};

/** In-cluster destinations are the default and stay quiet; anything else is named. */
const destinationTarget = (e: ArgoAppListEntry): string => {
  if (e.destName && e.destName !== 'in-cluster') return e.destName;
  const server = e.destServer || '';
  if (!server || server.includes('kubernetes.default.svc')) return '';
  return server.replace(/^https?:\/\//, '');
};

const destinationTitle = (e: ArgoAppListEntry): string => {
  const parts = [`Application namespace: ${e.namespace}`];
  if (e.destNamespace) parts.push(`Target namespace: ${e.destNamespace}`);
  if (e.destName) parts.push(`Cluster: ${e.destName}`);
  if (e.destServer) parts.push(`Server: ${e.destServer}`);
  return parts.join('\n');
};

const EmptyValue = () => <span className="argo-apps-empty-value">–</span>;

const StatusBadge = ({ tone, label }: { tone: Tone; label: string }) => (
  <span className={`ap-badge argo-apps-pill${tone === 'neutral' ? '' : ` ap-badge--${tone}`}`}>{label}</span>
);

const Pivot = ({ value, label, tone, active, onClick }: { value: number; label: string; tone?: Tone; active: boolean; onClick?: () => void }) => (
  <button
    type="button"
    className={`argo-apps-pivot${tone ? ` pivot-${tone}` : ''}${active ? ' active' : ''}${onClick ? '' : ' static'}`}
    onClick={onClick}
    disabled={!onClick}
    aria-pressed={onClick ? active : undefined}
  >
    <span className="argo-apps-pivot-value" data-zero={value === 0 ? 'true' : undefined}>{value}</span>
    <span className="argo-apps-pivot-label">{label}</span>
  </button>
);

const FilterChips = ({ label, values, active, onToggle }: { label: string; values: string[]; active: Set<string>; onToggle: (v: string) => void }) => (
  <div className="argo-apps-chip-group">
    <span className="argo-apps-chip-label">{label}</span>
    {values.map((v) => (
      <button key={v} className={`argo-apps-chip ${active.has(v) ? 'active' : ''}`} onClick={() => onToggle(v)}>{v}</button>
    ))}
  </div>
);

export default ArgoApplicationsPage;
