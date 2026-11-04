import React, { useMemo } from 'react';
import { SparkleIcon } from './AIIcons';
import { askAI, looksLikeQuestion } from './aiContext';
import { currentContextChips } from './currentContext';
import { ChipView } from './ChatParts';
import './AI.css';
import './AIChat.css';

const REACH_ID = 'cmd:can-reach-run';

/**
 * Where the palette's "Ask AI" row goes: first when the input reads like a
 * question, after the network "can X reach Y" result when that matched, and
 * nowhere for ">" commands, cluster switching or plain resource searches.
 */
export function paletteAskPosition(query: string, results: { resource?: { id?: string } }[], clusterMode: boolean): 'first' | 'last' | null {
  const q = query.trim();
  if (clusterMode || q.startsWith('>') || !looksLikeQuestion(q)) return null;
  if (results.some((r) => r.resource?.id === REACH_ID)) return 'last';
  return 'first';
}

/** Opens the diagnosis sheet for the typed question with the current context. */
export function runPaletteAsk(query: string, cluster: string | null) {
  const text = query.trim();
  if (!text) return;
  askAI({
    source: 'palette',
    intent: 'ask',
    surface: 'sheet',
    cluster: cluster || undefined,
    text,
    title: text.length > 72 ? text.slice(0, 72) + '…' : text,
    chips: currentContextChips(),
  });
}

/** The "Ask AI: <question>" palette row. */
export const PaletteAskRow: React.FC<{ query: string; navIndex: number; selected: boolean; onHover: (i: number) => void; onPick: () => void }> = ({ query, navIndex, selected, onHover, onPick }) => {
  const chips = useMemo(() => currentContextChips(), []);
  return (
    <div className="command-palette-section">
      <div
        className={`command-palette-result ai-palette-row ${selected ? 'selected' : ''}`}
        data-nav-index={navIndex}
        role="option"
        aria-selected={selected}
        onMouseEnter={() => onHover(navIndex)}
        onClick={onPick}
      >
        <div className="ai-palette-ask">
          <span className="ai-tile" aria-hidden="true"><SparkleIcon size={13} /></span>
          <div className="command-palette-result-content">
            <div className="command-palette-result-name ap-truncate">Ask AI: {query.trim()}</div>
            <div className="command-palette-result-meta">Diagnose with your agent, read-only · ⇥ or ⌘↵</div>
          </div>
          {chips.length > 0 && <span className="ai-palette-chips">{chips.slice(0, 3).map((c, i) => <ChipView key={i} c={c} />)}</span>}
        </div>
      </div>
    </div>
  );
};
