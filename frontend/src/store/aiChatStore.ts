import { create } from 'zustand';
import { aiChatApi, type Chat, type ChatMode, type ChatSummary, type ContextChip, type Proposal } from '../services/api/aiChat';
import { ASK_AI_EVENT, dedupeChips, questionRefersTo, type AskSeed } from '../components/ai/aiContext';

export type ChatView = 'chat' | 'history' | 'review';

export interface FixChoice {
  mode: ChatMode;
  repo?: string;
  newBranch?: boolean;
  allowDirty?: boolean;
}

interface AIChatState {
  panelOpen: boolean;
  view: ChatView;
  /** The palette's diagnosis sheet shows this chat. */
  sheetOpen: boolean;
  activeId: string | null;
  chats: Record<string, Chat>;
  list: ChatSummary[];
  /** Composer state for a chat that does not exist yet. */
  draftText: string;
  draftChips: ContextChip[];
  /** The inspector resource offered (not attached) for a new chat. */
  draftSuggestion: ContextChip | null;
  draftSeed: AskSeed | null;
  /** A fix waiting for the user to pick "in code" or "in cluster". */
  pendingFix: AskSeed | null;
  agentId: string;
  noMcp: boolean;
  error: string | null;
  /** Repo with uncommitted changes the user must confirm. */
  dirtyRepo: { repo: string; files: string[] } | null;
  busy: boolean;

  open: (seed?: AskSeed) => void;
  openSheet: (seed: AskSeed) => void;
  sheetToPanel: () => void;
  closeSheet: () => void;
  closePanel: () => void;
  setView: (v: ChatView) => void;
  newChat: (chips?: ContextChip[], suggestion?: ContextChip | null) => void;
  addDraftSuggestion: () => void;
  setDraft: (text: string, chips?: ContextChip[]) => void;
  removeDraftChip: (i: number) => void;
  send: (text: string, chips?: ContextChip[]) => Promise<void>;
  startFix: (seed: AskSeed, choice: FixChoice) => Promise<void>;
  cancelFix: () => void;
  select: (id: string) => void;
  cancel: () => Promise<void>;
  rename: (id: string, title: string) => Promise<void>;
  remove: (id: string) => Promise<void>;
  loadList: () => Promise<void>;
  setAgent: (id: string) => void;
  setNoMcp: (v: boolean) => void;
  proposalAction: (msgId: string, p: Proposal, action: 'dry-run' | 'apply' | 'reject') => Promise<Proposal | null>;
  refresh: (id: string) => Promise<void>;
}

const pollers = new Map<string, ReturnType<typeof setTimeout>>();

const readPref = (k: string, d: string) => {
  try {
    return localStorage.getItem(k) ?? d;
  } catch {
    return d;
  }
};
const writePref = (k: string, v: string) => {
  try {
    localStorage.setItem(k, v);
  } catch {
    /* ignore */
  }
};

const errText = (e: any, d: string) => e?.response?.data?.error || e?.message || d;

export const useAIChatStore = create<AIChatState>((set, get) => {
  const poll = (id: string) => {
    if (pollers.has(id)) return;
    const tick = async () => {
      const cur = get().chats[id];
      try {
        const c = await aiChatApi.get(id, cur ? cur.version : -1);
        if (c) set((s) => ({ chats: { ...s.chats, [id]: c } }));
        const latest = c || get().chats[id];
        if (latest && !latest.running) {
          pollers.delete(id);
          void get().loadList();
          return;
        }
      } catch {
        /* backend restarting: keep trying */
      }
      pollers.set(id, setTimeout(tick, 450));
    };
    pollers.set(id, setTimeout(tick, 150));
  };

  const create = async (seed: AskSeed | null, text: string, chips: ContextChip[], extra: Partial<FixChoice> = {}) => {
    set({ busy: true, error: null, dirtyRepo: null });
    try {
      const c = await aiChatApi.create({
        agent: get().agentId || undefined,
        mode: extra.mode || seed?.mode || 'ask',
        cluster: seed?.cluster || chips.find((x) => x.cluster)?.cluster,
        title: seed?.title,
        source: seed?.source || 'chat',
        noMcp: get().noMcp,
        text,
        context: chips,
        repo: extra.repo,
        newBranch: extra.newBranch,
        allowDirty: extra.allowDirty,
      });
      set((s) => ({
        chats: { ...s.chats, [c.id]: c }, activeId: c.id, view: 'chat', busy: false,
        draftText: '', draftChips: [], draftSuggestion: null, draftSeed: null, pendingFix: null,
      }));
      poll(c.id);
      void get().loadList();
    } catch (e: any) {
      const d = e?.response?.data;
      if (d?.code === 'dirty' && extra.repo) {
        set({ busy: false, dirtyRepo: { repo: extra.repo, files: [...(d.status?.dirty || []), ...(d.status?.untracked || [])] } });
        return;
      }
      set({ busy: false, error: errText(e, 'Could not start the chat') });
    }
  };

  return {
    panelOpen: false,
    view: 'chat',
    sheetOpen: false,
    activeId: null,
    chats: {},
    list: [],
    draftText: '',
    draftChips: [],
    draftSuggestion: null,
    draftSeed: null,
    pendingFix: null,
    agentId: readPref('kanivet.ai.agent', ''),
    noMcp: readPref('kanivet.ai.noMcp', '') === '1',
    error: null,
    dirtyRepo: null,
    busy: false,

    open: (seed) => {
      set({ panelOpen: true, sheetOpen: false, error: null });
      if (!seed) {
        if (!get().activeId) set({ view: get().list.length ? 'history' : 'chat' });
        return;
      }
      const chips = dedupeChips(seed.chips || []);
      if (seed.intent === 'fix') {
        set({ pendingFix: { ...seed, chips }, activeId: null, view: 'chat', dirtyRepo: null });
        return;
      }
      if (seed.autoSend === false) {
        set({ activeId: null, view: 'chat', draftText: seed.text, draftChips: chips, draftSuggestion: null, draftSeed: seed, pendingFix: null });
        return;
      }
      set({ pendingFix: null });
      void create(seed, seed.text, chips);
    },
    openSheet: (seed) => {
      set({ sheetOpen: true, panelOpen: false, pendingFix: null, error: null });
      void create(seed, seed.text, dedupeChips(seed.chips || []));
    },
    sheetToPanel: () => set({ sheetOpen: false, panelOpen: true, view: 'chat' }),
    closeSheet: () => set({ sheetOpen: false }),
    closePanel: () => set({ panelOpen: false }),
    setView: (view) => {
      set({ view });
      if (view === 'history') void get().loadList();
    },
    newChat: (chips, suggestion) => set({ activeId: null, view: 'chat', pendingFix: null, draftText: '', draftChips: chips || [], draftSuggestion: suggestion || null, draftSeed: null, error: null }),
    addDraftSuggestion: () => set((s) => (s.draftSuggestion ? { draftChips: dedupeChips([...s.draftChips, s.draftSuggestion]), draftSuggestion: null } : {})),
    setDraft: (text, chips) => set((s) => ({ draftText: text, draftChips: chips ?? s.draftChips })),
    removeDraftChip: (i) => set((s) => ({ draftChips: s.draftChips.filter((_, j) => j !== i) })),
    send: async (text, chips) => {
      const id = get().activeId;
      if (!id) {
        // The offered inspector resource goes along only when the question refers to it.
        const sug = get().draftSuggestion;
        const base = chips ?? get().draftChips;
        const refers = sug && questionRefersTo(text, { kind: sug.kind || '', name: sug.name || '' });
        await create(get().draftSeed, text, refers && sug ? dedupeChips([...base, sug]) : base);
        return;
      }
      set({ error: null });
      try {
        await aiChatApi.send(id, text, chips);
        await get().refresh(id);
        poll(id);
      } catch (e: any) {
        set({ error: errText(e, 'Could not send') });
      }
    },
    startFix: async (seed, choice) => {
      const text = choice.mode === 'code'
        ? `${seed.text}\n\nMake the change in this repository's source (manifests, Helm values/templates, Kustomize or Terraform), not in the live cluster.`
        : `${seed.text}\n\nPropose the change for the live cluster as a kanivet-patch block so I can review a dry-run diff and approve it.`;
      await create(seed, text, seed.chips || [], choice);
    },
    cancelFix: () => set({ pendingFix: null, dirtyRepo: null }),
    select: (id) => {
      set({ activeId: id, view: 'chat', panelOpen: true, pendingFix: null, error: null });
      void get().refresh(id).then(() => {
        if (get().chats[id]?.running) poll(id);
      });
    },
    cancel: async () => {
      const id = get().activeId;
      if (id) await aiChatApi.cancel(id).catch(() => {});
    },
    rename: async (id, title) => {
      await aiChatApi.rename(id, title);
      await get().refresh(id);
      await get().loadList();
    },
    remove: async (id) => {
      await aiChatApi.remove(id);
      set((s) => {
        const chats = { ...s.chats };
        delete chats[id];
        return { chats, activeId: s.activeId === id ? null : s.activeId };
      });
      await get().loadList();
    },
    loadList: async () => {
      try {
        set({ list: await aiChatApi.list() });
      } catch {
        /* backend down */
      }
    },
    setAgent: (agentId) => {
      writePref('kanivet.ai.agent', agentId);
      set({ agentId });
    },
    setNoMcp: (noMcp) => {
      writePref('kanivet.ai.noMcp', noMcp ? '1' : '');
      set({ noMcp });
    },
    proposalAction: async (msgId, p, action) => {
      const id = get().activeId;
      if (!id) return null;
      try {
        const out = await aiChatApi.proposal(id, msgId, p.id, action);
        await get().refresh(id);
        return out;
      } catch (e: any) {
        set({ error: errText(e, 'Proposal action failed') });
        return null;
      }
    },
    refresh: async (id) => {
      try {
        const c = await aiChatApi.get(id, -1);
        if (c) set((s) => ({ chats: { ...s.chats, [id]: c } }));
      } catch (e: any) {
        set({ error: errText(e, 'Could not load the chat') });
      }
    },
  };
});

// Every AI entry point dispatches ASK_AI_EVENT (see aiContext.askAI).
if (typeof window !== 'undefined') {
  window.addEventListener(ASK_AI_EVENT, (e: Event) => {
    const seed = (e as CustomEvent<AskSeed>).detail;
    if (!seed) return;
    if (seed.surface === 'sheet') useAIChatStore.getState().openSheet(seed);
    else useAIChatStore.getState().open(seed);
  });
}
