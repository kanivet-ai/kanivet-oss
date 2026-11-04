import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { ArrowRightIcon, CheckCircledIcon, ShuffleIcon, ReloadIcon } from '@radix-ui/react-icons';
import { useStore } from '../../store';
import { DiffEditor } from '../MonacoEditor';
import { installKanivetMonacoTheme, KANIVET_MONACO_THEME } from '../../utils/monacoTheme';
import { compareResources } from '../../services/api/compare';
import type { CompareInsight, CompareOptions, CompareRef, CompareResult } from '../../types/compare';
import { isCompleteRef, lineForPath, refKey } from '../../utils/compareRefs';
import { failureMessage } from '../../utils/errorMessage';
import CompareResourcePicker from './CompareResourcePicker';
import { setCompareRefs, useCompareRequest } from './compareLauncher';
import './ComparePage.css';

const CATEGORY_LABELS: Record<string, string> = {
  kind: 'Kind',
  identity: 'Identity',
  image: 'Image',
  replicas: 'Replicas',
  env: 'Env',
  resources: 'Resources',
  ports: 'Ports',
  probes: 'Probes',
  volumes: 'Volumes',
  scheduling: 'Scheduling',
  labels: 'Labels',
  annotations: 'Annotations',
  data: 'Data',
  spec: 'Spec',
  status: 'Status',
  other: 'Metadata',
};

const TOGGLES: { key: keyof CompareOptions; label: string; title: string; driftOnly?: boolean }[] = [
  { key: 'includeStatus', label: 'Status', title: 'Include .status' },
  { key: 'includeMetadata', label: 'Metadata', title: 'Include server-managed metadata (uid, resourceVersion, managedFields, …)' },
  { key: 'keepDefaults', label: 'Defaults', title: 'Include server defaults and cluster-assigned fields' },
  { key: 'includeUndeclared', label: 'Undeclared', title: 'Include live fields the declared version never set', driftOnly: true },
];

const ComparePage: React.FC<{ cluster: string }> = ({ cluster }) => {
  const { a, b, seq } = useCompareRequest();
  const clusters = useStore((s) => s.clusters);
  const [options, setOptions] = useState<CompareOptions>({});
  const [result, setResult] = useState<CompareResult | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [sideBySide, setSideBySide] = useState(true);
  const [changedOnly, setChangedOnly] = useState(true);
  const [reload, setReload] = useState(0);
  const diffRef = useRef<any>(null);

  const ready = isCompleteRef(a) && isCompleteRef(b);
  const isDrift = !!a && !!b && ((a.source || 'live') === 'live') !== ((b.source || 'live') === 'live');

  useEffect(() => {
    if (!ready) { setResult(null); setError(null); return; }
    const ctrl = new AbortController();
    setLoading(true);
    setError(null);
    compareResources(a!, b!, options, ctrl.signal)
      .then((r) => setResult(r))
      .catch((e) => { if (!ctrl.signal.aborted) { setError(failureMessage('Could not compare', e)); setResult(null); } })
      .finally(() => { if (!ctrl.signal.aborted) setLoading(false); });
    return () => ctrl.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [refKey(a), refKey(b), options.includeStatus, options.includeMetadata, options.keepDefaults, options.includeUndeclared, seq, reload, ready]);

  const setA = useCallback((r: CompareRef | null) => setCompareRefs(r, b), [b]);
  const setB = useCallback((r: CompareRef | null) => setCompareRefs(a, r), [a]);
  const swap = () => setCompareRefs(b, a);

  const reveal = (ins: CompareInsight) => {
    const ed = diffRef.current;
    const path = ins.paths?.[0];
    if (!ed || !path || !result) return;
    const lb = lineForPath(result.b.yaml, path);
    const la = lineForPath(result.a.yaml, path);
    const target = lb ? ed.getModifiedEditor() : ed.getOriginalEditor();
    const line = lb || la;
    if (!line) return;
    target.revealLineInCenter(line);
    target.setPosition({ lineNumber: line, column: 1 });
    target.focus();
  };

  const sideError = !!(result?.a.error || result?.b.error);
  const stats = result?.stats;
  const statLine = useMemo(() => {
    if (!stats) return '';
    const parts = [];
    if (stats.changed) parts.push(`${stats.changed} changed`);
    if (stats.added) parts.push(`${stats.added} only in B`);
    if (stats.removed) parts.push(`${stats.removed} only in A`);
    return parts.join(' · ');
  }, [stats]);

  return (
    <div className="compare-page">
      <div className="compare-toolbar">
        <div className="compare-toolbar-title">
          <span className="compare-title">Compare</span>
          {result && !sideError && (
            <span className="compare-subtitle ap-tnum">{result.identical ? 'No differences' : statLine}</span>
          )}
        </div>
        <div className="compare-toolbar-actions">
          <span className="compare-toolbar-label">Include</span>
          <div className="ap-segmented ap-segmented--sm" role="group" aria-label="Normalization">
            {TOGGLES.filter((t) => !t.driftOnly || isDrift).map((t) => (
              <button
                key={t.key}
                type="button"
                aria-pressed={!!options[t.key]}
                title={t.title}
                onClick={() => setOptions((o) => ({ ...o, [t.key]: !o[t.key] }))}
              >
                {t.label}
              </button>
            ))}
          </div>
          <div className="ap-segmented ap-segmented--sm" role="group" aria-label="Diff layout">
            <button type="button" aria-pressed={changedOnly} onClick={() => setChangedOnly((v) => !v)} title="Collapse unchanged regions">Changes only</button>
            <button type="button" aria-pressed={!sideBySide} onClick={() => setSideBySide((v) => !v)} title="Inline diff">Inline</button>
          </div>
          <button className="ap-icon-btn ap-icon-btn--sm" type="button" onClick={() => setReload((n) => n + 1)} disabled={!ready || loading} title="Refresh" aria-label="Refresh">
            <ReloadIcon />
          </button>
        </div>
      </div>

      <div className="compare-pickers">
        <CompareResourcePicker side="A" value={a} clusters={clusters} defaultCluster={a?.cluster || cluster} result={result?.a} onChange={setA} />
        <button className="ap-icon-btn compare-swap" type="button" onClick={swap} disabled={!a && !b} title="Swap A and B" aria-label="Swap A and B">
          <ShuffleIcon />
        </button>
        <CompareResourcePicker side="B" value={b} clusters={clusters} defaultCluster={b?.cluster || a?.cluster || cluster} result={result?.b} onChange={setB} />
      </div>

      {!ready && (
        <div className="empty-state compare-empty">
          <div className="empty-state-title">Pick two resources to compare</div>
          <div className="empty-state-subtitle">
            Any two objects, the same object in another cluster or namespace, or an object against its last-applied
            configuration or Helm manifest. Start from an inspector with Compare, or select two rows in a list.
          </div>
        </div>
      )}

      {ready && error && (
        <div className="compare-banner compare-banner--error" role="alert">
          <span>{error}</span>
          <button className="ap-btn ap-btn--sm" onClick={() => setReload((n) => n + 1)}>Retry</button>
        </div>
      )}

      {ready && loading && !result && <div className="compare-loading">Comparing…</div>}

      {result && !sideError && (
        <>
          {result.identical ? (
            <div className="compare-banner compare-banner--ok">
              <CheckCircledIcon />
              <span>Identical after normalization.</span>
            </div>
          ) : (
            <div className="compare-summary" aria-label="Summary of differences">
              {result.summary.map((ins, i) => (
                <button key={`${ins.title}-${i}`} type="button" className={`compare-insight compare-insight--${ins.category}`} onClick={() => reveal(ins)} title={ins.paths?.join('\n')}>
                  <span className="compare-insight-head">
                    <span className="ap-badge ap-badge--sm compare-insight-cat">{CATEGORY_LABELS[ins.category] || ins.category}</span>
                    <span className="compare-insight-title">{ins.title}</span>
                  </span>
                  {(ins.a || ins.b) && (
                    <span className="compare-insight-values">
                      <span className="compare-insight-a ap-truncate" title={ins.a}>{ins.a || 'not set'}</span>
                      <ArrowRightIcon className="compare-insight-arrow" />
                      <span className="compare-insight-b ap-truncate" title={ins.b}>{ins.b || 'not set'}</span>
                    </span>
                  )}
                </button>
              ))}
            </div>
          )}
          {result.notes && result.notes.length > 0 && <div className="compare-notes">{result.notes.join(' ')}</div>}
          <div className="compare-diff-header">
            <span className="compare-diff-label"><span className="compare-side-tag compare-side-tag--a">A</span>{result.a.label}</span>
            {sideBySide && <span className="compare-diff-label"><span className="compare-side-tag compare-side-tag--b">B</span>{result.b.label}</span>}
          </div>
          <div className={`compare-diff${loading ? ' is-loading' : ''}`}>
            <DiffEditor
              original={result.a.yaml}
              modified={result.b.yaml}
              language="yaml"
              theme={KANIVET_MONACO_THEME}
              beforeMount={installKanivetMonacoTheme}
              onMount={(ed: any) => { diffRef.current = ed; }}
              options={{
                renderSideBySide: sideBySide,
                readOnly: true,
                originalEditable: false,
                minimap: { enabled: false },
                scrollBeyondLastLine: false,
                automaticLayout: true,
                fontSize: 12,
                fontFamily: "ui-monospace, 'SF Mono', Menlo, Monaco, 'Cascadia Code', Consolas, 'Liberation Mono', monospace",
                renderOverviewRuler: true,
                hideUnchangedRegions: { enabled: changedOnly, contextLineCount: 3, minimumLineCount: 4 },
              } as any}
            />
          </div>
        </>
      )}
    </div>
  );
};

export default ComparePage;
