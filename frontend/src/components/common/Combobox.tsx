import { useId, useMemo, useRef, useState, type KeyboardEvent } from 'react';
import AnchoredPopover from './AnchoredPopover';

export interface ComboboxOption {
  value: string;
  /** Shown on the right of the row, quieter (an API group, a port name). */
  hint?: string;
  /** Rows with the same group are listed together under its label. */
  group?: string;
}

interface ComboboxProps {
  value: string;
  onChange: (value: string) => void;
  options: ComboboxOption[];
  placeholder?: string;
  className?: string;
  inputClassName?: string;
  'aria-label'?: string;
  inputMode?: 'text' | 'numeric';
  /** Filters what is typed before it reaches onChange (digits only, …). */
  sanitize?: (raw: string) => string;
  onEnter?: () => void;
  /** Minimum menu width, for narrow inputs with long suggestions. */
  menuMinWidth?: number;
}

/** Case-insensitive substring match; an exact value still lists its group. */
export function filterOptions(options: ComboboxOption[], query: string): ComboboxOption[] {
  const q = query.trim().toLowerCase();
  if (!q) return options;
  return options.filter((o) => o.value.toLowerCase().includes(q) || (o.hint || '').toLowerCase().includes(q));
}

/**
 * A themed text field with a suggestion list: what a native `<datalist>`
 * does, without the unstyled OS popup. Free text is always allowed.
 */
export default function Combobox({
  value,
  onChange,
  options,
  placeholder,
  className,
  inputClassName,
  inputMode,
  sanitize,
  onEnter,
  menuMinWidth = 220,
  ...rest
}: ComboboxProps) {
  const inputRef = useRef<HTMLInputElement>(null);
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(-1);
  // While the field shows a picked value, list everything rather than the one match.
  const [typed, setTyped] = useState(false);
  const listId = useId();

  const rows = useMemo(() => filterOptions(options, typed ? value : ''), [options, value, typed]);
  const grouped = useMemo(() => {
    const out: { group: string; items: { option: ComboboxOption; index: number }[] }[] = [];
    rows.forEach((option, index) => {
      const g = option.group || '';
      let bucket = out.find((b) => b.group === g);
      if (!bucket) out.push((bucket = { group: g, items: [] }));
      bucket.items.push({ option, index });
    });
    return out;
  }, [rows]);
  // Keyboard order follows display order (by group).
  const ordered = useMemo(() => grouped.flatMap((g) => g.items.map((i) => i.index)), [grouped]);

  const pick = (option: ComboboxOption) => {
    onChange(option.value);
    setTyped(false);
    setOpen(false);
    setActive(-1);
  };

  const move = (delta: number) => {
    if (!ordered.length) return;
    const at = ordered.indexOf(active);
    const next = at < 0 ? (delta > 0 ? 0 : ordered.length - 1) : Math.max(0, Math.min(ordered.length - 1, at + delta));
    setActive(ordered[next]);
    document.getElementById(`${listId}-${ordered[next]}`)?.scrollIntoView({ block: 'nearest' });
  };

  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      if (!open) setOpen(true);
      move(1);
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      move(-1);
    } else if (e.key === 'Enter') {
      if (open && active >= 0 && rows[active]) {
        e.preventDefault();
        pick(rows[active]);
      } else {
        setOpen(false);
        onEnter?.();
      }
    } else if (e.key === 'Escape' && open) {
      e.stopPropagation();
      setOpen(false);
    }
  };

  return (
    <div className={`ap-combobox${className ? ` ${className}` : ''}`}>
      <input
        ref={inputRef}
        className={`ap-input${inputClassName ? ` ${inputClassName}` : ''}`}
        role="combobox"
        aria-autocomplete="list"
        aria-expanded={open && rows.length > 0}
        aria-controls={listId}
        aria-activedescendant={open && active >= 0 ? `${listId}-${active}` : undefined}
        aria-label={rest['aria-label']}
        autoComplete="off"
        spellCheck={false}
        inputMode={inputMode}
        placeholder={placeholder}
        value={value}
        onFocus={() => setOpen(true)}
        onClick={() => setOpen(true)}
        onBlur={() => setOpen(false)}
        onChange={(e) => {
          onChange(sanitize ? sanitize(e.target.value) : e.target.value);
          setTyped(true);
          setOpen(true);
          setActive(-1);
        }}
        onKeyDown={onKeyDown}
      />
      <AnchoredPopover anchor={inputRef} open={open && rows.length > 0} id={listId} minWidth={menuMinWidth} aria-label={rest['aria-label']}>
        {grouped.map((g) => (
          <div key={g.group || '_'} className="ap-combobox-group" role="group" aria-label={g.group || undefined}>
            {g.group && <div className="ap-combobox-group-label">{g.group}</div>}
            {g.items.map(({ option, index }) => (
              <div
                key={`${option.value}-${index}`}
                id={`${listId}-${index}`}
                role="option"
                aria-selected={index === active}
                className={`ap-menu-item ap-combobox-option${index === active ? ' is-highlighted' : ''}`}
                onMouseEnter={() => setActive(index)}
                onClick={() => pick(option)}
              >
                <span className="ap-combobox-option-label">{option.value}</span>
                {option.hint && <span className="ap-combobox-option-hint">{option.hint}</span>}
              </div>
            ))}
          </div>
        ))}
      </AnchoredPopover>
    </div>
  );
}
