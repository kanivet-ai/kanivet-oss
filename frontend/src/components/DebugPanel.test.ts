import { createElement } from 'react';
import { renderToString } from 'react-dom/server';
import { afterEach, describe, expect, it, vi } from 'vitest';

afterEach(() => {
  vi.unstubAllEnvs();
  vi.restoreAllMocks();
  delete (globalThis as any).window;
  delete (globalThis as any).localStorage;
});

describe('DebugPanel', () => {
  it('renders nothing in production, without running its hooks', async () => {
    vi.stubEnv('NODE_ENV', 'production');
    (globalThis as any).window = {};
    const getItem = vi.fn(() => null);
    (globalThis as any).localStorage = { getItem, setItem: vi.fn() };
    const toLocaleTimeString = vi.spyOn(Date.prototype, 'toLocaleTimeString');
    const { default: DebugPanel } = await import('./DebugPanel');

    expect(renderToString(createElement(DebugPanel, { treeData: [] }))).toBe(
      '',
    );
    expect(getItem).not.toHaveBeenCalled();
    expect(toLocaleTimeString).not.toHaveBeenCalled();
  });
});
