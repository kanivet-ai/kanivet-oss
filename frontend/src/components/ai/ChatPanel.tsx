import React, { useEffect, useState } from 'react';
import { Cross2Icon, CounterClockwiseClockIcon, Pencil1Icon, PlusIcon, TrashIcon } from '@radix-ui/react-icons';
import { useAIChatStore } from '../../store/aiChatStore';
import { useAIStore } from '../../store/aiStore';
import type { Chat } from '../../services/api/aiChat';
import { SparkleIcon } from './AIIcons';
import { ChatComposer, ChatMessages } from './ChatThread';
import FixChooser from './FixChooser';
import CodeReview from './CodeReview';
import { timeAgo } from './aiUtils';
import { currentContextChips, inspectorResourceChip } from './currentContext';
import './AI.css';
import './AIChat.css';

/** Which agent and what it may do: shown in every chat header. */
export const ChatHeaderInfo: React.FC<{ chat: Chat | null }> = ({ chat }) => {
  const { agentId, setAgent, noMcp, setNoMcp } = useAIChatStore();
  const agents = useAIStore((s) => s.agents);
  const loadAgents = useAIStore((s) => s.loadAgents);
  useEffect(() => {
    if (!agents) void loadAgents();
  }, [agents, loadAgents]);
  if (chat) {
    return (
      <>
        <div className="ai-perms" aria-label="Agent permissions">
          <span className="ai-perm is-agent">{chat.agent}</span>
          <span className="ai-perm">{chat.mode === 'code' ? 'Code fix' : 'Read-only'}</span>
          {chat.permissions.map((p) => <span key={p} className={`ai-perm${p.includes('blocked') ? ' is-warn' : ''}`}>{p}</span>)}
        </div>
        {chat.mcpNote && <div className="ai-banner is-warning ai-banner--tight">{chat.mcpNote}</div>}
      </>
    );
  }
  const avail = (agents || []).filter((a) => a.available);
  return (
    <div className="ai-perms">
      <select className="ap-select ai-agent-select" value={agentId || avail[0]?.id || ''} onChange={(e) => setAgent(e.target.value)} aria-label="Agent">
        {(agents || []).map((a) => <option key={a.id} value={a.id} disabled={!a.available}>{a.name}{a.available ? '' : ' (not installed)'}</option>)}
      </select>
      <label className="ai-perm ai-perm--toggle" title="Run without Kanivet's MCP server; Kanivet inlines the context instead">
        <input type="checkbox" checked={!noMcp} onChange={(e) => setNoMcp(!e.target.checked)} /> Live cluster tools (read-only MCP)
      </label>
    </div>
  );
};

const History: React.FC = () => {
  const { list, select, rename, remove, loadList } = useAIChatStore();
  const [editing, setEditing] = useState<string | null>(null);
  const [title, setTitle] = useState('');
  useEffect(() => {
    void loadList();
  }, [loadList]);
  if (list.length === 0) return <div className="ai-empty">No chats yet.</div>;
  return (
    <div className="ai-history-list" role="list">
      {list.map((c) => (
        <div key={c.id} className="ai-history-row" role="listitem">
          {editing === c.id ? (
            <form
              className="ai-history-edit"
              onSubmit={(e) => {
                e.preventDefault();
                void rename(c.id, title);
                setEditing(null);
              }}
            >
              <input className="ap-input" autoFocus value={title} onChange={(e) => setTitle(e.target.value)} onBlur={() => setEditing(null)} aria-label="Chat title" />
            </form>
          ) : (
            <button type="button" className="ai-history-main" onClick={() => select(c.id)}>
              <span className="ai-history-title ap-truncate">{c.running && <span className="ai-spinner" />} {c.title}</span>
              <span className="ai-small ai-muted">{c.agent} · {c.mode === 'code' ? 'code fix' : c.source || 'chat'}{c.cluster ? ` · ${c.cluster}` : ''} · {timeAgo(c.updated)}</span>
            </button>
          )}
          <button type="button" className="ap-icon-btn" aria-label="Rename" title="Rename" onClick={() => { setEditing(c.id); setTitle(c.title); }}><Pencil1Icon /></button>
          <button type="button" className="ap-icon-btn" aria-label="Delete" title="Delete" onClick={() => { if (window.confirm(`Delete "${c.title}"?`)) void remove(c.id); }}><TrashIcon /></button>
        </div>
      ))}
    </div>
  );
};

const EmptyState: React.FC = () => (
  <div className="ai-chat-empty">
    <span className="ai-tile ai-tile--lg" aria-hidden="true"><SparkleIcon size={22} /></span>
    <div className="ai-noagent-title">Ask about this cluster</div>
    <div className="ai-muted ai-small">The agent runs on your machine with your Claude Code or Codex login. It is read-only toward the cluster; fixes are proposed for you to review.</div>
  </div>
);

/** The side chat panel. Every AI entry point opens it (or the palette's
 * diagnosis sheet, which shows the same session). */
const ChatPanel: React.FC = () => {
  const { panelOpen, closePanel, activeId, chats, view, setView, pendingFix, newChat, error } = useAIChatStore();
  const chat = activeId ? chats[activeId] || null : null;

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && useAIChatStore.getState().panelOpen && !document.querySelector('.ap-overlay, .command-palette-overlay')) closePanel();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [closePanel]);

  if (!panelOpen) return null;
  const title = view === 'history' ? 'Chats' : pendingFix ? 'Fix with AI' : chat?.title || 'Ask AI';
  return (
    <aside className="ai-panel ai-chat-panel" aria-label="AI chat">
      <div className="ai-panel-header">
        <span className="ai-tile" aria-hidden="true"><SparkleIcon /></span>
        <div className="ai-panel-titles">
          <div className="ai-panel-title ap-truncate" title={title}>{title}</div>
          {chat && <div className="ai-panel-sub ap-truncate">{chat.cluster || 'no cluster'}{chat.code ? ` · ${chat.code.repo.split(/[\\/]/).pop()} @ ${chat.code.branch}` : ''}</div>}
        </div>
        <button type="button" className={`ap-icon-btn${view === 'history' ? ' is-active' : ''}`} onClick={() => setView(view === 'history' ? 'chat' : 'history')} aria-label="Past chats" title="Past chats"><CounterClockwiseClockIcon /></button>
        <button type="button" className="ap-icon-btn" onClick={() => newChat(currentContextChips(), inspectorResourceChip())} aria-label="New chat" title="New chat (⌘J)"><PlusIcon /></button>
        <button type="button" className="ap-icon-btn" onClick={closePanel} aria-label="Close AI chat" title="Close (Esc)"><Cross2Icon /></button>
      </div>
      {view !== 'history' && view !== 'review' && !pendingFix && <div className="ai-chat-meta"><ChatHeaderInfo chat={chat} /></div>}
      {chat?.running && <div className="ai-progress" aria-hidden="true" />}
      {view === 'history' ? (
        <div className="ai-panel-body"><History /></div>
      ) : view === 'review' && chat?.code ? (
        <div className="ai-panel-body ai-panel-body--flush"><CodeReview chat={chat} onBack={() => setView('chat')} /></div>
      ) : pendingFix ? (
        <div className="ai-panel-body"><FixChooser seed={pendingFix} /></div>
      ) : (
        <>
          {error && <div className="ai-banner is-error ai-banner--tight">{error}</div>}
          {chat ? <ChatMessages chat={chat} onReview={() => setView('review')} /> : <div className="ai-thread"><EmptyState /></div>}
          <ChatComposer chat={chat} autoFocus />
        </>
      )}
    </aside>
  );
};

export default ChatPanel;

