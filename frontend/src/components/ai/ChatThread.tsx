import React, { useEffect, useMemo, useRef, useState } from 'react';
import { ArrowUpIcon, StopIcon } from '@radix-ui/react-icons';
import type { Chat, ChatMessage, ContextChip } from '../../services/api/aiChat';
import { useAIChatStore } from '../../store/aiChatStore';
import { ChipView, FindingsCard, Markdown, ThinkingStep, ToolStep } from './ChatParts';
import ProposalCard from './ProposalCard';
import { groupEvents, toolCount } from './chatUtils';
import { formatElapsed } from './aiUtils';

const AssistantMessage: React.FC<{ m: ChatMessage; cluster: string; now: number; onReview?: () => void; isCode: boolean }> = ({ m, cluster, now, onReview, isCode }) => {
  const steps = useMemo(() => groupEvents(m.events), [m.events]);
  const running = m.status === 'running';
  const tools = toolCount(m);
  const hasEdits = (m.events || []).some((e) => e.type === 'tool' && /^(Edit|Write|MultiEdit)$/.test(e.tool || ''));
  return (
    <div className="ai-msg ai-msg--assistant">
      {steps.length === 0 && running && <div className="ai-typing"><span /><span /><span /></div>}
      {steps.map((s) => {
        switch (s.kind) {
          case 'text':
            return <div key={s.key} className="ai-step ai-step--text"><Markdown md={s.text} cluster={cluster} /></div>;
          case 'thinking':
            return <ThinkingStep key={s.key} text={s.text} />;
          case 'tool':
            return <ToolStep key={s.key} e={s.e} result={s.result} running={running} now={now} />;
          default:
            return <div key={s.key} className={`ai-step ai-step--status${s.error ? ' is-error' : ''}`}>{s.text}</div>;
        }
      })}
      {m.findings && !running && <FindingsCard f={m.findings} cluster={cluster} />}
      {(m.proposals || []).map((p) => <ProposalCard key={p.id} msgId={m.id} p={p} cluster={cluster} />)}
      {m.error && <div className="ai-banner is-error">{m.error}</div>}
      {!running && (
        <div className="ai-msg-foot">
          <span>{m.status === 'cancelled' ? 'Stopped' : m.status === 'failed' ? 'Failed' : 'Done'}</span>
          {tools > 0 && <span>· {tools} tool call{tools === 1 ? '' : 's'}</span>}
          {m.costUsd ? <span>· ${m.costUsd.toFixed(2)}</span> : null}
          {isCode && hasEdits && onReview && (
            <button type="button" className="ap-btn ap-btn--sm ai-review-cta" onClick={onReview}>Review changes</button>
          )}
        </div>
      )}
    </div>
  );
};

const UserMessage: React.FC<{ m: ChatMessage; seeded: boolean }> = ({ m, seeded }) => {
  const [open, setOpen] = useState(false);
  const long = m.text.length > 280;
  return (
    <div className="ai-msg ai-msg--user">
      <div className="ai-bubble">
        {seeded && <div className="ai-bubble-tag">Context from Kanivet</div>}
        <div className={long && !open ? 'ai-bubble-text is-clamped' : 'ai-bubble-text'}>{m.text}</div>
        {long && <button type="button" className="ai-linkbtn" onClick={() => setOpen(!open)}>{open ? 'Less' : 'More'}</button>}
        {m.context && m.context.length > 0 && <div className="ai-chips">{m.context.map((c, i) => <ChipView key={i} c={c} />)}</div>}
      </div>
    </div>
  );
};

/** The messages of a chat, auto-scrolling while the agent streams. */
export const ChatMessages: React.FC<{ chat: Chat; onReview?: () => void; className?: string }> = ({ chat, onReview, className = '' }) => {
  const ref = useRef<HTMLDivElement>(null);
  const stick = useRef(true);
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    if (!chat.running) return;
    const t = setInterval(() => setNow(Date.now()), 500);
    return () => clearInterval(t);
  }, [chat.running]);
  const last = chat.messages[chat.messages.length - 1];
  const sig = `${chat.messages.length}:${last?.events?.length}:${last?.text?.length}:${last?.status}`;
  useEffect(() => {
    const el = ref.current;
    if (el && stick.current) el.scrollTop = el.scrollHeight;
  }, [sig]);
  return (
    <div
      className={`ai-thread ${className}`}
      ref={ref}
      onScroll={(e) => {
        const el = e.currentTarget;
        stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 48;
      }}
    >
      {chat.messages.map((m, i) =>
        m.role === 'user' ? (
          <UserMessage key={m.id} m={m} seeded={i === 0 && chat.source !== 'chat' && !!chat.source} />
        ) : (
          <AssistantMessage key={m.id} m={m} cluster={chat.cluster || ''} now={now} onReview={onReview} isCode={chat.mode === 'code'} />
        ),
      )}
    </div>
  );
};

/** Reply box: Enter sends, Shift+Enter is a newline. Chips are removable. */
export const Composer: React.FC<{
  running?: boolean;
  chips?: ContextChip[];
  onRemoveChip?: (i: number) => void;
  initial?: string;
  placeholder?: string;
  autoFocus?: boolean;
  onSend: (text: string) => void;
  onStop?: () => void;
  onChange?: (text: string) => void;
  startedAt?: number;
}> = ({ running, chips, onRemoveChip, initial = '', placeholder = 'Reply, ask a follow-up or steer the agent…', autoFocus, onSend, onStop, onChange, startedAt }) => {
  const [text, setText] = useState(initial);
  const ta = useRef<HTMLTextAreaElement>(null);
  useEffect(() => setText(initial), [initial]);
  useEffect(() => {
    if (autoFocus) ta.current?.focus();
  }, [autoFocus]);
  useEffect(() => {
    const el = ta.current;
    if (!el) return;
    el.style.height = 'auto';
    el.style.height = Math.min(160, el.scrollHeight) + 'px';
  }, [text]);
  const send = () => {
    const t = text.trim();
    if (!t || running) return;
    onSend(t);
    setText('');
  };
  return (
    <div className="ai-composer">
      {chips && chips.length > 0 && (
        <div className="ai-chips ai-composer-chips">{chips.map((c, i) => <ChipView key={i} c={c} onRemove={onRemoveChip ? () => onRemoveChip(i) : undefined} />)}</div>
      )}
      <div className="ai-composer-row">
        <textarea
          ref={ta}
          className="ai-composer-input"
          rows={1}
          value={text}
          placeholder={placeholder}
          aria-label="Message the agent"
          onChange={(e) => {
            setText(e.target.value);
            onChange?.(e.target.value);
          }}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
              e.preventDefault();
              send();
            }
          }}
        />
        {running && onStop ? (
          <button type="button" className="ai-send is-stop" onClick={onStop} aria-label="Stop the agent" title="Stop">
            <StopIcon />
          </button>
        ) : (
          <button type="button" className="ai-send" onClick={send} disabled={!text.trim()} aria-label="Send" title="Send (Enter)">
            <ArrowUpIcon />
          </button>
        )}
      </div>
      {running && startedAt ? <RunningLine startedAt={startedAt} /> : null}
    </div>
  );
};

const RunningLine: React.FC<{ startedAt: number }> = ({ startedAt }) => {
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, []);
  return <div className="ai-small ai-muted ai-running-line"><span className="ai-spinner" /> Working · {formatElapsed(now - startedAt)}</div>;
};

/** Composer wired to the store for the active chat. */
export const ChatComposer: React.FC<{ chat: Chat | null; autoFocus?: boolean }> = ({ chat, autoFocus }) => {
  const { send, cancel, draftChips, removeDraftChip, draftText, setDraft } = useAIChatStore();
  const last = chat?.messages[chat.messages.length - 1];
  return (
    <Composer
      running={chat?.running}
      chips={chat ? undefined : draftChips}
      onRemoveChip={chat ? undefined : removeDraftChip}
      initial={chat ? '' : draftText}
      placeholder={chat ? undefined : 'Ask about your cluster, a resource or a fix…'}
      autoFocus={autoFocus}
      onSend={(t) => void send(t)}
      onStop={() => void cancel()}
      onChange={chat ? undefined : (t) => setDraft(t)}
      startedAt={chat?.running && last ? Date.parse(last.time) : undefined}
    />
  );
};
