export const formatAge = (timestamp: string): string => {
  if (!timestamp) return '-';
  return formatAgeSince(new Date(timestamp).getTime(), Date.now());
};

// The age at `now` of something created at `time`, both in epoch milliseconds.
export const formatAgeSince = (time: number, now: number): string => {
  const diff = now - time;

  const seconds = Math.floor(diff / 1000);
  const minutes = Math.floor(seconds / 60);
  const hours = Math.floor(minutes / 60);
  const days = Math.floor(hours / 24);

  if (days > 0) return `${days}d`;
  if (hours > 0) return `${hours}h`;
  if (minutes > 0) return `${minutes}m`;
  return `${seconds}s`;
};

// Node conditions that are healthy when False, so a node can be Ready and
// still in trouble.
const NODE_PROBLEM_CONDITIONS = ['MemoryPressure', 'DiskPressure', 'PIDPressure', 'NetworkUnavailable'];

// What a node's Ready status hides: cordoned, under pressure, or being
// removed (Karpenter and the cluster autoscaler delete drained nodes).
const nodeStatusFlags = (item: any): string[] => {
  const flags: string[] = [];
  if (item.unschedulable) flags.push('Cordoned');
  for (const c of item.conditions) {
    if (c?.status === 'True' && NODE_PROBLEM_CONDITIONS.includes(c.type)) flags.push(c.type);
  }
  if (item.deletionTimestamp) flags.push('Terminating');
  return flags;
};

export const formatStatus = (item: any): string => {
  if (item.phase) return item.phase;

  if (typeof item.status === 'string' && item.chart) {
    return item.status;
  }

  if (item.conditions && Array.isArray(item.conditions)) {
    const readyCondition = item.conditions.find((c: any) => c.type === 'Ready');
    if (readyCondition) {
      const status = readyCondition.status === 'True' ? 'Ready' : 'Not Ready';
      const kind = String(item.kind || '').toLowerCase();
      const flags =
        kind === 'node' || kind === 'nodes'
          ? nodeStatusFlags(item)
          : item.unschedulable
            ? ['Cordoned']
            : [];

      return flags.length > 0 ? `${status} (${flags.join(', ')})` : status;
    }
  }

  return '-';
};
