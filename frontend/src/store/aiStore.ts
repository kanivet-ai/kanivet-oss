import { create } from 'zustand';
import { aiApi, AgentCli, Investigation, InvestigationTarget, McpStatus } from '../services/api/ai';
import { mergeInvestigation } from '../components/ai/aiUtils';
import { INVESTIGATE_EVENT, type InvestigateRequest } from '../types/health';
import { askAI, clusterChip, resourceChip, textChip, type AskSeed } from '../components/ai/aiContext';

/** AI & MCP state: settings sheet, server status, and investigations. Kept
 * out of the main store so the feature stays self-contained. */
interface AIState {
  settingsOpen: boolean;
  status: McpStatus | null;
  statusError: string | null;
  agents: AgentCli[] | null;

  panelOpen: boolean;
  activeId: string | null;
  investigations: Record<string, Investigation>;
  order: string[];
  startError: string | null;
  starting: boolean;
  /** Target the user asked about when no agent CLI was found. */
  pendingTarget: InvestigationTarget | null;

  openSettings: () => void;
  closeSettings: () => void;
  refreshStatus: () => Promise<void>;
  setStatus: (s: McpStatus) => void;
  loadAgents: () => Promise<AgentCli[]>;

  investigate: (target: InvestigationTarget, agent?: string) => Promise<void>;
  investigateLegacy: (target: InvestigationTarget, agent?: string) => Promise<void>;
  select: (id: string) => void;
  cancel: (id: string) => Promise<void>;
  openPanel: () => void;
  closePanel: () => void;
}

const pollers = new Map<string, ReturnType<typeof setTimeout>>();

export const useAIStore = create<AIState>((set, get) => {
  const poll = (id: string) => {
    const tick = async () => {
      const cur = get().investigations[id];
      try {
        const snap = await aiApi.getInvestigation(id, cur ? cur.events.length : 0);
        set((s) => ({ investigations: { ...s.investigations, [id]: mergeInvestigation(s.investigations[id], snap) } }));
        if (snap.status !== 'running') {
          pollers.delete(id);
          return;
        }
      } catch {
        // Backend restarting: keep trying a little slower.
      }
      pollers.set(id, setTimeout(tick, 700));
    };
    if (!pollers.has(id)) pollers.set(id, setTimeout(tick, 300));
  };

  return {
    settingsOpen: false,
    status: null,
    statusError: null,
    agents: null,
    panelOpen: false,
    activeId: null,
    investigations: {},
    order: [],
    startError: null,
    starting: false,
    pendingTarget: null,

    openSettings: () => set({ settingsOpen: true }),
    closeSettings: () => set({ settingsOpen: false }),
    refreshStatus: async () => {
      try {
        set({ status: await aiApi.status(), statusError: null });
      } catch (e: any) {
        set({ statusError: e?.response?.data?.error || e?.message || 'MCP server unavailable' });
      }
    },
    setStatus: (status) => set({ status }),
    loadAgents: async () => {
      try {
        const agents = await aiApi.agents();
        set({ agents });
        return agents;
      } catch {
        set({ agents: [] });
        return [];
      }
    },

    investigate: async (target) => {
      // Investigations are chats now: open the chat panel seeded with the
      // target, so the user can keep talking to the agent.
      askAI(seedFromTarget(target));
    },
    investigateLegacy: async (target: InvestigationTarget, agent?: string) => {
      set({ panelOpen: true, starting: true, startError: null, pendingTarget: target });
      const agents = get().agents ?? (await get().loadAgents());
      if (!agents.some((a) => a.available)) {
        set({ starting: false, activeId: null });
        return;
      }
      try {
        const inv = await aiApi.startInvestigation(target, agent);
        set((s) => ({
          investigations: { ...s.investigations, [inv.id]: inv },
          order: [inv.id, ...s.order.filter((x) => x !== inv.id)],
          activeId: inv.id,
          starting: false,
          pendingTarget: null,
        }));
        poll(inv.id);
      } catch (e: any) {
        const data = e?.response?.data;
        if (data?.agents) set({ agents: data.agents });
        set({ starting: false, activeId: null, startError: data?.error || e?.message || 'Could not start the investigation' });
      }
    },
    select: (id) => {
      set((s) => ({ activeId: id, panelOpen: true, startError: null, pendingTarget: null, order: s.order.includes(id) ? s.order : [id, ...s.order] }));
      const known = get().investigations[id];
      // Unknown here (e.g. after a reload) or still running: fetch it.
      if (!known || known.status === 'running') poll(id);
    },
    cancel: async (id) => {
      try {
        await aiApi.cancelInvestigation(id);
      } catch {
        /* already finished */
      }
    },
    openPanel: () => set({ panelOpen: true }),
    closePanel: () => set({ panelOpen: false }),
  };
});

/** Turns an investigation target into a chat seed. */
export function seedFromTarget(t: InvestigationTarget): AskSeed {
  const chips = [clusterChip(t.cluster)];
  if (t.name && t.kind) chips.push(resourceChip(t.cluster, { kind: t.kind, apiVersion: t.apiVersion, namespace: t.namespace, name: t.name }));
  if (t.context) chips.push(textChip('Context', t.context));
  const label = t.name ? `${t.kind} ${t.namespace ? t.namespace + '/' : ''}${t.name}` : `cluster ${t.cluster}`;
  return {
    source: 'investigate',
    intent: 'ask',
    cluster: t.cluster,
    title: `Investigate ${label}`,
    text: t.question || (t.name
      ? `Investigate ${label}: is it healthy? If not, find the root cause with evidence and tell me how to fix it.`
      : 'Find what is currently failing in this cluster, the most likely root causes, and how to fix them.'),
    chips,
  };
}

/** Maps the Issues page's investigate request to an investigation target. */
export function targetFromInvestigateRequest(req: InvestigateRequest): InvestigationTarget {
  const issue = req.issue;
  return {
    cluster: req.cluster,
    kind: req.resource.kind,
    apiVersion: req.resource.apiVersion,
    namespace: req.resource.namespace,
    name: req.resource.name,
    context: req.context,
    question: issue ? `Explain this issue (${issue.title}) on the resource, its root cause and the fix.` : undefined,
  };
}

// The Issues page (and anything else) can start an investigation by
// dispatching INVESTIGATE_EVENT, the same as the inspector's button.
if (typeof window !== 'undefined') {
  window.addEventListener(INVESTIGATE_EVENT, (e: Event) => {
    const detail = (e as CustomEvent<InvestigateRequest>).detail;
    if (detail?.cluster && detail.resource?.name) void useAIStore.getState().investigate(targetFromInvestigateRequest(detail));
  });
}
