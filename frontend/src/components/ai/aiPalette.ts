import { useAIStore } from '../../store/aiStore';
import { useAIChatStore } from '../../store/aiChatStore';
import { toggleAskAI } from './askToggle';

/** Command palette entries (type ">" in the palette). */
export const AI_PALETTE_COMMANDS = [
  { id: 'ai-ask', name: 'Ask AI (⌘J)', description: 'Chat with your agent about the current cluster, namespace or resource', category: 'Commands' },
  { id: 'ai-investigate-cluster', name: 'investigate cluster with AI', description: 'Ask an agent what is failing in this cluster', category: 'Commands' },
  { id: 'ai-investigations', name: 'show AI chats', description: 'Past AI chats and investigations', category: 'Commands' },
  { id: 'ai-mcp-settings', name: 'AI & MCP settings', description: 'Connect Claude Code, Codex, Cursor or VS Code to Kanivet; code locations', category: 'Commands' },
];

/** Runs an AI palette command; false when the id is not one of ours. */
export function runAIPaletteCommand(id: string, cluster: string | null): boolean {
  const s = useAIStore.getState();
  switch (id) {
    case 'ai-ask':
      useAIChatStore.getState().closePanel();
      toggleAskAI();
      return true;
    case 'ai-mcp-settings':
      s.openSettings();
      return true;
    case 'ai-investigate-cluster':
      if (cluster) void s.investigate({ cluster });
      else s.openSettings();
      return true;
    case 'ai-investigations': {
      const c = useAIChatStore.getState();
      c.open();
      c.setView('history');
      return true;
    }
  }
  return false;
}
