import { useStore } from '../../store';
import { openHealthPage } from './healthActions';
import { refreshIssues } from './healthStore';

export interface PaletteCommand {
  id: string;
  name: string;
  description: string;
  category: string;
  run: (cluster: string | null) => void;
}

/** Command palette entries for the health feature ("> issues", "> audit"). */
export const HEALTH_COMMANDS: PaletteCommand[] = [
  {
    id: 'health-issues',
    name: 'show issues',
    description: 'What is broken in this cluster right now',
    category: 'Commands',
    run: (c) => { if (c) void openHealthPage(c, 'issues'); },
  },
  {
    id: 'health-audit',
    name: 'run audit',
    description: 'Best-practice audit with scores and fixes',
    category: 'Commands',
    run: (c) => { if (c) void openHealthPage(c, 'audit'); },
  },
  {
    id: 'health-refresh',
    name: 'refresh issues',
    description: 'Re-read the cluster now instead of waiting for the next poll',
    category: 'Commands',
    run: (c) => {
      if (!c) return;
      void refreshIssues(c, true).then(() => {
        const s = useStore.getState();
        s.addToast?.({ type: 'info', message: 'Issues refreshed' } as any);
      });
    },
  },
];
