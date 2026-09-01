import { describe, expect, it } from 'vitest';
import { getClusterStatusPresentation } from './clusterStatusPresentation';

describe('getClusterStatusPresentation', () => {
  it('renders an unprobed status as static neutral unknown', () => {
    expect(getClusterStatusPresentation(undefined)).toEqual({
      className: 'unknown',
      title: 'Status unknown',
      isNotReady: false,
    });
  });

  it('uses loading only while the cluster list is loading', () => {
    expect(getClusterStatusPresentation(undefined, true)).toEqual({
      className: 'loading',
      title: 'Checking...',
      isNotReady: false,
    });
  });

  it('preserves normal status presentation after a probe', () => {
    expect(getClusterStatusPresentation({ name: 'ready', healthy: true, responseTimeMs: 5 })).toMatchObject({
      className: 'online',
      title: 'Connected',
      isNotReady: false,
    });
    expect(getClusterStatusPresentation({ name: 'blocked', healthy: false, responseTimeMs: 5, error: 'Unauthorized' })).toEqual({
      className: 'offline',
      title: 'Unauthorized',
      isNotReady: true,
    });
  });
});
