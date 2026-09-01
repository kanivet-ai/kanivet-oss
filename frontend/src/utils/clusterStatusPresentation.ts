import { ClusterStatus } from '../types';

export type ClusterStatusClass = 'loading' | 'unknown' | 'online' | 'offline';

export interface ClusterStatusPresentation {
  className: ClusterStatusClass;
  title: string;
  isNotReady: boolean;
}

export function getClusterStatusPresentation(
  status: ClusterStatus | undefined,
  isLoading = false,
): ClusterStatusPresentation {
  if (isLoading) {
    return { className: 'loading', title: 'Checking...', isNotReady: false };
  }

  if (!status) {
    return { className: 'unknown', title: 'Status unknown', isNotReady: false };
  }

  if (status.healthy) {
    return { className: 'online', title: 'Connected', isNotReady: false };
  }

  return {
    className: 'offline',
    title: status.error || 'Disconnected',
    isNotReady: true,
  };
}
