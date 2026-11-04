import React, { useMemo } from 'react';
import { SparkleIcon } from './AIIcons';
import { askAI } from './aiContext';
import { askContext, currentClusterLabel } from './currentContext';
import { ChipView } from './ChatParts';
import './AI.css';
import './AIChat.css';

export { paletteAskPosition } from './aiContext';

/** Opens the diagnosis sheet for the typed question with the current
 * cluster/namespace, plus the inspector resource only when the question
 * refers to it or the user added the suggestion. */
export function runPaletteAsk(query: string, cluster: string | null, addSuggested = false) {
  const text = query.trim();
  if (!text) return;
  askAI({
    source: 'palette',
    intent: 'ask',
    surface: 'sheet',
    cluster: cluster || undefined,
    text,
    title: text.length > 72 ? text.slice(0, 72) + '…' : text,
    chips: askContext(text, addSuggested).chips,
  });
}

function shortName(name: string, max = 24) {
  return name.length > max ? name.slice(0, max - 1) + '…' : name;
}

/** The palette's Ask AI row. `compact` is the one-line form shown under
 * search results when the input reads as a search first. */
export const PaletteAskRow: React.FC<{
  query: string;
  navIndex: number;
  selected: boolean;
  compact?: boolean;
  addSuggested?: boolean;
  onToggleSuggested?: (add: boolean) => void;
  onHover: (i: number) => void;
  onPick: () => void;
}> = ({ query, navIndex, selected, compact, addSuggested = false, onToggleSuggested, onHover, onPick }) => {
  const q = query.trim();
  const { chips, suggestion, added } = useMemo(() => {
    const base = askContext(q, false);
    const withAdded = addSuggested && base.suggestion ? askContext(q, true) : base;
    return { chips: withAdded.chips, suggestion: addSuggested ? null : base.suggestion, added: addSuggested && !!base.suggestion };
  }, [q, addSuggested]);
  const clusterLabel = useMemo(() => currentClusterLabel(), []);
  return (
    <div className="command-palette-section">
      <div
        className={`command-palette-result ai-palette-row${compact ? ' is-compact' : ''} ${selected ? 'selected' : ''}`}
        data-nav-index={navIndex}
        role="option"
        aria-selected={selected}
        onMouseEnter={() => onHover(navIndex)}
        onClick={onPick}
      >
        <span className="ai-tile" aria-hidden="true"><SparkleIcon size={compact ? 11 : 13} /></span>
        <div className="ai-palette-body">
          <div className="ai-palette-line">
            <span className="ai-palette-title">
              <span className="ai-palette-title-label">Ask AI</span>
              <span className="ai-palette-q">“{q}”</span>
            </span>
            {!compact && (
              <span className="ai-palette-sub">{clusterLabel ? `Diagnose in ${clusterLabel}` : 'Diagnose with your agent'} · read-only</span>
            )}
          </div>
          {!compact && (chips.length > 0 || suggestion) && (
            <div className="ai-palette-chips" onClick={(e) => e.stopPropagation()}>
              {chips.map((c, i) => (
                <ChipView
                  key={i}
                  c={c}
                  onRemove={added && c.type === 'resource' && i === chips.length - 1 ? () => onToggleSuggested?.(false) : undefined}
                />
              ))}
              {suggestion && (
                <button
                  type="button"
                  className="ai-palette-suggest"
                  title={`Add ${suggestion.label} to the context`}
                  onClick={(e) => {
                    e.stopPropagation();
                    onToggleSuggested?.(true);
                  }}
                >
                  + {suggestion.kind} {shortName(suggestion.name || suggestion.label)}
                </button>
              )}
            </div>
          )}
        </div>
        <span className="ai-palette-keys" aria-label="Tab or Command Enter">
          <kbd className="ap-kbd">⇥</kbd>
          <kbd className="ap-kbd">⌘↵</kbd>
        </span>
      </div>
    </div>
  );
};
