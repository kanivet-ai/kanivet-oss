import { describe, expect, it, vi } from 'vitest';

vi.mock('../../../services/api', () => ({ default: { getVClusterHostPod: vi.fn() } }));
vi.mock('../../../store', () => ({ useStore: { getState: () => ({}) } }));
vi.mock('../../../hooks/useResourceNavigation', () => ({ default: () => ({ navigateToResource: () => {} }) }));
vi.mock('../../common/PropertyRow', () => ({ default: () => null }));

const { hostPodUnresolved } = await import('./VClusterHostRows');

const hostPod = (nodeName: string) => ({ host: 'host', namespace: 'vc-ns', name: 'api-0-x-default-x-vc', nodeName });

describe('hostPodUnresolved', () => {
  it('keeps looking while the host pod is not on a node, or not there yet', () => {
    expect(hostPodUnresolved(null, null)).toBe(true);
    expect(hostPodUnresolved(hostPod(''), null)).toBe(true);
    expect(hostPodUnresolved(null, 'Not synced to host')).toBe(true);
    expect(hostPodUnresolved(null, 'Unavailable')).toBe(true);
  });

  it('stops once the host pod is scheduled', () => {
    expect(hostPodUnresolved(hostPod('node-a'), null)).toBe(false);
  });
});
