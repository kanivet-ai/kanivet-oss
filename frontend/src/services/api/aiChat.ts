import { apiClient } from './client';
import type { Findings, InvestigationEvent } from './ai';

/** One piece of context attached to a message (a removable chip). */
export interface ContextChip {
  type: 'cluster' | 'namespace' | 'resource' | 'finding' | 'yaml' | 'text';
  label: string;
  cluster?: string;
  kind?: string;
  apiVersion?: string;
  namespace?: string;
  name?: string;
  /** Gathered content; redacted by the backend before storing/sending. */
  text?: string;
  /** Ask the backend to attach the live object (redacted). */
  includeYaml?: boolean;
}

export interface Proposal {
  id: string;
  summary?: string;
  kind: string;
  apiVersion?: string;
  namespace?: string;
  name: string;
  patchType?: string;
  patch: unknown;
  status: 'proposed' | 'dry-run-ok' | 'dry-run-failed' | 'applied' | 'apply-failed' | 'rejected';
  error?: string;
  before?: string;
  after?: string;
}

export interface ChatMessage {
  id: string;
  role: 'user' | 'assistant';
  text: string;
  context?: ContextChip[];
  events?: InvestigationEvent[];
  findings?: Findings;
  status?: 'running' | 'done' | 'failed' | 'cancelled';
  error?: string;
  costUsd?: number;
  time: string;
  proposals?: Proposal[];
}

export interface CodeWork {
  repo: string;
  branch?: string;
  baseBranch?: string;
  baseSha: string;
  createdBranch?: boolean;
  dirtyAtStart?: string[];
  committed?: string;
}

export type ChatMode = 'ask' | 'code';

export interface Chat {
  id: string;
  title: string;
  agentId: 'claude' | 'codex';
  agent: string;
  mode: ChatMode;
  cluster?: string;
  sessionId?: string;
  mcp: '' | 'connected' | 'blocked' | 'off';
  mcpNote?: string;
  noMcp?: boolean;
  code?: CodeWork;
  source?: string;
  created: string;
  updated: string;
  messages: ChatMessage[];
  running: boolean;
  version: number;
  permissions: string[];
}

export interface ChatSummary {
  id: string;
  title: string;
  agent: string;
  mode: ChatMode;
  cluster?: string;
  source?: string;
  updated: string;
  running: boolean;
  turns: number;
}

export interface NewChatInput {
  agent?: string;
  mode?: ChatMode;
  cluster?: string;
  title?: string;
  source?: string;
  noMcp?: boolean;
  text?: string;
  context?: ContextChip[];
  repo?: string;
  newBranch?: boolean;
  allowDirty?: boolean;
}

export interface CodeLocation {
  path: string;
  name: string;
  remotes?: string[];
  charts?: string[];
}

export interface CodeSuggestion {
  location: CodeLocation;
  score: number;
  reason: string;
  subPath?: string;
}

export interface SourceHints {
  repoUrls?: string[];
  paths?: string[];
  charts?: string[];
  names?: string[];
  manager?: string;
}

export interface RepoStatus {
  root: string;
  branch: string;
  headSha: string;
  dirty: string[];
  untracked: string[];
}

export interface FileDiff {
  path: string;
  status: 'modified' | 'added' | 'deleted';
  original: string;
  modified: string;
  patch: string;
  preexisting?: boolean;
  binary?: boolean;
}

const ax = () => apiClient.getAxios();

export const aiChatApi = {
  async list(): Promise<ChatSummary[]> {
    return (await ax().get('/ai/chats')).data.chats || [];
  },
  async create(input: NewChatInput): Promise<Chat> {
    return (await ax().post('/ai/chats', input)).data;
  },
  /** Returns null when unchanged since `since`. */
  async get(id: string, since = -1): Promise<Chat | null> {
    const d = (await ax().get(`/ai/chats/${id}`, { params: { since } })).data;
    return d.unchanged ? null : d;
  },
  async send(id: string, text: string, context?: ContextChip[]): Promise<void> {
    await ax().post(`/ai/chats/${id}/messages`, { text, context: context || [] });
  },
  async cancel(id: string): Promise<void> {
    await ax().post(`/ai/chats/${id}/cancel`);
  },
  async rename(id: string, title: string): Promise<void> {
    await ax().patch(`/ai/chats/${id}`, { title });
  },
  async remove(id: string): Promise<void> {
    await ax().delete(`/ai/chats/${id}`);
  },
  async proposal(id: string, msg: string, pid: string, action: 'dry-run' | 'apply' | 'reject'): Promise<Proposal> {
    try {
      return (await ax().post(`/ai/chats/${id}/proposals/${msg}/${pid}/${action}`)).data.proposal;
    } catch (e: any) {
      if (e?.response?.data?.proposal) return e.response.data.proposal;
      throw e;
    }
  },
  async diff(id: string): Promise<{ files: FileDiff[]; work: CodeWork }> {
    return (await ax().get(`/ai/chats/${id}/diff`)).data;
  },
  async discard(id: string, paths?: string[]): Promise<string[]> {
    return (await ax().post(`/ai/chats/${id}/discard`, { paths: paths || [] })).data.discarded || [];
  },
  async commit(id: string, branch: string, message: string): Promise<string> {
    return (await ax().post(`/ai/chats/${id}/commit`, { branch, message })).data.sha;
  },
  async openEditor(id: string, path?: string, editor?: 'code' | 'cursor'): Promise<string> {
    return (await ax().post(`/ai/chats/${id}/open-editor`, { path: path || '', editor: editor || 'code' })).data.url || '';
  },
  async locations(): Promise<CodeLocation[]> {
    return (await ax().get('/ai/code-locations')).data.locations || [];
  },
  async addLocation(path: string): Promise<CodeLocation[]> {
    return (await ax().post('/ai/code-locations', { path })).data.locations || [];
  },
  async removeLocation(path: string): Promise<CodeLocation[]> {
    return (await ax().delete('/ai/code-locations', { params: { path } })).data.locations || [];
  },
  async suggest(cluster: string, kind?: string, namespace?: string, name?: string): Promise<{ hints: SourceHints; suggestions: CodeSuggestion[] }> {
    return (await ax().get('/ai/code-locations/suggest', { params: { cluster, kind, namespace, name } })).data;
  },
  async repoStatus(path: string): Promise<RepoStatus> {
    return (await ax().get('/ai/repo-status', { params: { path } })).data;
  },
};
