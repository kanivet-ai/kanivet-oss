export const CLUSTER_STATUS_WINDOW_SIZE = 8;

export async function runInWindows<T>(items: T[], fn: (window: T[]) => Promise<void>, size = CLUSTER_STATUS_WINDOW_SIZE) {
  for (let i = 0; i < items.length; i += size) await fn(items.slice(i, i + size));
}
