import { createElement } from 'react';
import { renderToString } from 'react-dom/server';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

// The prefetch queue only needs requestIdleCallback; give the node environment
// one whose callbacks the tests run by hand.
const idle: Array<() => void> = [];
const runIdle = async () => {
  const callback = idle.shift();
  callback?.();
  await new Promise((resolve) => setTimeout(resolve, 0));
};

beforeEach(() => {
  idle.length = 0;
  (globalThis as any).window = {
    requestIdleCallback: (callback: () => void) => idle.push(callback),
    cancelIdleCallback: () => {},
  };
  vi.resetModules();
});

afterEach(() => {
  delete (globalThis as any).window;
});

const view = (name: string) => ({
  default: () => createElement('b', null, name),
});

describe('lazyView', () => {
  it('renders the fallback until the chunk is in, then the view itself', async () => {
    const { lazyView } = await import('./lazyView');
    const Pods = lazyView(
      async () => view('pods'),
      createElement('i', null, 'skeleton'),
      { idlePrefetch: false },
    );

    expect(renderToString(createElement(Pods))).toContain('<i>skeleton</i>');
    await Pods.preload();
    const html = renderToString(createElement(Pods));
    expect(html).toContain('<b>pods</b>');
    expect(html).not.toContain('skeleton');
  });

  it('loads the chunk again after a failed load instead of keeping the error', async () => {
    const { lazyView } = await import('./lazyView');
    const load = vi
      .fn()
      .mockRejectedValueOnce(
        new Error('Failed to fetch dynamically imported module'),
      )
      .mockResolvedValue(view('logs'));
    const Logs = lazyView(load, null, { idlePrefetch: false });

    await expect(Logs.preload()).rejects.toThrow(
      'Failed to fetch dynamically imported module',
    );
    await Logs.preload();

    expect(load).toHaveBeenCalledTimes(2);
    expect(renderToString(createElement(Logs))).toContain('<b>logs</b>');
  });

  it('prefetches one chunk per idle slot once started, including views defined later', async () => {
    const { lazyView, prefetchLazyViewsWhenIdle } = await import('./lazyView');
    const loads: string[] = [];
    const chunk = (name: string) => async () => {
      loads.push(name);
      return view(name);
    };
    lazyView(chunk('palette'));
    lazyView(chunk('finops'));
    lazyView(chunk('monaco'), null, { idlePrefetch: false });
    expect(idle).toHaveLength(0);

    prefetchLazyViewsWhenIdle();
    expect(loads).toEqual([]);
    await runIdle();
    expect(loads).toEqual(['palette']);
    // A view defined inside a chunk that has just loaded joins the queue.
    lazyView(chunk('evidence'));
    await runIdle();
    await runIdle();
    await runIdle();

    expect(loads).toEqual(['palette', 'finops', 'evidence']);
    expect(idle).toHaveLength(0);
  });
});
