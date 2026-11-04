import React, { Suspense, useState } from 'react';
import { CheckIcon, Cross2Icon } from '@radix-ui/react-icons';
import { DiffEditor } from '../MonacoEditor';
import { installKanivetMonacoTheme, KANIVET_MONACO_THEME } from '../../utils/monacoTheme';
import type { Proposal } from '../../services/api/aiChat';
import { useAIChatStore } from '../../store/aiChatStore';
import { Ref } from './ChatParts';

export const diffOptions = {
  renderSideBySide: false,
  readOnly: true,
  minimap: { enabled: false },
  scrollBeyondLastLine: false,
  automaticLayout: true,
  fontSize: 11.5,
  lineNumbers: 'off' as const,
  folding: false,
  glyphMargin: false,
  renderOverviewRuler: false,
  hideUnchangedRegions: { enabled: true, contextLineCount: 2, minimumLineCount: 3, revealLineCount: 5 },
  fontFamily: "ui-monospace, 'SF Mono', Menlo, Monaco, 'Cascadia Code', Consolas, 'Liberation Mono', monospace",
};

export const YamlDiff: React.FC<{ original: string; modified: string; language?: string; height?: number }> = ({ original, modified, language = 'yaml', height = 220 }) => (
  <div className="ai-diff" style={{ height }}>
    <Suspense fallback={<div className="ai-empty"><span className="ai-spinner" /> Loading diff…</div>}>
      <DiffEditor keepCurrentOriginalModel keepCurrentModifiedModel original={original} modified={modified} language={language} theme={KANIVET_MONACO_THEME} beforeMount={installKanivetMonacoTheme} options={diffOptions} />
    </Suspense>
  </div>
);

const STATUS: Record<Proposal['status'], { label: string; tone: string }> = {
  proposed: { label: 'Proposed', tone: 'info' },
  'dry-run-ok': { label: 'Dry-run passed', tone: 'success' },
  'dry-run-failed': { label: 'Dry-run failed', tone: 'danger' },
  applied: { label: 'Applied', tone: 'success' },
  'apply-failed': { label: 'Apply failed', tone: 'danger' },
  rejected: { label: 'Rejected', tone: 'muted' },
};

/** Approval card for a live-cluster patch the agent proposed. Kanivet runs
 * the dry-run and the apply itself, only on these clicks. */
const ProposalCard: React.FC<{ msgId: string; p: Proposal; cluster: string }> = ({ msgId, p, cluster }) => {
  const action = useAIChatStore((s) => s.proposalAction);
  const [busy, setBusy] = useState<string | null>(null);
  const [confirm, setConfirm] = useState(false);
  const run = async (a: 'dry-run' | 'apply' | 'reject') => {
    setBusy(a);
    await action(msgId, p, a);
    setBusy(null);
    setConfirm(false);
  };
  const st = STATUS[p.status] || STATUS.proposed;
  const showDiff = p.before !== undefined && (p.after || p.status === 'dry-run-failed');
  return (
    <div className={`ai-approval is-${p.status}`}>
      <div className="ai-approval-head">
        <span className="ai-approval-title">Change to the live cluster</span>
        <span className={`ap-badge ap-badge--${st.tone}`}>{st.label}</span>
      </div>
      <div className="ai-approval-target">
        <Ref r={{ kind: p.kind, apiVersion: p.apiVersion, namespace: p.namespace, name: p.name }} cluster={cluster} chip />
        {p.summary && <span className="ai-approval-summary">{p.summary}</span>}
      </div>
      {showDiff && p.after ? (
        <YamlDiff original={p.before || ''} modified={p.after} />
      ) : (
        <pre className="ai-code ai-code--sm">{`${p.patchType || 'merge'} patch:\n${JSON.stringify(p.patch, null, 2)}`}</pre>
      )}
      {p.error && <div className="ai-banner is-error">{p.error}</div>}
      <div className="ai-approval-actions">
        {(p.status === 'proposed' || p.status === 'dry-run-failed') && (
          <button type="button" className="ap-btn ap-btn--sm" disabled={!!busy} onClick={() => run('dry-run')}>
            {busy === 'dry-run' ? <span className="ai-spinner" /> : null} {p.status === 'dry-run-failed' ? 'Retry dry-run' : 'Dry-run & show diff'}
          </button>
        )}
        {p.status === 'dry-run-ok' && !confirm && (
          <button type="button" className="ap-btn ap-btn--sm ap-btn--primary" disabled={!!busy} onClick={() => setConfirm(true)}>
            <CheckIcon /> Apply to {cluster}
          </button>
        )}
        {confirm && (
          <>
            <span className="ai-small ai-muted">Patch {p.kind} {p.name} now?</span>
            <button type="button" className="ap-btn ap-btn--sm ap-btn--danger" disabled={!!busy} onClick={() => run('apply')}>
              {busy === 'apply' ? <span className="ai-spinner" /> : null} Confirm apply
            </button>
            <button type="button" className="ap-btn ap-btn--sm ap-btn--ghost" onClick={() => setConfirm(false)}>Back</button>
          </>
        )}
        {(p.status === 'proposed' || p.status === 'dry-run-ok' || p.status === 'dry-run-failed') && !confirm && (
          <button type="button" className="ap-btn ap-btn--sm ap-btn--ghost" disabled={!!busy} onClick={() => run('reject')}><Cross2Icon /> Reject</button>
        )}
      </div>
    </div>
  );
};

export default ProposalCard;
