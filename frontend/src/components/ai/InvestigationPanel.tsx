import React, { useEffect, useMemo, useRef, useState } from 'react';
import { Cross2Icon, StopIcon, ReloadIcon, ChevronDownIcon, ChevronRightIcon } from '@radix-ui/react-icons';
import { useAIStore } from '../../store/aiStore';
import ResourceLink from '../ResourceLink';
import type { FindingResource, Investigation, InvestigationEvent } from '../../services/api/ai';
import {
  apiVersionFor,
  collectResources,
  formatElapsed,
  parseInline,
  parseMarkdown,
  refFromCode,
  targetLabel,
} from './aiUtils';
import { SparkleIcon, ToolIcon } from './AIIcons';
import { CopyButton } from './AISettingsDialog';
import './AI.css';

const Ref: React.FC<{ r: FindingResource; cluster: string; chip?: boolean }> = ({ r, cluster, chip }) => (
  <ResourceLink
    cluster={r.cluster || cluster}
    apiVersion={apiVersionFor(r)}
    kind={r.kind}
    name={r.name}
    namespace={r.namespace}
    className={chip ? 'ai-ref-chip' : 'ai-ref'}
  >
    <span className="ai-ref-kind">{r.kind}</span>
    {r.namespace && <span className="ai-ref-ns">{r.namespace}/</span>}
    <span className="ai-ref-name">{r.name}</span>
  </ResourceLink>
);

const Inline: React.FC<{ text: string; cluster: string }> = ({ text, cluster }) => (
  <>
    {parseInline(text).map((p, i) => {
      if (p.type === 'bold') return <strong key={i}>{p.text}</strong>;
      if (p.type === 'code') {
        const ref = refFromCode(p.text);
        return ref ? <Ref key={i} r={ref} cluster={cluster} /> : <code key={i} className="ai-inline-code">{p.text}</code>;
      }
      return <React.Fragment key={i}>{p.text}</React.Fragment>;
    })}
  </>
);

const Markdown: React.FC<{ md: string; cluster: string }> = ({ md, cluster }) => (
  <div className="ai-md">
    {parseMarkdown(md).map((b, i) => {
      switch (b.type) {
        case 'h':
          return <div key={i} className={`ai-md-h ai-md-h${Math.min(b.level, 3)}`}><Inline text={b.text} cluster={cluster} /></div>;
        case 'ul':
          return <ul key={i}>{b.items.map((it, j) => <li key={j}><Inline text={it} cluster={cluster} /></li>)}</ul>;
        case 'ol':
          return <ol key={i}>{b.items.map((it, j) => <li key={j}><Inline text={it} cluster={cluster} /></li>)}</ol>;
        case 'code':
          return <pre key={i} className="ai-code">{b.text}</pre>;
        default:
          return <p key={i}><Inline text={b.text} cluster={cluster} /></p>;
      }
    })}
  </div>
);

const sevClass = (s?: string) =>
  s === 'critical' ? 'ap-badge--danger' : s === 'warning' ? 'ap-badge--warning' : s === 'healthy' ? 'ap-badge--success' : 'ap-badge--info';

const StepRow: React.FC<{ e: InvestigationEvent; result?: InvestigationEvent; cluster: string }> = ({ e, result, cluster }) => {
  const [open, setOpen] = useState(false);
  if (e.type === 'tool') {
    const state = !result ? 'running' : result.ok === false ? 'error' : 'ok';
    return (
      <div className={`ai-step ai-step--tool is-${state}`}>
        <button type="button" className="ai-step-head" onClick={() => setOpen(!open)} aria-expanded={open}>
          <span className="ai-step-icon">{state === 'running' ? <span className="ai-spinner" /> : <ToolIcon />}</span>
          <span className="ai-step-tool">{e.tool}</span>
          <span className="ai-step-args ap-truncate">{e.input && e.input !== '{}' ? e.input : ''}</span>
          {(e.input || result?.text) && (open ? <ChevronDownIcon /> : <ChevronRightIcon />)}
        </button>
        {open && (
          <div className="ai-step-detail">
            {e.input && <pre className="ai-code ai-code--sm">{e.input}</pre>}
            {result?.text && <pre className="ai-code ai-code--sm">{result.text}</pre>}
          </div>
        )}
      </div>
    );
  }
  if (e.type === 'thinking') {
    return (
      <div className="ai-step ai-step--thinking">
        <button type="button" className="ai-step-head" onClick={() => setOpen(!open)} aria-expanded={open}>
          <span className="ai-step-icon">{open ? <ChevronDownIcon /> : <ChevronRightIcon />}</span>
          <span className="ai-muted">Thinking</span>
        </button>
        {open && <div className="ai-step-detail ai-muted">{e.text}</div>}
      </div>
    );
  }
  if (e.type === 'text') {
    return (
      <div className="ai-step ai-step--text">
        <Markdown md={e.text || ''} cluster={cluster} />
      </div>
    );
  }
  return <div className={`ai-step ai-step--status${e.type === 'error' ? ' is-error' : ''}`}>{e.text}</div>;
};

const Findings: React.FC<{ inv: Investigation }> = ({ inv }) => {
  const f = inv.findings;
  const cluster = inv.target.cluster;
  const resources = collectResources(inv);
  return (
    <div className="ai-findings">
      {f ? (
        <>
          <div className="ai-summary-card">
            <div className="ai-summary-top">
              {f.severity && <span className={`ap-badge ${sevClass(f.severity)}`}>{f.severity}</span>}
              {f.confidence && <span className="ap-badge">confidence: {f.confidence}</span>}
            </div>
            <div className="ai-summary-text"><Inline text={f.summary} cluster={cluster} /></div>
            {f.rootCause && (
              <div className="ai-rootcause">
                <div className="ai-label">Root cause</div>
                <Inline text={f.rootCause} cluster={cluster} />
              </div>
            )}
          </div>
          {f.findings && f.findings.length > 0 && (
            <>
              <div className="ap-group-label">Findings</div>
              <div className="ap-card ai-finding-list">
                {f.findings.map((fd, i) => (
                  <div key={i} className="ai-finding">
                    <div className="ai-finding-head">
                      <span className={`ap-dot ap-dot--${fd.severity === 'critical' ? 'danger' : fd.severity === 'warning' ? 'warning' : 'success'}`} />
                      <span className="ai-finding-title"><Inline text={fd.title} cluster={cluster} /></span>
                    </div>
                    {fd.detail && <div className="ai-finding-detail"><Inline text={fd.detail} cluster={cluster} /></div>}
                    {fd.resources && fd.resources.length > 0 && (
                      <div className="ai-chips">{fd.resources.map((r, j) => <Ref key={j} r={r} cluster={cluster} chip />)}</div>
                    )}
                  </div>
                ))}
              </div>
            </>
          )}
          {f.nextSteps && f.nextSteps.length > 0 && (
            <>
              <div className="ap-group-label">Next steps</div>
              <div className="ap-card ai-steps-card">
                <ol>{f.nextSteps.map((s, i) => <li key={i}><Inline text={s} cluster={cluster} /></li>)}</ol>
              </div>
            </>
          )}
          {resources.length > 0 && (
            <>
              <div className="ap-group-label">Resources</div>
              <div className="ai-chips ai-chips--pad">{resources.map((r, i) => <Ref key={i} r={r} cluster={cluster} chip />)}</div>
            </>
          )}
        </>
      ) : null}
      {inv.report && (
        <>
          <div className="ap-group-label ai-label-row">
            <span>Report</span>
            <CopyButton text={inv.report} />
          </div>
          <div className="ap-card ai-report"><Markdown md={inv.report} cluster={cluster} /></div>
        </>
      )}
    </div>
  );
};

const NoAgent: React.FC = () => {
  const { agents, pendingTarget, investigate, loadAgents, openSettings } = useAIStore();
  return (
    <div className="ai-noagent">
      <span className="ai-tile ai-tile--lg" aria-hidden="true"><SparkleIcon size={22} /></span>
      <div className="ai-noagent-title">Install an agent CLI to investigate</div>
      <div className="ai-muted">
        Kanivet runs Claude Code or Codex on your machine against its read-only MCP server, using the login you already have. No API key is needed.
      </div>
      <div className="ap-card ai-section ai-noagent-list">
        {(agents ?? []).map((a) => (
          <div key={a.id} className="ap-card-row">
            <span className="ai-row-label">{a.name}</span>
            <span className="ai-row-right"><code className="ai-mono">{a.install}</code><CopyButton text={a.install} /></span>
          </div>
        ))}
      </div>
      <div className="ai-noagent-actions">
        <button
          type="button"
          className="ap-btn ap-btn--primary"
          onClick={async () => {
            const list = await loadAgents();
            if (pendingTarget && list.some((a) => a.available)) void investigate(pendingTarget);
          }}
        >
          <ReloadIcon /> Check again
        </button>
        <button type="button" className="ap-btn" onClick={openSettings}>Connect another agent</button>
      </div>
    </div>
  );
};

const InvestigationPanel: React.FC = () => {
  const { panelOpen, closePanel, activeId, investigations, order, select, cancel, investigate, starting, startError, agents, pendingTarget } = useAIStore();
  const inv = activeId ? investigations[activeId] : undefined;
  const [now, setNow] = useState(Date.now());
  const bodyRef = useRef<HTMLDivElement>(null);
  const stick = useRef(true);

  useEffect(() => {
    if (inv?.status !== 'running') return;
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, [inv?.status]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && panelOpen && !document.querySelector('.ap-overlay')) closePanel();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [panelOpen, closePanel]);

  useEffect(() => {
    const el = bodyRef.current;
    if (el && stick.current) el.scrollTop = el.scrollHeight;
  }, [inv?.events.length, inv?.status]);

  const steps = useMemo(() => {
    if (!inv) return [];
    const out: { e: InvestigationEvent; result?: InvestigationEvent }[] = [];
    const open: { e: InvestigationEvent; result?: InvestigationEvent }[] = [];
    for (const e of inv.events) {
      if (e.type === 'tool_result') {
        const s = open.shift();
        if (s) s.result = e;
        continue;
      }
      const s = { e };
      if (e.type === 'tool') open.push(s);
      out.push(s);
    }
    // The final answer is shown as the findings view.
    if (inv.status !== 'running' && inv.report) {
      for (let i = out.length - 1; i >= 0; i--) {
        if (out[i].e.type === 'text') {
          out.splice(i, 1);
          break;
        }
      }
    }
    return out;
  }, [inv]);

  if (!panelOpen) return null;
  const noAgent = !inv && !starting && agents && !agents.some((a) => a.available);
  const elapsed = inv ? (inv.ended ? Date.parse(inv.ended) : now) - Date.parse(inv.started) : 0;

  return (
    <aside className="ai-panel" aria-label="AI investigation">
      <div className="ai-panel-header">
        <span className="ai-tile" aria-hidden="true"><SparkleIcon /></span>
        <div className="ai-panel-titles">
          <div className="ai-panel-title">Investigation</div>
          <div className="ai-panel-sub ap-truncate">
            {inv ? (
              <>
                {inv.target.name ? (
                  <Ref r={{ kind: inv.target.kind || '', apiVersion: inv.target.apiVersion, namespace: inv.target.namespace, name: inv.target.name }} cluster={inv.target.cluster} />
                ) : (
                  targetLabel(inv.target)
                )}
                <span className="ai-sep">·</span>
                {inv.agent}
              </>
            ) : pendingTarget ? (
              targetLabel(pendingTarget)
            ) : (
              'No investigation selected'
            )}
          </div>
        </div>
        {order.length > 1 && (
          <select className="ap-select ai-history" value={activeId || ''} onChange={(e) => select(e.target.value)} aria-label="Past investigations">
            {order.map((id) => {
              const i = investigations[id];
              return i ? <option key={id} value={id}>{targetLabel(i.target)} · {i.status}</option> : null;
            })}
          </select>
        )}
        <button type="button" className="ap-icon-btn" onClick={closePanel} aria-label="Close investigation panel" title="Close (Esc)"><Cross2Icon /></button>
      </div>

      {inv && (
        <div className="ai-panel-status">
          <span className={`ai-status-pill is-${inv.status}`}>
            {inv.status === 'running' ? <span className="ai-spinner" /> : <span className={`ap-dot ap-dot--${inv.status === 'done' ? 'success' : inv.status === 'failed' ? 'danger' : 'warning'}`} />}
            {inv.status === 'running' ? 'Investigating' : inv.status === 'done' ? 'Complete' : inv.status === 'failed' ? 'Failed' : 'Cancelled'}
          </span>
          <span className="ai-muted ai-small">{formatElapsed(elapsed)} · {inv.toolCalls} tool call{inv.toolCalls === 1 ? '' : 's'}{inv.costUsd ? ` · $${inv.costUsd.toFixed(2)}` : ''}</span>
          <span className="ai-grow" />
          {inv.status === 'running' ? (
            <button type="button" className="ap-btn ap-btn--sm" onClick={() => cancel(inv.id)}><StopIcon /> Stop</button>
          ) : (
            <button type="button" className="ap-btn ap-btn--sm" onClick={() => investigate(inv.target)}><ReloadIcon /> Run again</button>
          )}
        </div>
      )}
      {inv?.status === 'running' && <div className="ai-progress" aria-hidden="true" />}

      <div
        className="ai-panel-body"
        ref={bodyRef}
        onScroll={(e) => {
          const el = e.currentTarget;
          stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
        }}
      >
        {starting && !inv && (
          <div className="ai-empty"><span className="ai-spinner" /> Starting agent…</div>
        )}
        {startError && <div className="ai-banner is-error">{startError}</div>}
        {noAgent && <NoAgent />}
        {!inv && !starting && !startError && !noAgent && (
          <div className="ai-empty">No investigations yet. Use Investigate in the inspector or on an incident, or run “investigate cluster with AI” from the command palette.</div>
        )}
        {inv && (
          <>
            {inv.status !== 'running' && (inv.findings || inv.report) && <Findings inv={inv} />}
            {inv.error && <div className="ai-banner is-error">{inv.error}</div>}
            <div className="ap-group-label">{inv.status === 'running' ? 'Progress' : 'Steps'}</div>
            <div className="ai-timeline">
              {steps.map((s) => <StepRow key={s.e.seq} e={s.e} result={s.result} cluster={inv.target.cluster} />)}
            </div>
          </>
        )}
      </div>
    </aside>
  );
};

export default InvestigationPanel;
