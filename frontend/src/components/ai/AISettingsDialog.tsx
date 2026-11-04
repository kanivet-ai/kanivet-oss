import React, { useEffect, useMemo, useState } from 'react';
import * as RDialog from '@radix-ui/react-dialog';
import { groupTools, featureLabel } from './aiUtils';
import { Cross2Icon, EyeOpenIcon, EyeClosedIcon, ReloadIcon, CheckIcon, CopyIcon } from '@radix-ui/react-icons';
import { useAIStore } from '../../store/aiStore';
import { aiApi, McpCall } from '../../services/api/ai';
import { maskToken, prettyClient, timeAgo } from './aiUtils';
import { SparkleIcon } from './AIIcons';
import './AI.css';

const AGENT_ORDER = ['claude-code', 'codex', 'cursor', 'vscode', 'claude-desktop'];

export const CopyButton: React.FC<{ text: string; label?: string; className?: string }> = ({ text, label, className = '' }) => {
  const [done, setDone] = useState(false);
  return (
    <button
      type="button"
      className={`ap-btn ap-btn--sm ai-copy ${className}`}
      onClick={() => {
        void navigator.clipboard.writeText(text).then(() => {
          setDone(true);
          setTimeout(() => setDone(false), 1600);
        });
      }}
      title="Copy to clipboard"
    >
      {done ? <CheckIcon /> : <CopyIcon />}
      {label && <span>{done ? 'Copied' : label}</span>}
    </button>
  );
};

const Toggle: React.FC<{ on: boolean; onChange: (v: boolean) => void; label: string; disabled?: boolean }> = ({ on, onChange, label, disabled }) => (
  <button
    type="button"
    role="switch"
    aria-checked={on}
    aria-label={label}
    disabled={disabled}
    className={`ap-toggle${on ? ' is-on' : ''}`}
    onClick={() => onChange(!on)}
  />
);

export const CallLog: React.FC<{ calls: McpCall[]; compact?: boolean }> = ({ calls, compact }) => {
  if (calls.length === 0) {
    return (
      <div className="ai-empty">
        No tool calls yet. Connect an agent with one of the commands above and ask it about your cluster.
      </div>
    );
  }
  return (
    <div className={`ai-calllog${compact ? ' is-compact' : ''}`} role="log" aria-live="polite">
      {calls.map((c, i) => (
        <div key={`${c.time}-${i}`} className={`ai-call${c.ok ? '' : ' is-error'}`}>
          <span className={`ap-dot ap-dot--${c.ok ? (c.write ? 'warning' : 'success') : 'danger'}`} aria-hidden="true" />
          <span className="ai-call-agent" title={c.client}>{prettyClient(c.client)}</span>
          <span className="ai-call-tool">{c.tool}</span>
          <span className="ai-call-args ap-truncate" title={c.error || c.args}>{c.error || c.args}</span>
          <span className="ai-call-meta">{c.durationMs}ms · ~{c.estTokens.toLocaleString()} tok</span>
          <span className="ai-call-time">{timeAgo(c.time)}</span>
        </div>
      ))}
    </div>
  );
};

const AISettingsDialog: React.FC = () => {
  const { settingsOpen, closeSettings, status, statusError, refreshStatus, setStatus, agents, loadAgents } = useAIStore();
  const [calls, setCalls] = useState<McpCall[]>([]);
  const [reveal, setReveal] = useState(false);
  const [agentTab, setAgentTab] = useState('claude-code');
  const [readOnly, setReadOnly] = useState(true);
  const [busy, setBusy] = useState(false);
  const [showTools, setShowTools] = useState(false);

  useEffect(() => {
    if (!settingsOpen) return;
    void refreshStatus();
    void loadAgents();
    let alive = true;
    const tick = async () => {
      try {
        const c = await aiApi.calls();
        if (alive) setCalls(c);
      } catch {
        /* backend down: keep last */
      }
    };
    void tick();
    const t = setInterval(tick, 2000);
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, [settingsOpen, refreshStatus, loadAgents]);

  const recipes = useMemo(() => {
    const id = readOnly ? `${agentTab}-readonly` : agentTab;
    return status?.setup.find((r) => r.id === id);
  }, [status, agentTab, readOnly]);

  const labels = useMemo(() => {
    const m: Record<string, string> = {};
    status?.setup.forEach((r) => (m[r.id] = r.label));
    return m;
  }, [status]);

  const update = async (patch: { enabled?: boolean; allowWrites?: boolean; allowExec?: boolean }) => {
    setBusy(true);
    try {
      setStatus(await aiApi.updateSettings(patch));
    } finally {
      setBusy(false);
    }
  };

  const rotate = async () => {
    if (!window.confirm('Rotate the MCP token? Agents configured with the old token stop working until you update them.')) return;
    setBusy(true);
    try {
      setStatus(await aiApi.rotateToken());
      setReveal(true);
    } finally {
      setBusy(false);
    }
  };

  const readTools = status?.tools.filter((t) => !t.write) ?? [];
  const writeTools = status?.tools.filter((t) => t.write) ?? [];
  const live = status?.lastCallAt && Date.now() - Date.parse(status.lastCallAt) < 30000;

  return (
    <RDialog.Root open={settingsOpen} onOpenChange={(o) => !o && closeSettings()}>
      <RDialog.Portal>
        <RDialog.Overlay className="ap-overlay ap-dialog-overlay">
          <RDialog.Content className="ap-sheet ai-settings" aria-describedby={undefined}>
            <div className="ai-settings-header">
              <span className="ai-tile" aria-hidden="true"><SparkleIcon /></span>
              <div className="ai-settings-titles">
                <RDialog.Title className="ap-sheet-title">AI &amp; MCP</RDialog.Title>
                <div className="ai-settings-sub">
                  Let Claude Code, Codex, Cursor and other agents read your clusters through Kanivet.
                </div>
              </div>
              <RDialog.Close className="ap-icon-btn" aria-label="Close"><Cross2Icon /></RDialog.Close>
            </div>

            <div className="ai-settings-body">
              {statusError && !status && (
                <div className="ai-banner is-error">The MCP server is not responding: {statusError}</div>
              )}
              {!status && !statusError && <div className="ai-empty">Loading…</div>}
              {status && (
                <>
                  <div className="ap-card ai-section">
                    <div className="ap-card-row">
                      <span className="ai-row-label">
                        <span className={`ap-dot ap-dot--${status.enabled ? (live ? 'success' : 'success') : 'danger'}${live ? ' ai-pulse' : ''}`} />
                        MCP server
                      </span>
                      <span className="ai-row-right">
                        <span className="ai-muted">
                          {status.enabled ? (live ? `Active · ${status.activeClients.map(prettyClient).join(', ')}` : 'On') : 'Off'}
                        </span>
                        <Toggle label="MCP server" on={status.enabled} disabled={busy} onChange={(v) => update({ enabled: v })} />
                      </span>
                    </div>
                    <div className="ap-card-row">
                      <span className="ai-row-label">
                        Allow write tools on <code>/mcp</code>
                        <span className="ai-muted ai-small">restart, scale, delete pod, apply, rollback, sync, cordon/drain</span>
                      </span>
                      <Toggle label="Allow write tools" on={status.allowWrites} disabled={busy || !status.enabled} onChange={(v) => update({ allowWrites: v })} />
                    </div>
                    <div className="ap-card-row">
                      <span className="ai-row-label">
                        Allow agents to run commands in pods
                        <span className="ai-muted ai-small">
                          Adds <code>exec_pod</code> on <code>/mcp</code> only. Its output is raw and can contain secrets.
                        </span>
                      </span>
                      <Toggle
                        label="Allow agents to run commands in pods"
                        on={status.allowExec}
                        disabled={busy || !status.enabled}
                        onChange={(v) => {
                          if (v && !window.confirm('Let agents run commands inside your containers? Commands run with your cluster credentials and their output (which may include secrets) goes to the agent unfiltered.')) return;
                          void update({ allowExec: v });
                        }}
                      />
                    </div>
                    {status.allowExec && (
                      <div className="ai-banner is-warning">
                        Agents connected to <code>/mcp</code> can run commands in any pod you can exec into. Turn this off when you are done.
                      </div>
                    )}
                  </div>

                  <div className="ap-group-label">Endpoints</div>
                  <div className="ap-card ai-section">
                    <div className="ap-card-row">
                      <span className="ai-row-label">Read-only <span className="ap-badge ap-badge--sm ap-badge--success">recommended</span></span>
                      <span className="ai-row-right"><code className="ai-mono ap-truncate">{status.readOnlyUrl}</code><CopyButton text={status.readOnlyUrl} /></span>
                    </div>
                    <div className="ap-card-row">
                      <span className="ai-row-label">Full (read + write)</span>
                      <span className="ai-row-right"><code className="ai-mono ap-truncate">{status.fullUrl}</code><CopyButton text={status.fullUrl} /></span>
                    </div>
                    <div className="ap-card-row">
                      <span className="ai-row-label">Bearer token</span>
                      <span className="ai-row-right">
                        <code className="ai-mono ap-truncate">{reveal ? status.token : maskToken(status.token)}</code>
                        <button type="button" className="ap-icon-btn" onClick={() => setReveal(!reveal)} aria-label={reveal ? 'Hide token' : 'Show token'} title={reveal ? 'Hide' : 'Show'}>
                          {reveal ? <EyeClosedIcon /> : <EyeOpenIcon />}
                        </button>
                        <CopyButton text={status.token} />
                        <button type="button" className="ap-icon-btn" onClick={rotate} disabled={busy} aria-label="Rotate token" title="Rotate token"><ReloadIcon /></button>
                      </span>
                    </div>
                  </div>
                  <div className="ai-footnote">
                    Loopback only. The token lives in <code>{status.tokenFile}</code>. Tools called without a cluster use the one open in Kanivet{status.activeCluster ? ` (${status.activeCluster})` : ''}.
                  </div>

                  <div className="ap-group-label">Connect an agent</div>
                  <div className="ap-card ai-section ai-setup">
                    <div className="ai-setup-bar">
                      <div className="ap-segmented" role="tablist">
                        {AGENT_ORDER.map((id) => (
                          <button key={id} type="button" role="tab" aria-selected={agentTab === id} onClick={() => setAgentTab(id)}>
                            {labels[id] || id}
                          </button>
                        ))}
                      </div>
                      <div className="ap-segmented ap-segmented--sm">
                        <button type="button" aria-pressed={readOnly} onClick={() => setReadOnly(true)}>Read-only</button>
                        <button type="button" aria-pressed={!readOnly} onClick={() => setReadOnly(false)}>Full</button>
                      </div>
                    </div>
                    {recipes && (
                      <div className="ai-snippet">
                        <div className="ai-snippet-head">
                          <span className="ai-muted ai-small">
                            {recipes.kind === 'command' ? 'Run in a terminal' : `Add to ${recipes.where}`}
                          </span>
                          <CopyButton text={recipes.snippet} label="Copy" />
                        </div>
                        <pre className="ai-code">{recipes.snippet}</pre>
                      </div>
                    )}
                  </div>

                  <div className="ap-group-label">Investigations</div>
                  <div className="ap-card ai-section">
                    {(agents ?? []).map((a) => (
                      <div key={a.id} className="ap-card-row">
                        <span className="ai-row-label">
                          <span className={`ap-dot ap-dot--${a.available ? 'success' : 'warning'}`} />
                          {a.name}
                          {a.version && <span className="ai-muted ai-small">{a.version}</span>}
                        </span>
                        {a.available ? (
                          <span className="ai-muted ai-small ap-truncate" title={a.path}>{a.path}</span>
                        ) : (
                          <span className="ai-row-right"><code className="ai-mono">{a.install}</code><CopyButton text={a.install} /></span>
                        )}
                      </div>
                    ))}
                    <div className="ap-card-row ai-muted ai-small">
                      Investigate (in the inspector and on incidents) runs an installed CLI headless against the read-only endpoint, using its own login. No API keys are stored in Kanivet.
                    </div>
                  </div>

                  <div className="ap-group-label ai-label-row">
                    <span>Live agent activity</span>
                    <span className="ai-muted ai-small">{calls.length} recent calls</span>
                  </div>
                  <div className="ap-card ai-section">
                    <CallLog calls={calls} />
                  </div>

                  <div className="ap-group-label ai-label-row">
                    <span>Tools ({status.tools.length})</span>
                    <button type="button" className="ap-btn ap-btn--sm ap-btn--ghost" onClick={() => setShowTools(!showTools)}>
                      {showTools ? 'Hide' : 'Show'}
                    </button>
                  </div>
                  {showTools && (
                    <div className="ap-card ai-section">
                      {groupTools([...readTools, ...writeTools]).flatMap(([feature, tools]) => [
                        <div key={`f-${feature}`} className="ai-tool-group">{featureLabel(feature)}</div>,
                        ...tools.map((t) => (
                        <div key={t.name} className="ap-card-row ai-tool-row">
                          <code className="ai-mono">{t.name}</code>
                          <span className="ai-muted ai-small ai-tool-desc">{t.description.replace(/^DESTRUCTIVE: /, '')}</span>
                          {t.write && (
                            <span className={`ap-badge ap-badge--sm ${t.destructive ? 'ap-badge--danger' : 'ap-badge--warning'}`}>
                              {t.destructive ? 'destructive' : 'write'}
                            </span>
                          )}
                        </div>
                        )),
                      ])}
                    </div>
                  )}
                </>
              )}
            </div>
          </RDialog.Content>
        </RDialog.Overlay>
      </RDialog.Portal>
    </RDialog.Root>
  );
};

export default AISettingsDialog;
