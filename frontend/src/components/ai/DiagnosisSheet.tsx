import React, { useEffect, useMemo, useState } from 'react';
import { ArrowRightIcon, Cross2Icon, FileTextIcon, Share1Icon } from '@radix-ui/react-icons';
import { useAIChatStore } from '../../store/aiChatStore';
import type { Chat, ChatMessage } from '../../services/api/aiChat';
import type { FindingResource } from '../../services/api/ai';
import { SparkleIcon } from './AIIcons';
import { ChipView, FindingsCard, Inline, Markdown, Ref } from './ChatParts';
import { Composer } from './ChatThread';
import { ChatHeaderInfo } from './ChatPanel';
import ProposalCard from './ProposalCard';
import { groupEvents, messageResources, toolVerb } from './chatUtils';
import { askAI, clusterChip, resourceChip, textChip } from './aiContext';
import { apiVersionFor, formatElapsed } from './aiUtils';
import { canOpenLogs, openLogs } from '../health/healthActions';
import { openTopology } from '../topology/topologyNav';
import { parseApiVersion } from '../../utils/resourceUtils';
import './AI.css';
import './AIChat.css';

const Timeline: React.FC<{ m: ChatMessage; now: number }> = ({ m, now }) => {
  const steps = useMemo(() => groupEvents(m.events).filter((s) => s.kind === 'tool' || (s.kind === 'status' && s.error)), [m.events]);
  const running = m.status === 'running';
  if (steps.length === 0) {
    return running ? (
      <div className="ai-dx-steps"><div className="ai-dx-step is-running"><span className="ai-dx-dot"><span className="ai-spinner" /></span><span>Thinking…</span></div></div>
    ) : null;
  }
  return (
    <ol className="ai-dx-steps" aria-label="What the agent did">
      {steps.map((s) => {
        if (s.kind !== 'tool') return <li key={s.key} className="ai-dx-step is-error"><span className="ai-dx-dot" /><span>{s.kind === 'status' ? s.text : ''}</span></li>;
        const state = !s.result ? (running ? 'running' : 'stopped') : s.result.ok === false ? 'error' : 'ok';
        const dur = s.result ? s.result.time - s.e.time : running ? now - s.e.time : 0;
        return (
          <li key={s.key} className={`ai-dx-step is-${state}`}>
            <span className="ai-dx-dot">{state === 'running' ? <span className="ai-spinner" /> : null}</span>
            <span className="ai-dx-label">{toolVerb(s.e.tool || '', s.e.input)}{state === 'running' ? '…' : ''}</span>
            {dur > 0 && <span className="ai-dx-dur">{dur < 1000 ? `${dur}ms` : `${(dur / 1000).toFixed(1)}s`}</span>}
          </li>
        );
      })}
    </ol>
  );
};

const ResourceCards: React.FC<{ refs: FindingResource[]; cluster: string }> = ({ refs, cluster }) =>
  refs.length === 0 ? null : (
    <div className="ai-dx-cards">
      {refs.slice(0, 8).map((r) => (
        <div key={`${r.kind}/${r.namespace}/${r.name}`} className="ai-dx-card">
          <span className="ai-dx-card-kind">{r.kind}</span>
          <Ref r={r} cluster={cluster} />
        </div>
      ))}
    </div>
  );

function primaryRef(chat: Chat, m: ChatMessage | undefined): FindingResource | undefined {
  const fromMsg = m ? messageResources(m, chat.cluster) : [];
  const fromChips = chat.messages[0]?.context?.filter((c) => c.type === 'resource' && c.name).map((c) => ({ kind: c.kind!, apiVersion: c.apiVersion, namespace: c.namespace, name: c.name!, cluster: c.cluster })) || [];
  return m?.findings?.resources?.[0] || fromChips[0] || fromMsg[0];
}

const Actions: React.FC<{ chat: Chat; m: ChatMessage }> = ({ chat, m }) => {
  const sheetToPanel = useAIChatStore((s) => s.sheetToPanel);
  const ref = primaryRef(chat, m);
  const cluster = chat.cluster || '';
  const href = ref ? { apiVersion: apiVersionFor(ref), kind: ref.kind, namespace: ref.namespace, name: ref.name } : null;
  return (
    <div className="ai-dx-actions">
      {href && canOpenLogs(href) && (
        <button type="button" className="ap-btn ap-btn--sm" onClick={() => void openLogs(cluster, href)}><FileTextIcon /> Open logs</button>
      )}
      {href && (
        <button type="button" className="ap-btn ap-btn--sm" onClick={() => {
          const { group, version } = parseApiVersion(href.apiVersion);
          void openTopology(cluster, { focus: { group, version, kind: href.kind, namespace: href.namespace, name: href.name } as any });
        }}><Share1Icon /> Show topology</button>
      )}
      {href && (
        <button type="button" className="ap-btn ap-btn--sm ai-btn" onClick={() => {
          askAI({
            source: 'diagnosis', intent: 'fix', cluster, resource: { ...href, cluster },
            title: `Fix: ${m.findings?.summary?.slice(0, 60) || href.name}`,
            text: `Fix the problem diagnosed on ${href.kind} ${href.namespace ? href.namespace + '/' : ''}${href.name}${m.findings?.rootCause ? `: ${m.findings.rootCause}` : ''}.`,
            chips: [clusterChip(cluster), resourceChip(cluster, href), textChip('Diagnosis', [m.findings?.summary, m.findings?.rootCause, ...(m.findings?.nextSteps || [])].filter(Boolean).join('\n') || m.text.slice(0, 3000))],
          });
        }}><SparkleIcon size={12} /> Fix with AI</button>
      )}
      <span className="ai-grow" />
      <button type="button" className="ap-btn ap-btn--sm" onClick={sheetToPanel}>Continue in chat <kbd className="ai-kbd">⌘J</kbd></button>
    </div>
  );
};

/** The palette's in-place diagnosis view: the same chat session as the side
 * panel, shown as a centred sheet that streams the agent's work. Esc or ⌘J
 * moves it to the side panel; the run keeps going. */
const DiagnosisSheet: React.FC = () => {
  const { sheetOpen, activeId, chats, sheetToPanel, busy, error, send, cancel } = useAIChatStore();
  const chat = activeId ? chats[activeId] : null;
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    if (!chat?.running) return;
    const t = setInterval(() => setNow(Date.now()), 250);
    return () => clearInterval(t);
  }, [chat?.running]);
  useEffect(() => {
    if (!sheetOpen) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault();
        e.stopPropagation();
        sheetToPanel();
      }
    };
    window.addEventListener('keydown', onKey, true);
    return () => window.removeEventListener('keydown', onKey, true);
  }, [sheetOpen, sheetToPanel]);
  if (!sheetOpen) return null;

  const msgs = chat?.messages || [];
  const lastUserIdx = msgs.map((m) => m.role).lastIndexOf('user');
  const q = lastUserIdx >= 0 ? msgs[lastUserIdx] : undefined;
  const a = lastUserIdx >= 0 ? msgs[lastUserIdx + 1] : undefined;
  const running = a?.status === 'running';
  const refs = a ? messageResources(a, chat?.cluster) : [];
  const answerText = a ? groupEvents(a.events).filter((s) => s.kind === 'text').map((s) => (s as any).text).join('\n\n') : '';
  const elapsed = a ? (running ? now : Date.parse(chat!.updated)) - Date.parse(a.time) : 0;

  return (
    <div className="ai-dx-overlay" onMouseDown={(e) => { if (e.target === e.currentTarget) sheetToPanel(); }}>
      <div className="ai-dx" role="dialog" aria-label="AI diagnosis">
        <div className="ai-dx-head">
          <span className={`ai-tile${running ? ' is-live' : ''}`} aria-hidden="true"><SparkleIcon /></span>
          <div className="ai-dx-q">
            <div className="ai-dx-question">{q?.text || 'Starting…'}</div>
            {q?.context && q.context.length > 0 && <div className="ai-chips">{q.context.map((c, i) => <ChipView key={i} c={c} />)}</div>}
          </div>
          <button type="button" className="ap-icon-btn" onClick={sheetToPanel} aria-label="Minimise to chat panel" title="Minimise to chat (Esc)"><Cross2Icon /></button>
        </div>
        {chat && <div className="ai-dx-meta"><ChatHeaderInfo chat={chat} /></div>}
        {running && <div className="ai-progress" aria-hidden="true" />}
        <div className="ai-dx-body">
          {!chat && (busy ? <div className="ai-empty"><span className="ai-spinner" /> Starting the agent…</div> : error ? <div className="ai-banner is-error">{error}</div> : null)}
          {lastUserIdx > 1 && <div className="ai-small ai-muted">{Math.floor(lastUserIdx / 2)} earlier exchange{lastUserIdx > 3 ? 's' : ''} · <button type="button" className="ai-linkbtn" onClick={sheetToPanel}>show in chat</button></div>}
          {a && (
            <div className="ai-dx-grid">
              <section className="ai-dx-col">
                <div className="ap-group-label">{running ? `Working · ${formatElapsed(elapsed)}` : `Steps · ${formatElapsed(elapsed)}`}</div>
                <Timeline m={a} now={now} />
                {refs.length > 0 && <div className="ap-group-label">Resources</div>}
                <ResourceCards refs={refs} cluster={chat?.cluster || ''} />
              </section>
              <section className="ai-dx-col ai-dx-answer">
                {a.findings && !running ? (
                  <>
                    <FindingsCard f={a.findings} cluster={chat?.cluster || ''} />
                    {a.findings.resources && a.findings.resources.length > 0 && (
                      <>
                        <div className="ap-group-label">Evidence</div>
                        <div className="ai-chips">{a.findings.resources.map((r, i) => <Ref key={i} r={r} cluster={chat?.cluster || ''} chip />)}</div>
                      </>
                    )}
                    <details className="ai-dx-report"><summary>Full answer</summary><Markdown md={answerText} cluster={chat?.cluster || ''} /></details>
                  </>
                ) : answerText ? (
                  <Markdown md={answerText} cluster={chat?.cluster || ''} />
                ) : running ? (
                  <div className="ai-dx-skeleton"><span /><span /><span /></div>
                ) : null}
                {(a.proposals || []).map((p) => <ProposalCard key={p.id} msgId={a.id} p={p} cluster={chat?.cluster || ''} />)}
                {a.error && <div className="ai-banner is-error">{a.error}</div>}
                {!running && a.status === 'done' && a.findings?.summary === undefined && answerText === '' && <div className="ai-muted"><Inline text="No answer." cluster="" /></div>}
              </section>
            </div>
          )}
          {a && !running && chat && <Actions chat={chat} m={a} />}
        </div>
        <div className="ai-dx-foot">
          <Composer running={chat?.running} placeholder="Ask a follow-up…" onSend={(t) => void send(t)} onStop={() => void cancel()} />
          <div className="ai-dx-hint"><span><kbd className="ai-kbd">Esc</kbd> minimise</span><span><kbd className="ai-kbd">⌘J</kbd> continue in chat</span><ArrowRightIcon /></div>
        </div>
      </div>
    </div>
  );
};

export default DiagnosisSheet;
