import React, { useEffect, useState } from 'react';
import { DiffEditor } from '../MonacoEditor';
import { installKanivetMonacoTheme, KANIVET_MONACO_THEME } from '../../utils/monacoTheme';
import { getChangeSnapshot } from '../../services/api/changes';
import type { ChangeEntry } from '../../types/changes';

interface Props {
  cluster: string;
  base: ChangeEntry | null;
  target: ChangeEntry;
  candidates: ChangeEntry[];
  onBaseChange: (id: string | null) => void;
}

const useSnapshot = (cluster: string, e: ChangeEntry | null) => {
  const [state, setState] = useState<{ text: string; loading: boolean; error: string | null }>({ text: '', loading: false, error: null });
  useEffect(() => {
    if (!e) { setState({ text: '', loading: false, error: null }); return; }
    if (e.snapshot || !e.hasSnapshot) { setState({ text: e.snapshot || '', loading: false, error: null }); return; }
    let cancelled = false;
    setState({ text: '', loading: true, error: null });
    getChangeSnapshot(cluster, e.id)
      .then((text) => { if (!cancelled) setState({ text, loading: false, error: null }); })
      .catch((err) => { if (!cancelled) setState({ text: '', loading: false, error: err?.message || 'Could not load revision' }); });
    return () => { cancelled = true; };
  }, [cluster, e?.id, e?.snapshot, e?.hasSnapshot]);
  return state;
};

const label = (e: ChangeEntry) => `${e.revision ? `rev ${e.revision}` : e.title} · ${new Date(e.time).toLocaleString()}`;

const RevisionDiff: React.FC<Props> = ({ cluster, base, target, candidates, onBaseChange }) => {
  const [sideBySide, setSideBySide] = useState(true);
  const a = useSnapshot(cluster, base);
  const b = useSnapshot(cluster, target);
  const loading = a.loading || b.loading;
  const error = a.error || b.error;

  return (
    <div className="chg-diff">
      <div className="chg-diff-bar">
        <label className="chg-diff-pick">
          <span>Compare</span>
          <select
            className="ap-select"
            value={base?.id || ''}
            onChange={(ev) => onBaseChange(ev.target.value || null)}
          >
            <option value="">Nothing (show revision)</option>
            {candidates.filter((c) => c.id !== target.id).map((c) => (
              <option key={c.id} value={c.id}>{label(c)}</option>
            ))}
          </select>
          <span>with {target.revision ? `rev ${target.revision}` : 'this entry'}</span>
        </label>
        <div className="ap-segmented ap-segmented--sm" role="group" aria-label="Diff layout">
          <button type="button" aria-pressed={sideBySide} onClick={() => setSideBySide(true)}>Side by side</button>
          <button type="button" aria-pressed={!sideBySide} onClick={() => setSideBySide(false)}>Unified</button>
        </div>
      </div>
      {target.diff && target.diff.length > 0 && base && base.source === target.source && (
        <div className="chg-diff-fields">
          {target.diff.slice(0, 12).map((d) => (
            <div key={d.path} className="chg-diff-field">
              <code>{d.path}</code>
              <span className="chg-diff-old">{d.old ?? '—'}</span>
              <span aria-hidden="true">→</span>
              <span className="chg-diff-new">{d.new ?? '—'}</span>
            </div>
          ))}
          {target.diff.length > 12 && <div className="chg-more">{target.diff.length - 12} more fields in the diff below</div>}
        </div>
      )}
      <div className="chg-diff-editor">
        {error ? (
          <div className="chg-empty">{error}</div>
        ) : loading ? (
          <div className="chg-empty">Loading revision…</div>
        ) : (
          <DiffEditor
            original={a.text}
            modified={b.text}
            language="yaml"
            theme={KANIVET_MONACO_THEME}
            beforeMount={installKanivetMonacoTheme}
            options={{
              renderSideBySide: sideBySide,
              readOnly: true,
              minimap: { enabled: false },
              scrollBeyondLastLine: false,
              automaticLayout: true,
              fontSize: 11.5,
              fontFamily: "ui-monospace, 'SF Mono', Menlo, Monaco, monospace",
              renderOverviewRuler: false,
              hideUnchangedRegions: { enabled: !!base },
            }}
          />
        )}
      </div>
    </div>
  );
};

export default RevisionDiff;
