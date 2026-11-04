import React, { useEffect, useMemo, useRef, useState } from 'react';
import { Cross2Icon } from '@radix-ui/react-icons';
import type { NetRef, Target } from '../../types/network';
import { getResourceIcon } from '../../utils/resourceIcons';
import { filterTargets, parseEndpoint, refKey, refLabel } from './networkUtils';

interface TargetPickerProps {
  label: string;
  role: 'source' | 'destination';
  targets: Target[];
  value: NetRef | null;
  onChange: (ref: NetRef | null, target?: Target) => void;
  autoFocus?: boolean;
}

/** A searchable combobox over the cluster's workloads, pods and Services. A destination also takes an IP. */
const TargetPicker: React.FC<TargetPickerProps> = ({ label, role, targets, value, onChange, autoFocus }) => {
  const [query, setQuery] = useState('');
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const listId = `net-picker-${role}`;

  const options = useMemo(() => filterTargets(targets, query, role), [targets, query, role]);
  const typedIP = useMemo(() => {
    if (role !== 'destination') return null;
    const p = parseEndpoint(query);
    return p?.kind === 'IP' && p.ip ? p.ip : null;
  }, [query, role]);
  const rows: { ref: NetRef; target?: Target }[] = useMemo(
    () => [...(typedIP ? [{ ref: { kind: 'IP', ip: typedIP } as NetRef }] : []), ...options.map((t) => ({ ref: t.ref, target: t }))],
    [options, typedIP],
  );

  useEffect(() => { setActive(0); }, [query]);

  const pick = (i: number) => {
    const row = rows[i];
    if (!row) return;
    onChange(row.ref, row.target);
    setQuery('');
    setOpen(false);
  };

  const onKey = (e: React.KeyboardEvent) => {
    if (e.key === 'ArrowDown') { e.preventDefault(); setOpen(true); setActive((a) => Math.min(a + 1, rows.length - 1)); }
    else if (e.key === 'ArrowUp') { e.preventDefault(); setActive((a) => Math.max(a - 1, 0)); }
    else if (e.key === 'Enter' && open) { e.preventDefault(); pick(active); }
    else if (e.key === 'Escape') { setOpen(false); }
  };

  return (
    <div className="net-picker">
      <span className="net-picker-label">{label}</span>
      {value && !open ? (
        <button type="button" className="net-picker-value" onClick={() => { setOpen(true); setTimeout(() => inputRef.current?.focus(), 0); }}>
          <span className="net-picker-icon">{getResourceIcon(value.kind === 'IP' ? 'network' : value.kind)}</span>
          <span className="net-picker-kind">{value.kind}</span>
          <span className="ap-truncate">{refLabel(value)}</span>
          <span
            role="button"
            tabIndex={0}
            className="net-picker-clear"
            aria-label={`Clear ${label.toLowerCase()}`}
            onClick={(e) => { e.stopPropagation(); onChange(null); }}
            onKeyDown={(e) => { if (e.key === 'Enter') { e.stopPropagation(); onChange(null); } }}
          >
            <Cross2Icon />
          </span>
        </button>
      ) : (
        <input
          ref={inputRef}
          className="ap-input net-picker-input"
          placeholder={role === 'source' ? 'Pod or workload…' : 'Service, pod, workload or IP…'}
          value={query}
          autoFocus={autoFocus}
          role="combobox"
          aria-expanded={open}
          aria-controls={listId}
          aria-label={label}
          onFocus={() => setOpen(true)}
          onBlur={() => setTimeout(() => setOpen(false), 150)}
          onChange={(e) => { setQuery(e.target.value); setOpen(true); }}
          onKeyDown={onKey}
        />
      )}
      {open && rows.length > 0 && (
        <div className="ap-menu net-picker-menu" id={listId} role="listbox">
          {rows.map((r, i) => (
            <div
              key={refKey(r.ref)}
              role="option"
              aria-selected={i === active}
              className={`ap-menu-item net-picker-option${i === active ? ' is-active' : ''}`}
              onMouseDown={(e) => { e.preventDefault(); pick(i); }}
              onMouseEnter={() => setActive(i)}
            >
              <span className="net-picker-icon">{getResourceIcon(r.ref.kind === 'IP' ? 'network' : r.ref.kind)}</span>
              <span className="net-picker-kind">{r.ref.kind}</span>
              <span className="ap-truncate">{r.ref.kind === 'IP' ? `${r.ref.ip} (address)` : refLabel(r.ref)}</span>
              {r.target && r.target.ports && r.target.ports.length > 0 && (
                <span className="net-picker-ports ap-tnum">{r.target.ports.slice(0, 3).join(', ')}{r.target.ports.length > 3 ? '…' : ''}</span>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  );
};

export default TargetPicker;
