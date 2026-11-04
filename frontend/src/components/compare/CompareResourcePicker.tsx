import React, { useEffect, useMemo, useRef, useState } from 'react';
import { MagnifyingGlassIcon } from '@radix-ui/react-icons';
import api from '../../services/api';
import { search } from '../../services/api/search';
import type { SearchResult } from '../../types/search';
import type { CompareRef, CompareSide, CompareSource } from '../../types/compare';
import ResourceLink from '../ResourceLink';
import { getResourceIcon } from '../../utils/resourceIcons';

interface Props {
  side: 'A' | 'B';
  value: CompareRef | null;
  clusters: string[];
  defaultCluster: string;
  result?: CompareSide;
  onChange: (ref: CompareRef | null) => void;
}

const SOURCES: { id: CompareSource; label: string }[] = [
  { id: 'live', label: 'Live' },
  { id: 'last-applied', label: 'Last applied' },
  { id: 'helm', label: 'Helm' },
];

/**
 * One side of a comparison: cluster and namespace selectors, a source
 * (live object, its last-applied configuration or its Helm manifest) and a
 * search box over the resource index. Changing the cluster or namespace keeps
 * the kind and name, so "the same Deployment in prod" is one click.
 */
const CompareResourcePicker: React.FC<Props> = ({ side, value, clusters, defaultCluster, result, onChange }) => {
  const [pickCluster, setPickCluster] = useState(defaultCluster);
  const cluster = value?.cluster || pickCluster || defaultCluster;
  const [namespaces, setNamespaces] = useState<string[]>([]);
  const [query, setQuery] = useState('');
  const [results, setResults] = useState<SearchResult[]>([]);
  const [open, setOpen] = useState(false);
  const [searching, setSearching] = useState(false);
  const [highlight, setHighlight] = useState(0);
  const [nsFilter, setNsFilter] = useState<string>('');
  const boxRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!cluster) return;
    let live = true;
    api.getNamespaces(cluster).then((ns) => { if (live) setNamespaces(ns); }).catch(() => { if (live) setNamespaces([]); });
    return () => { live = false; };
  }, [cluster]);

  useEffect(() => { setNsFilter(value?.namespace || ''); }, [value?.namespace]);

  useEffect(() => {
    if (!open) return;
    const q = query.trim();
    if (q.length < 2) { setResults([]); return; }
    const ctrl = new AbortController();
    setSearching(true);
    const t = setTimeout(() => {
      search(q, { clusters: [cluster], namespaces: nsFilter ? [nsFilter] : undefined, limit: 20 }, ctrl.signal)
        .then((r) => { setResults(r); setHighlight(0); })
        .catch(() => { if (!ctrl.signal.aborted) setResults([]); })
        .finally(() => { if (!ctrl.signal.aborted) setSearching(false); });
    }, 150);
    return () => { clearTimeout(t); ctrl.abort(); };
  }, [query, cluster, nsFilter, open]);

  useEffect(() => {
    const onDown = (e: MouseEvent) => {
      if (boxRef.current && !boxRef.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener('mousedown', onDown);
    return () => document.removeEventListener('mousedown', onDown);
  }, []);

  const clusterOptions = useMemo(() => {
    const set = new Set(clusters);
    if (cluster) set.add(cluster);
    return Array.from(set);
  }, [clusters, cluster]);

  const pick = (r: SearchResult) => {
    const res = r.resource;
    onChange({
      cluster: res.cluster || cluster,
      group: res.group || '',
      version: res.version || 'v1',
      kind: res.kind,
      namespace: res.namespace || '',
      name: res.name,
      source: value?.source || 'live',
    });
    setQuery('');
    setOpen(false);
  };

  const retarget = (patch: Partial<CompareRef>) => {
    if (value) onChange({ ...value, ...patch });
  };

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'ArrowDown') { e.preventDefault(); setHighlight((h) => Math.min(h + 1, results.length - 1)); }
    else if (e.key === 'ArrowUp') { e.preventDefault(); setHighlight((h) => Math.max(h - 1, 0)); }
    else if (e.key === 'Enter' && results[highlight]) { e.preventDefault(); pick(results[highlight]); }
    else if (e.key === 'Escape') { setOpen(false); }
  };

  const apiVersion = value ? (value.group ? `${value.group}/${value.version}` : value.version) : '';

  return (
    <div className={`compare-picker${result?.error ? ' has-error' : ''}`} ref={boxRef}>
      <div className="compare-picker-head">
        <span className={`compare-side-tag compare-side-tag--${side.toLowerCase()}`}>{side}</span>
        {value ? (
          <span className="compare-picker-current">
            <span className="compare-picker-kind">{value.kind}</span>
            <ResourceLink cluster={value.cluster} apiVersion={apiVersion} kind={value.kind} name={value.name} namespace={value.namespace || undefined} className="compare-picker-name">
              {value.namespace ? `${value.namespace}/` : ''}{value.name}
            </ResourceLink>
          </span>
        ) : (
          <span className="compare-picker-placeholder">Choose a resource</span>
        )}
      </div>
      <div className="compare-picker-controls">
        <select
          className="ap-select compare-picker-select"
          value={cluster}
          aria-label={`Cluster for ${side}`}
          title="Cluster"
          onChange={(e) => (value ? retarget({ cluster: e.target.value }) : setPickCluster(e.target.value))}
        >
          {clusterOptions.map((c) => <option key={c} value={c}>{c}</option>)}
        </select>
        <select
          className="ap-select compare-picker-select"
          value={value ? value.namespace || '' : nsFilter}
          aria-label={`Namespace for ${side}`}
          title="Namespace"
          onChange={(e) => (value && value.namespace ? retarget({ namespace: e.target.value }) : setNsFilter(e.target.value))}
        >
          {(!value || !value.namespace) && <option value="">{value ? 'Cluster-scoped' : 'All namespaces'}</option>}
          {namespaces.map((n) => <option key={n} value={n}>{n}</option>)}
        </select>
        <div className="ap-segmented ap-segmented--sm compare-picker-source" role="group" aria-label={`Source for ${side}`}>
          {SOURCES.map((s) => (
            <button
              key={s.id}
              type="button"
              aria-pressed={(value?.source || 'live') === s.id}
              disabled={!value}
              onClick={() => retarget({ source: s.id })}
              title={s.id === 'live' ? 'The object as it is now' : s.id === 'last-applied' ? 'What kubectl apply last declared' : 'What the Helm release rendered'}
            >
              {s.label}
            </button>
          ))}
        </div>
      </div>
      <div className="compare-picker-search">
        <label className="ap-search ap-search--sm">
          <MagnifyingGlassIcon />
          <input
            value={query}
            placeholder={value ? 'Search for another resource…' : 'Search by name, kind or label…'}
            onFocus={() => setOpen(true)}
            onChange={(e) => { setQuery(e.target.value); setOpen(true); }}
            onKeyDown={onKeyDown}
            aria-label={`Search resource ${side}`}
          />
        </label>
        {open && query.trim().length >= 2 && (
          <div className="ap-menu compare-picker-menu" role="listbox">
            {searching && results.length === 0 && <div className="compare-picker-menu-empty">Searching…</div>}
            {!searching && results.length === 0 && <div className="compare-picker-menu-empty">No matches in {cluster}</div>}
            {results.map((r, i) => {
              return (
                <div
                  key={r.resource.id}
                  role="option"
                  aria-selected={i === highlight}
                  className={`ap-menu-item compare-picker-option${i === highlight ? ' is-highlighted' : ''}`}
                  onMouseEnter={() => setHighlight(i)}
                  onMouseDown={(e) => { e.preventDefault(); pick(r); }}
                >
                  <span className="compare-picker-option-main">
                    <span className="compare-picker-option-icon">{getResourceIcon(r.resource.kind)}</span>
                    <span className="ap-truncate">{r.resource.name}</span>
                  </span>
                  <span className="ap-menu-shortcut">{r.resource.kind}{r.resource.namespace ? ` · ${r.resource.namespace}` : ''}</span>
                </div>
              );
            })}
          </div>
        )}
      </div>
      {result?.error && <div className="compare-picker-error" role="alert">{result.error}</div>}
    </div>
  );
};

export default CompareResourcePicker;
