import React, { useEffect, useMemo, useRef, useState } from 'react';
import {
  CheckIcon,
  ChevronDownIcon,
  MagnifyingGlassIcon,
} from '@radix-ui/react-icons';
import type { FacetValue } from './rightsizingView';

interface Props {
  label: string;
  values: FacetValue[];
  selected: Set<string>;
  onChange: (next: Set<string>) => void;
  /** Shown when there is nothing to pick, e.g. no workload carries the label. */
  emptyHint?: string;
}

/** A multi-select filter with counts, searchable once the list is long. */
export const FacetMenu: React.FC<Props> = ({
  label,
  values,
  selected,
  onChange,
  emptyHint,
}) => {
  const [open, setOpen] = useState(false);
  const [q, setQ] = useState('');
  const root = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const close = (e: MouseEvent) => {
      if (root.current && !root.current.contains(e.target as Node))
        setOpen(false);
    };
    const esc = (e: KeyboardEvent) => e.key === 'Escape' && setOpen(false);
    document.addEventListener('mousedown', close);
    document.addEventListener('keydown', esc);
    return () => {
      document.removeEventListener('mousedown', close);
      document.removeEventListener('keydown', esc);
    };
  }, [open]);

  const shown = useMemo(() => {
    const needle = q.trim().toLowerCase();
    return needle
      ? values.filter((v) => v.value.toLowerCase().includes(needle))
      : values;
  }, [values, q]);

  const toggle = (v: string) => {
    const next = new Set(selected);
    if (next.has(v)) next.delete(v);
    else next.add(v);
    onChange(next);
  };

  const summary =
    selected.size === 0
      ? 'All'
      : selected.size === 1
        ? [...selected][0]
        : `${selected.size} selected`;

  return (
    <div className="rs-facet" ref={root}>
      <button
        className={`rs-facet-btn${selected.size > 0 ? ' is-active' : ''}`}
        onClick={() => setOpen(!open)}
        aria-haspopup="listbox"
        aria-expanded={open}
        disabled={values.length === 0 && selected.size === 0}
        title={values.length === 0 ? emptyHint : undefined}
      >
        <span className="rs-facet-label">{label}</span>
        <span className="rs-facet-value">{summary}</span>
        <ChevronDownIcon />
      </button>
      {open && (
        <div
          className="ap-menu rs-facet-menu"
          role="listbox"
          aria-multiselectable="true"
        >
          {values.length > 8 && (
            <div className="ap-search ap-search--sm rs-facet-search">
              <MagnifyingGlassIcon />
              <input
                autoFocus
                value={q}
                onChange={(e) => setQ(e.target.value)}
                placeholder={`Find ${label.toLowerCase()}`}
              />
            </div>
          )}
          <div className="rs-facet-list">
            {shown.map((v) => (
              <button
                key={v.value}
                role="option"
                aria-selected={selected.has(v.value)}
                className="rs-facet-option"
                onClick={() => toggle(v.value)}
              >
                <span
                  className={`rs-check${selected.has(v.value) ? ' on' : ''}`}
                >
                  {selected.has(v.value) && <CheckIcon />}
                </span>
                <span className="rs-facet-option-label">{v.value}</span>
                <span className="rs-facet-count">{v.count}</span>
              </button>
            ))}
            {shown.length === 0 && (
              <div className="rs-facet-empty">No matches</div>
            )}
          </div>
          {selected.size > 0 && (
            <button
              className="rs-facet-clear"
              onClick={() => onChange(new Set())}
            >
              Clear {label.toLowerCase()}
            </button>
          )}
        </div>
      )}
    </div>
  );
};
