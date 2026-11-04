import { openNetworkPage } from './openNetwork';
import { parseReachQuery } from './networkUtils';

/** Entries for the palette's "> " command list. */
export const NETWORK_COMMANDS = [
  { id: 'can-reach', name: 'can X reach Y?', description: 'Check a network path between two workloads', category: 'Network' },
  { id: 'network', name: 'open network', description: 'Path checker, policy coverage and traffic', category: 'Network' },
  { id: 'network-coverage', name: 'network policy coverage', description: 'Which pods NetworkPolicies isolate', category: 'Network' },
];

/** A palette result for typed text like "can web reach api:80", or null. */
export function reachCommandResult(text: string) {
  const q = parseReachQuery(text);
  if (!q) return null;
  const dst = q.destination.ip || q.destination.name;
  return {
    resource: {
      id: 'cmd:can-reach-run',
      name: `Can ${q.source.name} reach ${dst}${q.port ? `:${q.port}` : ''}?`,
      kind: 'Command',
      cluster: '',
      category: 'Network',
      query: text.trim().replace(/^>\s*/, ''),
    },
  };
}

/**
 * Runs a network palette command. Returns 'prime' when the palette should
 * prefill "can " and stay open, 'done' when it ran, null when not ours.
 */
export function runNetworkCommand(id: string, resource: any, cluster: string | null | undefined): 'prime' | 'done' | null {
  switch (id) {
    case 'can-reach':
      return 'prime';
    case 'can-reach-run':
      if (cluster) void openNetworkPage(cluster, { view: 'path', query: resource?.query });
      return 'done';
    case 'network':
      if (cluster) void openNetworkPage(cluster);
      return 'done';
    case 'network-coverage':
      if (cluster) void openNetworkPage(cluster, { view: 'coverage' });
      return 'done';
  }
  return null;
}
