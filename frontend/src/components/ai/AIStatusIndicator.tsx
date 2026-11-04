import React, { useEffect } from 'react';
import { useAIStore } from '../../store/aiStore';
import { prettyClient } from './aiUtils';
import { SparkleIcon } from './AIIcons';
import './AI.css';

/** Shell indicator: MCP on/off, pulses while an agent is calling tools, and a
 * shortcut back to a running investigation. */
const AIStatusIndicator: React.FC = () => {
  const { status, refreshStatus, openSettings, order, investigations, select } = useAIStore();
  const running = order.map((id) => investigations[id]).find((i) => i?.status === 'running');
  const live = !!status?.lastCallAt && Date.now() - Date.parse(status.lastCallAt) < 20000;

  useEffect(() => {
    void refreshStatus();
    const t = setInterval(() => void refreshStatus(), live || running ? 4000 : 12000);
    return () => clearInterval(t);
  }, [refreshStatus, live, running]);

  const state = !status ? 'unknown' : !status.enabled ? 'off' : live ? 'live' : 'on';
  const title =
    state === 'live'
      ? `MCP: ${status!.activeClients.map(prettyClient).join(', ') || 'agent'} is using Kanivet`
      : state === 'on'
        ? 'MCP server on: agents can connect (click for setup)'
        : state === 'off'
          ? 'MCP server off'
          : 'MCP server status unknown';

  return (
    <>
      {running && (
        <button type="button" className="ai-indicator is-running" onClick={() => select(running.id)} title="Investigation running">
          <span className="ai-spinner" aria-hidden="true" />
          <span className="ai-indicator-label">Investigating</span>
        </button>
      )}
      <button type="button" className={`ai-indicator is-${state}`} onClick={openSettings} title={title} aria-label={title}>
        <SparkleIcon size={13} />
        <span className="ai-indicator-label">MCP</span>
        <span className={`ai-indicator-dot${live ? ' ai-pulse' : ''}`} aria-hidden="true" />
      </button>
    </>
  );
};

export default AIStatusIndicator;
