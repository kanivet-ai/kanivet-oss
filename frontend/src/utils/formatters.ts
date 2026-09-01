export const formatAge = (timestamp: string): string => {
  if (!timestamp) return '-';

  const date = new Date(timestamp);
  const now = new Date();
  const diff = now.getTime() - date.getTime();

  const seconds = Math.floor(diff / 1000);
  const minutes = Math.floor(seconds / 60);
  const hours = Math.floor(minutes / 60);
  const days = Math.floor(hours / 24);

  if (days > 0) return `${days}d`;
  if (hours > 0) return `${hours}h`;
  if (minutes > 0) return `${minutes}m`;
  return `${seconds}s`;
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

      if (item.unschedulable) {
        return `${status} (Cordoned)`;
      }

      return status;
    }
  }

  return '-';
};
