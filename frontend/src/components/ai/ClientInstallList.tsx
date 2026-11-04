import React, { useCallback, useEffect, useState } from 'react';
import { aiApi, type InstallMode, type McpClientStatus, type McpInstallPlan } from '../../services/api/ai';
import { openExternal } from '../cloud/LoginProgress';
import { clientStateLabel, clientStateTone } from './aiUtils';

interface Props {
  /** Changes when the token or port changes, so the list re-checks entries. */
  stamp: string;
}

type Pending = { id: string; action: 'install' | 'uninstall'; mode: InstallMode; plan: McpInstallPlan | null; error?: string };

/** One-click "Add to <client>" rows: detect, install, update, remove. */
const ClientInstallList: React.FC<Props> = ({ stamp }) => {
  const [clients, setClients] = useState<McpClientStatus[] | null>(null);
  const [mode, setMode] = useState<InstallMode>('readonly');
  const [pending, setPending] = useState<Pending | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [messages, setMessages] = useState<Record<string, { tone: 'ok' | 'error'; text: string }>>({});
  const [loadError, setLoadError] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      const r = await aiApi.clients();
      setClients(r.clients);
      setLoadError(null);
    } catch (e: any) {
      setLoadError(e?.message || 'Could not read agent configs');
    }
  }, []);

  useEffect(() => { void load(); }, [load, stamp]);

  const ask = async (id: string, action: 'install' | 'uninstall', m: InstallMode) => {
    setPending({ id, action, mode: m, plan: null });
    try {
      const plan = await aiApi.installPlan(id, action, m);
      setPending({ id, action, mode: m, plan });
    } catch (e: any) {
      setPending({ id, action, mode: m, plan: null, error: e?.response?.data?.error || e?.message });
    }
  };

  const run = async (p: Pending) => {
    setBusy(p.id);
    setPending(null);
    try {
      const r = p.action === 'install' ? await aiApi.installClient(p.id, p.mode) : await aiApi.uninstallClient(p.id);
      setClients((cs) => cs?.map((c) => (c.id === r.client.id ? r.client : c)) ?? cs);
      const label = r.client.label;
      setMessages((m) => ({
        ...m,
        [p.id]: r.error
          ? { tone: 'error', text: r.error }
          : { tone: 'ok', text: p.action === 'install' ? (r.client.restartHint ? `Added. Restart ${label} to apply.` : 'Added.') : (r.client.restartHint ? `Removed. Restart ${label} to apply.` : 'Removed.') },
      }));
    } catch (e: any) {
      setMessages((m) => ({ ...m, [p.id]: { tone: 'error', text: e?.response?.data?.error || e?.message || 'Failed' } }));
    } finally {
      setBusy(null);
    }
  };

  const updateAll = async () => {
    setBusy('*');
    try {
      const r = await aiApi.updateAllClients();
      setClients(r.clients);
      if (r.error) setLoadError(r.error);
    } finally {
      setBusy(null);
    }
  };

  const outdated = clients?.filter((c) => c.state === 'outdated') ?? [];
  const visible = clients?.filter((c) => c.state !== 'not_detected') ?? [];
  const missing = clients?.filter((c) => c.state === 'not_detected') ?? [];

  return (
    <div className="ap-card ai-section ai-clients">
      <div className="ap-card-row ai-clients-bar">
        <span className="ai-muted ai-small">Install with</span>
        <div className="ap-segmented ap-segmented--sm">
          <button type="button" aria-pressed={mode === 'readonly'} onClick={() => setMode('readonly')}>Read-only</button>
          <button type="button" aria-pressed={mode === 'full'} onClick={() => setMode('full')}>Full</button>
        </div>
        {outdated.length > 0 && (
          <button type="button" className="ap-btn ap-btn--sm ap-btn--primary ai-clients-update-all" disabled={busy !== null} onClick={() => void updateAll()}>
            Update all ({outdated.length})
          </button>
        )}
      </div>
      {loadError && <div className="ai-banner is-error">{loadError}</div>}
      {!clients && !loadError && <div className="ai-empty">Looking for agents…</div>}
      {visible.map((c) => {
        const msg = messages[c.id];
        const isPending = pending?.id === c.id;
        const installed = c.state === 'installed' || c.state === 'outdated';
        return (
          <div key={c.id} className="ai-client">
            <div className="ap-card-row ai-client-row">
              <span className="ai-row-label">
                <span className={`ap-dot ap-dot--${clientStateTone(c.state)}`} />
                {c.label}
                <span className={`ap-badge ap-badge--sm ap-badge--${clientStateTone(c.state)}`}>{clientStateLabel(c.state, c.mode)}</span>
              </span>
              <span className="ai-row-right">
                {c.state === 'blocked' ? null : c.canInstall ? (
                  <>
                    {c.state === 'outdated' && (
                      <button type="button" className="ap-btn ap-btn--sm ap-btn--primary" disabled={busy !== null} onClick={() => void ask(c.id, 'install', c.mode || mode)}>Update</button>
                    )}
                    {!installed && (
                      <button type="button" className="ap-btn ap-btn--sm ap-btn--primary" disabled={busy !== null} onClick={() => void ask(c.id, 'install', mode)}>
                        {busy === c.id ? 'Adding…' : `Add to ${c.label}`}
                      </button>
                    )}
                    {c.state === 'installed' && c.mode !== mode && (
                      <button type="button" className="ap-btn ap-btn--sm" disabled={busy !== null} onClick={() => void ask(c.id, 'install', mode)}>
                        Switch to {mode === 'full' ? 'full' : 'read-only'}
                      </button>
                    )}
                  </>
                ) : c.deeplink && !installed ? (
                  <button type="button" className="ap-btn ap-btn--sm ap-btn--primary" onClick={() => openExternal(c.deeplink!)}>Open in {c.label}</button>
                ) : null}
                {installed && (
                  <button type="button" className="ap-btn ap-btn--sm ap-btn--ghost" disabled={busy !== null} onClick={() => void ask(c.id, 'uninstall', c.mode || mode)}>Remove</button>
                )}
              </span>
            </div>
            {(c.detail || c.state === 'blocked') && (
              <div className={`ai-client-detail ai-small ${c.state === 'blocked' ? 'is-blocked' : 'ai-muted'}`}>
                {c.state === 'blocked'
                  ? `${c.detail || 'Blocked by policy.'} Your organization's policy decides which MCP servers ${c.label} may use; ask your admin to allow "kanivet", or use another client.`
                  : c.detail}
              </div>
            )}
            {isPending && (
              <div className="ai-confirm" role="dialog" aria-label={`Confirm ${pending.action}`}>
                {pending.error && <div className="ai-banner is-error">{pending.error}</div>}
                {!pending.plan && !pending.error && <div className="ai-muted ai-small">Checking…</div>}
                {pending.plan && (
                  <>
                    <div className="ai-small">
                      {pending.action === 'install' ? `This will ${installed ? 'update' : 'add'} the "kanivet" server (${pending.mode === 'full' ? 'full' : 'read-only'} endpoint):` : 'This will remove the "kanivet" server:'}
                    </div>
                    {pending.plan.commands?.map((cmd) => <pre key={cmd} className="ai-code ai-confirm-code">{cmd}</pre>)}
                    {pending.plan.files?.map((f) => (
                      <div key={f} className="ai-small">Edits <code className="ai-mono">{f}</code>{pending.plan?.backup ? ' (backed up first)' : ''}</div>
                    ))}
                    {pending.plan.note && <div className="ai-muted ai-small">{pending.plan.note}</div>}
                  </>
                )}
                <div className="ai-confirm-actions">
                  <button type="button" className="ap-btn ap-btn--sm ap-btn--ghost" onClick={() => setPending(null)}>Cancel</button>
                  <button type="button" className={`ap-btn ap-btn--sm ${pending.action === 'uninstall' ? 'ap-btn--danger' : 'ap-btn--primary'}`} disabled={!pending.plan} onClick={() => void run(pending)}>
                    {pending.action === 'install' ? 'Confirm' : 'Remove'}
                  </button>
                </div>
              </div>
            )}
            {msg && <div className={`ai-client-detail ai-small ${msg.tone === 'error' ? 'is-blocked' : 'ai-muted'}`}>{msg.text}</div>}
          </div>
        );
      })}
      {missing.length > 0 && (
        <div className="ap-card-row ai-muted ai-small">Not found on this machine: {missing.map((c) => c.label).join(', ')}.</div>
      )}
    </div>
  );
};

export default ClientInstallList;
