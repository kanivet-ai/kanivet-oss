import { apiClient } from './client';

export interface McpToolInfo {
  name: string;
  description: string;
  write: boolean;
  destructive: boolean;
  feature: string;
}

export interface McpSetupRecipe {
  id: string;
  label: string;
  kind: 'command' | 'json' | 'toml';
  where?: string;
  snippet: string;
  readOnly: boolean;
}

export interface McpStatus {
  enabled: boolean;
  allowWrites: boolean;
  readOnlyUrl: string;
  fullUrl: string;
  token: string;
  activeCluster?: string;
  lastCallAt?: string;
  activeClients: string[];
  tools: McpToolInfo[];
  setup: McpSetupRecipe[];
  tokenFile: string;
}

export interface McpCall {
  time: string;
  client: string;
  endpoint: 'readonly' | 'full';
  tool: string;
  cluster?: string;
  args?: string;
  durationMs: number;
  ok: boolean;
  error?: string;
  bytes: number;
  estTokens: number;
  write?: boolean;
}

export interface AgentCli {
  id: 'claude' | 'codex';
  name: string;
  path?: string;
  version?: string;
  available: boolean;
  install: string;
}

export interface InvestigationTarget {
  cluster: string;
  kind?: string;
  apiVersion?: string;
  namespace?: string;
  name?: string;
  context?: string;
  question?: string;
}

export interface InvestigationEvent {
  seq: number;
  time: number;
  type: 'status' | 'thinking' | 'text' | 'tool' | 'tool_result' | 'error';
  text?: string;
  tool?: string;
  input?: string;
  ok?: boolean;
}

export interface FindingResource {
  kind: string;
  apiVersion?: string;
  namespace?: string;
  name: string;
  cluster?: string;
}

export interface Finding {
  title: string;
  detail?: string;
  severity?: string;
  resources?: FindingResource[];
}

export interface Findings {
  summary: string;
  rootCause?: string;
  severity?: string;
  confidence?: string;
  findings?: Finding[];
  nextSteps?: string[];
  resources?: FindingResource[];
}

export interface Investigation {
  id: string;
  agent: string;
  target: InvestigationTarget;
  status: 'running' | 'done' | 'failed' | 'cancelled';
  started: string;
  ended?: string;
  error?: string;
  report?: string;
  findings?: Findings;
  costUsd?: number;
  events: InvestigationEvent[];
  toolCalls: number;
}

const ax = () => apiClient.getAxios();

export const aiApi = {
  async status(): Promise<McpStatus> {
    return (await ax().get('/mcp/status')).data;
  },
  async calls(): Promise<McpCall[]> {
    return (await ax().get('/mcp/calls')).data.calls || [];
  },
  async updateSettings(patch: { enabled?: boolean; allowWrites?: boolean }): Promise<McpStatus> {
    return (await ax().put('/mcp/settings', patch)).data;
  },
  async rotateToken(): Promise<McpStatus> {
    return (await ax().post('/mcp/token/rotate')).data;
  },
  async setContext(cluster: string | null): Promise<void> {
    await ax().post('/mcp/context', { cluster: cluster || '' });
  },
  async agents(): Promise<AgentCli[]> {
    return (await ax().get('/ai/agents')).data.agents || [];
  },
  async startInvestigation(target: InvestigationTarget, agent?: string): Promise<Investigation> {
    return (await ax().post('/ai/investigations', { target, agent: agent || '' })).data;
  },
  async getInvestigation(id: string, after: number): Promise<Investigation> {
    return (await ax().get(`/ai/investigations/${id}`, { params: { after } })).data;
  },
  async cancelInvestigation(id: string): Promise<void> {
    await ax().post(`/ai/investigations/${id}/cancel`);
  },
  async listInvestigations(): Promise<Investigation[]> {
    return (await ax().get('/ai/investigations')).data.investigations || [];
  },
};
