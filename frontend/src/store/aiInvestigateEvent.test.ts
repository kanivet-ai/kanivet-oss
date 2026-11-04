import { describe, expect, it, vi } from 'vitest';

vi.mock('../services/api/ai', () => ({
  aiApi: {
    startInvestigation: vi.fn(async (target: any) => ({ id: 'inv-1', target, status: 'running', started: '', events: [] })),
    getStatus: vi.fn(async () => ({ enabled: true })),
    listAgents: vi.fn(async () => [{ id: 'claude', available: true }]),
    listInvestigations: vi.fn(async () => []),
  },
}));

// The unit tests run in node: give the module a window to listen on.
if (typeof (globalThis as any).window === 'undefined') (globalThis as any).window = new EventTarget();

describe('kanivet:investigate event', () => {
  it('maps an Issues-page request to an investigation target', async () => {
    const { targetFromInvestigateRequest } = await import('./aiStore');
    const t = targetFromInvestigateRequest({
      cluster: 'c1',
      resource: { apiVersion: 'apps/v1', kind: 'Deployment', namespace: 'ns', name: 'web' },
      context: 'CrashLoop: back-off',
      issue: { title: 'CrashLoop' } as any,
    });
    expect(t).toMatchObject({ cluster: 'c1', kind: 'Deployment', apiVersion: 'apps/v1', namespace: 'ns', name: 'web', context: 'CrashLoop: back-off' });
    expect(t.question).toContain('CrashLoop');
  });

  it('starts an investigation when the event is dispatched', async () => {
    const { useAIStore } = await import('./aiStore');
    const { INVESTIGATE_EVENT } = await import('../types/health');
    const spy = vi.fn(async () => {});
    useAIStore.setState({ investigate: spy } as any);
    window.dispatchEvent(new CustomEvent(INVESTIGATE_EVENT, { detail: { cluster: 'c1', resource: { apiVersion: 'v1', kind: 'Pod', name: 'p' } } }));
    expect(spy).toHaveBeenCalledWith(expect.objectContaining({ cluster: 'c1', kind: 'Pod', name: 'p' }));
  });
});
