import { openAccessPage } from './accessIntent';

// Command palette entries ("> who can", "> my permissions", ...).
export const ACCESS_COMMANDS = [
  { id: 'access-who-can', name: 'who can…', description: 'Find the subjects allowed a verb on a resource', category: 'Commands' },
  { id: 'access-my-permissions', name: 'my permissions', description: 'What you can do in this cluster', category: 'Commands' },
  { id: 'access-subject', name: 'subject permissions', description: 'Permission matrix of a service account, user or group', category: 'Commands' },
  { id: 'access-risky-grants', name: 'risky grants', description: 'Wildcards, secret reads, escalation, exec and cluster-admin bindings', category: 'Commands' },
];

const MODES: Record<string, Parameters<typeof openAccessPage>[1]> = {
  'access-who-can': { mode: 'who-can' },
  'access-my-permissions': { mode: 'mine' },
  'access-subject': { mode: 'subject' },
  'access-risky-grants': { mode: 'risks' },
};

/** Runs an access command; false when id is not one. */
export function runAccessCommand(id: string, cluster: string | null): boolean {
  const intent = MODES[id];
  if (!intent) return false;
  void openAccessPage(cluster, intent);
  return true;
}
