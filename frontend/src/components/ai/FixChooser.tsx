import React, { useEffect, useState } from 'react';
import { PlusIcon } from '@radix-ui/react-icons';
import { aiChatApi, type CodeLocation, type CodeSuggestion, type SourceHints } from '../../services/api/aiChat';
import { useAIChatStore } from '../../store/aiChatStore';
import type { AskSeed } from './aiContext';
import { ChipView } from './ChatParts';

/** Asks the user for a folder (Electron dialog, or typed path in a browser). */
export async function pickFolder(): Promise<string | null> {
  const ai = (window as any).electronAPI?.ai;
  if (ai?.pickFolder) return ai.pickFolder();
  const p = window.prompt('Path of a local repository folder');
  return p?.trim() || null;
}

/** "How do you want to fix this?" Shown before a fix chat starts. */
const FixChooser: React.FC<{ seed: AskSeed }> = ({ seed }) => {
  const { startFix, cancelFix, busy, dirtyRepo, error } = useAIChatStore();
  const [mode, setMode] = useState<'code' | 'ask'>('code');
  const [locs, setLocs] = useState<CodeLocation[] | null>(null);
  const [sugs, setSugs] = useState<CodeSuggestion[]>([]);
  const [hints, setHints] = useState<SourceHints | null>(null);
  const [repo, setRepo] = useState('');
  const [newBranch, setNewBranch] = useState(true);
  const [locErr, setLocErr] = useState<string | null>(null);

  useEffect(() => {
    let off = false;
    (async () => {
      try {
        const all = await aiChatApi.locations();
        if (off) return;
        setLocs(all);
        const r = seed.resource;
        if (r && seed.cluster) {
          const s = await aiChatApi.suggest(r.cluster || seed.cluster, r.kind, r.namespace, r.name);
          if (off) return;
          setSugs(s.suggestions);
          setHints(s.hints);
          if (s.suggestions[0]) setRepo(s.suggestions[0].location.path);
          else if (all.length === 1) setRepo(all[0].path);
        } else if (all.length === 1) setRepo(all[0].path);
      } catch {
        if (!off) setLocs([]);
      }
    })();
    return () => {
      off = true;
    };
  }, [seed]);

  const add = async () => {
    const p = await pickFolder();
    if (!p) return;
    try {
      setLocErr(null);
      const all = await aiChatApi.addLocation(p);
      setLocs(all);
      setRepo(all.find((l) => l.path === p || p.endsWith(l.name))?.path || p);
      if (seed.resource && seed.cluster) {
        const s = await aiChatApi.suggest(seed.resource.cluster || seed.cluster, seed.resource.kind, seed.resource.namespace, seed.resource.name);
        setSugs(s.suggestions);
      }
    } catch (e: any) {
      setLocErr(e?.response?.data?.error || e?.message || 'Could not add the folder');
    }
  };

  const sugFor = (p: string) => sugs.find((s) => s.location.path === p);
  const ordered = [...(locs || [])].sort((a, b) => (sugFor(b.path)?.score || 0) - (sugFor(a.path)?.score || 0));

  return (
    <div className="ai-fix">
      <div className="ai-fix-title">{seed.title || 'Fix with AI'}</div>
      <div className="ai-fix-text">{seed.text}</div>
      {seed.chips && seed.chips.length > 0 && <div className="ai-chips">{seed.chips.map((c, i) => <ChipView key={i} c={c} />)}</div>}

      <div className="ap-segmented ai-fix-seg" role="tablist" aria-label="Where to fix">
        <button type="button" role="tab" data-active={mode === 'code'} className={mode === 'code' ? 'is-active' : undefined} onClick={() => setMode('code')}>Fix in code</button>
        <button type="button" role="tab" data-active={mode === 'ask'} className={mode === 'ask' ? 'is-active' : undefined} onClick={() => setMode('ask')}>Fix in cluster</button>
      </div>

      {mode === 'code' ? (
        <div className="ai-fix-body">
          <div className="ai-small ai-muted">
            Recommended for GitOps. The agent edits the manifest, Helm values, Kustomize or Terraform that defines this resource, inside the folder you pick only (no shell). You review the git diff before anything is kept.
          </div>
          {hints?.manager && (
            <div className="ai-small ai-muted">Managed by {hints.manager}{hints.repoUrls?.[0] ? ` from ${hints.repoUrls[0]}` : ''}{hints.paths?.[0] ? ` (${hints.paths[0]})` : ''}.</div>
          )}
          <div className="ai-label-row ap-group-label">
            <span>Code location</span>
            <button type="button" className="ap-btn ap-btn--sm ap-btn--ghost" onClick={add}><PlusIcon /> Add folder…</button>
          </div>
          {locs === null ? (
            <div className="ai-empty"><span className="ai-spinner" /> Loading code locations…</div>
          ) : locs.length === 0 ? (
            <div className="ai-empty">No code locations yet. Add the local folder of the repo that deploys this resource.</div>
          ) : (
            <div className="ap-card ai-loc-list" role="radiogroup" aria-label="Code location">
              {ordered.map((l) => {
                const s = sugFor(l.path);
                return (
                  <label key={l.path} className={`ai-loc${repo === l.path ? ' is-active' : ''}`}>
                    <input type="radio" name="ai-repo" checked={repo === l.path} onChange={() => setRepo(l.path)} />
                    <span className="ai-loc-main">
                      <span className="ai-loc-name">{l.name}{s && s.score >= 60 && <span className="ap-badge ap-badge--sm ap-badge--success">match</span>}</span>
                      <span className="ai-small ai-muted ap-truncate">{s ? s.reason : l.remotes?.[0] || l.path}</span>
                    </span>
                  </label>
                );
              })}
            </div>
          )}
          {locErr && <div className="ai-banner is-error">{locErr}</div>}
          <label className="ai-check">
            <input type="checkbox" checked={newBranch} onChange={(e) => setNewBranch(e.target.checked)} />
            <span>Work on a new branch <code className="ai-mono">kanivet/fix-…</code> (recommended)</span>
          </label>
          {dirtyRepo && dirtyRepo.repo === repo && (
            <div className="ai-banner is-warning">
              This repository has uncommitted changes ({dirtyRepo.files.slice(0, 4).join(', ')}{dirtyRepo.files.length > 4 ? ', …' : ''}). Use a new branch, or confirm working on top of them; Discard will leave your own changes alone.
              <div className="ai-banner-actions">
                <button type="button" className="ap-btn ap-btn--sm" onClick={() => startFix(seed, { mode: 'code', repo, newBranch: false, allowDirty: true })}>Continue on current branch</button>
              </div>
            </div>
          )}
        </div>
      ) : (
        <div className="ai-fix-body ai-small ai-muted">
          The agent stays read-only. It proposes a patch; Kanivet shows it as a diff against the live object, runs a server-side dry-run, and applies it only when you approve it in the chat.
        </div>
      )}
      {error && <div className="ai-banner is-error">{error}</div>}
      <div className="ai-fix-actions">
        <button type="button" className="ap-btn ap-btn--sm ap-btn--ghost" onClick={cancelFix}>Cancel</button>
        <button
          type="button"
          className="ap-btn ap-btn--sm ap-btn--primary"
          disabled={busy || (mode === 'code' && !repo)}
          onClick={() => startFix(seed, mode === 'code' ? { mode: 'code', repo, newBranch } : { mode: 'ask' })}
        >
          {busy ? <span className="ai-spinner" /> : null} {mode === 'code' ? 'Start code fix' : 'Propose a cluster fix'}
        </button>
      </div>
    </div>
  );
};

export default FixChooser;
