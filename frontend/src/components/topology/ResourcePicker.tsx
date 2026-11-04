// The header's "Go to" field: focus any resource the search index knows,
// jump to a namespace, or find a node already on screen.
import React, { useEffect, useId, useMemo, useRef, useState } from 'react';
import AnchoredPopover from '../common/AnchoredPopover';
import { search } from '../../services/api/search';
import { getResourceIcon } from '../../utils/resourceIcons';
import type { TopologyRef } from '../../services/api/topology';
import { TOPOLOGY_KINDS } from './topologyNav';
import type { ViewNode } from './graphModel';

export type PickTarget =
  | { type: 'node'; id: string }
  | { type: 'namespace'; namespace: string }
  | { type: 'resource'; ref: TopologyRef };

interface Row {
  key: string;
  group: string;
  label: string;
  hint: string;
  kind: string;
  target: PickTarget;
}

interface Props {
  cluster: string;
  namespaces: string[];
  /** Nodes on screen, searched first. */
  onScreen: ViewNode[];
  onPick: (t: PickTarget) => void;
  /** Bump to focus the field (keyboard shortcut). */
  focusSignal?: number;
}

const MAX_LOCAL = 6;

const ResourcePicker: React.FC<Props> = ({ cluster, namespaces, onScreen, onPick, focusSignal }) => {
  const inputRef = useRef<HTMLInputElement>(null);
  const [q, setQ] = useState('');
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(0);
  const [remote, setRemote] = useState<Row[]>([]);
  const [searching, setSearching] = useState(false);
  const listId = useId();

  useEffect(() => {
    if (focusSignal) inputRef.current?.focus();
  }, [focusSignal]);

  // The search index, debounced; only kinds the graph can relate.
  useEffect(() => {
    const term = q.trim();
    if (term.length < 2) { setRemote([]); setSearching(false); return; }
    const ctrl = new AbortController();
    setSearching(true);
    const t = window.setTimeout(() => {
      search(term, { clusters: [cluster], limit: 40 }, ctrl.signal)
        .then((results) => {
          const rows: Row[] = [];
          const seen = new Set<string>();
          for (const r of results) {
            const res = r.resource;
            if (!TOPOLOGY_KINDS.has(res.kind) || !res.namespace) continue;
            const key = `${res.kind}/${res.namespace}/${res.name}`;
            if (seen.has(key)) continue;
            seen.add(key);
            rows.push({
              key: `r:${key}`, group: 'Resources', label: res.name, hint: `${res.kind} · ${res.namespace}`, kind: res.kind,
              target: { type: 'resource', ref: { group: res.group, version: res.version || 'v1', kind: res.kind, namespace: res.namespace, name: res.name } },
            });
            if (rows.length >= 12) break;
          }
          setRemote(rows);
        })
        .catch(() => { if (!ctrl.signal.aborted) setRemote([]); })
        .finally(() => { if (!ctrl.signal.aborted) setSearching(false); });
    }, 160);
    return () => { ctrl.abort(); window.clearTimeout(t); };
  }, [q, cluster]);

  const rows = useMemo(() => {
    const term = q.trim().toLowerCase();
    const out: Row[] = [];
    const local = term
      ? onScreen.filter((v) => (v.members ? v.members.some((m) => m.name.toLowerCase().includes(term)) : v.node.name.toLowerCase().includes(term) || v.node.kind.toLowerCase() === term))
      : [];
    for (const v of local.slice(0, MAX_LOCAL)) {
      out.push({ key: `n:${v.id}`, group: 'In this view', label: v.members ? `${v.members.length} pods` : v.node.name, hint: v.node.kind, kind: v.node.kind, target: { type: 'node', id: v.id } });
    }
    const nsHits = namespaces.filter((ns) => !term || ns.toLowerCase().includes(term)).slice(0, term ? 5 : 8);
    for (const ns of nsHits) out.push({ key: `ns:${ns}`, group: 'Namespaces', label: ns, hint: 'Applications', kind: 'Namespace', target: { type: 'namespace', namespace: ns } });
    const onScreenKeys = new Set(local.map((v) => `r:${v.id}`));
    for (const r of remote) if (!onScreenKeys.has(r.key)) out.push(r);
    return out;
  }, [q, onScreen, namespaces, remote]);

  useEffect(() => setActive(0), [rows.length, q]);

  const pick = (r: Row) => {
    onPick(r.target);
    setQ('');
    setOpen(false);
    inputRef.current?.blur();
  };

  const groups = useMemo(() => {
    const out: { group: string; items: { row: Row; index: number }[] }[] = [];
    rows.forEach((row, index) => {
      let g = out.find((x) => x.group === row.group);
      if (!g) out.push((g = { group: row.group, items: [] }));
      g.items.push({ row, index });
    });
    return out;
  }, [rows]);

  const onKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'ArrowDown') { e.preventDefault(); setOpen(true); setActive((i) => Math.min(rows.length - 1, i + 1)); }
    else if (e.key === 'ArrowUp') { e.preventDefault(); setActive((i) => Math.max(0, i - 1)); }
    else if (e.key === 'Enter' && rows[active]) { e.preventDefault(); pick(rows[active]); }
    else if (e.key === 'Escape') { e.stopPropagation(); if (q) setQ(''); else { setOpen(false); inputRef.current?.blur(); } }
  };

  useEffect(() => {
    document.getElementById(`${listId}-${active}`)?.scrollIntoView({ block: 'nearest' });
  }, [active, listId]);

  return (
    <div className="topo-picker">
      <label className="ap-search ap-search--sm topo-picker-field">
        <svg viewBox="0 0 16 16" aria-hidden><circle cx="7" cy="7" r="4.5" fill="none" stroke="currentColor" strokeWidth="1.6" /><path d="M10.5 10.5 14 14" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" /></svg>
        <input
          ref={inputRef}
          value={q}
          placeholder="Go to resource or namespace"
          aria-label="Go to resource or namespace"
          role="combobox"
          aria-expanded={open && rows.length > 0}
          aria-controls={listId}
          aria-activedescendant={open && rows[active] ? `${listId}-${active}` : undefined}
          autoComplete="off"
          spellCheck={false}
          onFocus={() => setOpen(true)}
          onClick={() => setOpen(true)}
          onBlur={() => setOpen(false)}
          onChange={(e) => { setQ(e.target.value); setOpen(true); }}
          onKeyDown={onKeyDown}
        />
        {searching ? <span className="topo-spinner" aria-label="Searching" /> : <kbd className="topo-kbd" aria-hidden>/</kbd>}
      </label>
      <AnchoredPopover anchor={inputRef} open={open && rows.length > 0} id={listId} minWidth={340} maxHeight={380} aria-label="Go to">
        {groups.map((g) => (
          <div key={g.group} className="ap-combobox-group" role="group" aria-label={g.group}>
            <div className="ap-combobox-group-label">{g.group}</div>
            {g.items.map(({ row, index }) => (
              <div
                key={row.key}
                id={`${listId}-${index}`}
                role="option"
                aria-selected={index === active}
                className={`ap-menu-item ap-combobox-option topo-picker-row${index === active ? ' is-highlighted' : ''}`}
                onMouseEnter={() => setActive(index)}
                onClick={() => pick(row)}
              >
                <span className="topo-picker-icon" aria-hidden>{getResourceIcon(row.kind)}</span>
                <span className="ap-combobox-option-label">{row.label}</span>
                <span className="ap-combobox-option-hint">{row.hint}</span>
              </div>
            ))}
          </div>
        ))}
      </AnchoredPopover>
    </div>
  );
};

export default ResourcePicker;
