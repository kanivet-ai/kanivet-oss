import { useAIStore } from '../../store/aiStore';

/** Command palette entries (type ">" in the palette). */
export const AI_PALETTE_COMMANDS = [
  { id: 'ai-mcp-settings', name: 'AI & MCP settings', description: 'Connect Claude Code, Codex, Cursor or VS Code to Kanivet', category: 'Commands' },
  { id: 'ai-investigate-cluster', name: 'investigate cluster with AI', description: 'Ask an agent what is failing in this cluster', category: 'Commands' },
  { id: 'ai-investigations', name: 'show AI investigations', description: 'Open the investigation panel', category: 'Commands' },
];

/** Runs an AI palette command; false when the id is not one of ours. */
export function runAIPaletteCommand(id: string, cluster: string | null): boolean {
  const s = useAIStore.getState();
  switch (id) {
    case 'ai-mcp-settings':
      s.openSettings();
      return true;
    case 'ai-investigate-cluster':
      if (cluster) void s.investigate({ cluster });
      else s.openSettings();
      return true;
    case 'ai-investigations':
      if (s.order[0]) s.select(s.activeId || s.order[0]);
      else s.openPanel();
      return true;
  }
  return false;
}
