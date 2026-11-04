import React, { useCallback, useEffect, useState } from 'react';
import { ArrowLeftIcon, ReloadIcon } from '@radix-ui/react-icons';
import { aiChatApi, type Chat, type FileDiff } from '../../services/api/aiChat';
import { YamlDiff } from './ProposalCard';

const langFor = (p: string) => {
  const ext = p.split('.').pop()?.toLowerCase() || '';
  if (ext === 'yaml' || ext === 'yml') return 'yaml';
  if (ext === 'tf' || ext === 'hcl') return 'hcl';
  if (ext === 'json') return 'json';
  if (ext === 'tpl') return 'yaml';
  return 'plaintext';
};

async function openUrl(url: string) {
  const ai = (window as any).electronAPI?.ai;
  if (ai?.openEditorUrl) await ai.openEditorUrl(url);
  else window.open(url, '_blank');
}

/** Review of a "fix in code" chat: the repo's git diff since the agent
 * started, per file, with Keep / Discard / Open in editor / Commit. */
const CodeReview: React.FC<{ chat: Chat; onBack: () => void }> = ({ chat, onBack }) => {
  const [files, setFiles] = useState<FileDiff[] | null>(null);
  const [sel, setSel] = useState(0);
  const [err, setErr] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [commitOpen, setCommitOpen] = useState(false);
  const w = chat.code!;
  const [branch, setBranch] = useState(w.branch?.startsWith('kanivet/') ? w.branch : `kanivet/fix-${chat.title.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '').slice(0, 40)}`);
  const [msg, setMsg] = useState(`fix: ${chat.title.replace(/^Fix:\s*/i, '')}`);

  const load = useCallback(async () => {
    setErr(null);
    try {
      const d = await aiChatApi.diff(chat.id);
      setFiles(d.files);
      setSel((s) => Math.min(s, Math.max(0, d.files.length - 1)));
    } catch (e: any) {
      setErr(e?.response?.data?.error || e?.message || 'Could not read the diff');
    }
  }, [chat.id]);

  useEffect(() => {
    void load();
  }, [load, chat.running]);

  const act = async (fn: () => Promise<string | void>, ok?: string) => {
    setBusy(true);
    setErr(null);
    try {
      const r = await fn();
      if (ok) setNote(typeof r === 'string' && r ? `${ok} ${r.slice(0, 10)}` : ok);
      await load();
    } catch (e: any) {
      setErr(e?.response?.data?.error || e?.message || 'Failed');
    }
    setBusy(false);
  };

  const f = files?.[sel];
  const own = (files || []).filter((x) => !x.preexisting);
  return (
    <div className="ai-review">
      <div className="ai-review-bar">
        <button type="button" className="ap-icon-btn" onClick={onBack} aria-label="Back to chat" title="Back to chat"><ArrowLeftIcon /></button>
        <div className="ai-review-titles">
          <div className="ai-review-title">Review changes</div>
          <div className="ai-small ai-muted ap-truncate" title={w.repo}>{w.repo.split(/[\\/]/).pop()} · {w.branch || 'detached'}{w.createdBranch ? ' (new branch)' : ''}</div>
        </div>
        <button type="button" className="ap-icon-btn" onClick={() => void load()} aria-label="Refresh diff" title="Refresh"><ReloadIcon /></button>
      </div>
      {err && <div className="ai-banner is-error">{err}</div>}
      {note && <div className="ai-banner is-ok">{note}</div>}
      {files === null ? (
        <div className="ai-empty"><span className="ai-spinner" /> Reading git diff…</div>
      ) : files.length === 0 ? (
        <div className="ai-empty">{chat.running ? 'The agent has not changed any files yet.' : 'No changes in the repository since the agent started.'}</div>
      ) : (
        <>
          <div className="ai-review-files" role="listbox" aria-label="Changed files">
            {files.map((x, i) => (
              <button key={x.path} type="button" role="option" aria-selected={i === sel} className={`ai-review-file${i === sel ? ' is-active' : ''}`} onClick={() => setSel(i)}>
                <span className={`ai-file-status is-${x.status}`}>{x.status === 'added' ? 'A' : x.status === 'deleted' ? 'D' : 'M'}</span>
                <span className="ap-truncate">{x.path}</span>
                {x.preexisting && <span className="ap-badge ap-badge--sm" title="Already changed before the agent started">yours</span>}
              </button>
            ))}
          </div>
          {f && (
            <div className="ai-review-diff">
              <div className="ai-review-file-actions">
                <span className="ai-mono ap-truncate">{f.path}</span>
                <span className="ai-grow" />
                <button type="button" className="ap-btn ap-btn--sm ap-btn--ghost" disabled={busy} onClick={() => act(() => aiChatApi.openEditor(chat.id, f.path).then((u) => (u ? openUrl(u) : undefined)))}>Open in editor</button>
                {!f.preexisting && <button type="button" className="ap-btn ap-btn--sm ap-btn--ghost" disabled={busy} onClick={() => act(() => aiChatApi.discard(chat.id, [f.path]).then(() => undefined), `Discarded ${f.path}`)}>Discard file</button>}
              </div>
              {f.binary ? <div className="ai-empty">Binary file</div> : <YamlDiff original={f.original} modified={f.modified} language={langFor(f.path)} height={300} />}
            </div>
          )}
          <div className="ai-review-actions">
            <button type="button" className="ap-btn ap-btn--sm ap-btn--primary" disabled={busy || chat.running} onClick={() => { setNote('Kept. The changes stay in your working tree; nothing was committed.'); }}>Keep</button>
            <button type="button" className="ap-btn ap-btn--sm" disabled={busy || chat.running || own.length === 0} onClick={() => act(() => aiChatApi.discard(chat.id).then(() => undefined), 'Discarded the agent\'s changes (restored with git).')}>Discard all</button>
            <button type="button" className="ap-btn ap-btn--sm ap-btn--ghost" disabled={busy} onClick={() => act(() => aiChatApi.openEditor(chat.id).then((u) => (u ? openUrl(u) : undefined)))}>Open repo in editor</button>
            <span className="ai-grow" />
            <button type="button" className="ap-btn ap-btn--sm" disabled={busy || chat.running || own.length === 0} onClick={() => setCommitOpen((o) => !o)}>Create branch & commit…</button>
          </div>
          {commitOpen && (
            <div className="ai-commit">
              <label className="ai-field"><span>Branch</span><input className="ap-input" value={branch} onChange={(e) => setBranch(e.target.value)} /></label>
              <label className="ai-field"><span>Message</span><input className="ap-input" value={msg} onChange={(e) => setMsg(e.target.value)} /></label>
              <div className="ai-small ai-muted">Commits the {own.length} file{own.length === 1 ? '' : 's'} the agent changed. Nothing is pushed.</div>
              <button type="button" className="ap-btn ap-btn--sm ap-btn--primary" disabled={busy || !branch.trim()} onClick={() => act(() => aiChatApi.commit(chat.id, branch.trim(), msg), 'Committed')}>Commit</button>
            </div>
          )}
        </>
      )}
    </div>
  );
};

export default CodeReview;
