import { Component, createElement } from 'react';
import { flushSync } from 'react-dom';
import { createRoot, type Root } from 'react-dom/client';
import {
  afterAll,
  beforeAll,
  beforeEach,
  describe,
  expect,
  it,
  vi,
} from 'vitest';

const { error } = vi.hoisted(() => ({ error: vi.fn() }));
vi.mock('../../utils/logger', () => ({ default: { error } }));

import { ViewErrorBoundary } from './ViewErrorBoundary';

// Error boundaries only work in a client render, and these tests have no
// DOM: the few node APIs React DOM uses for a small tree are faked here.
class FakeNode {
  childNodes: FakeNode[] = [];
  parentNode: FakeNode | null = null;
  attributes: Record<string, string> = {};
  style = {};
  nodeValue = '';
  namespaceURI = 'http://www.w3.org/1999/xhtml';
  constructor(
    readonly nodeType: number,
    readonly nodeName: string,
    readonly ownerDocument: any,
  ) {}
  get tagName() {
    return this.nodeName;
  }
  get firstChild() {
    return this.childNodes[0] ?? null;
  }
  appendChild(child: FakeNode) {
    return this.insertBefore(child, null);
  }
  insertBefore(child: FakeNode, before: FakeNode | null) {
    child.parentNode?.removeChild(child);
    child.parentNode = this;
    const i = before ? this.childNodes.indexOf(before) : -1;
    this.childNodes.splice(i < 0 ? this.childNodes.length : i, 0, child);
    return child;
  }
  removeChild(child: FakeNode) {
    this.childNodes = this.childNodes.filter((c) => c !== child);
    child.parentNode = null;
    return child;
  }
  setAttribute(name: string, value: unknown) {
    this.attributes[name] = String(value);
  }
  removeAttribute(name: string) {
    delete this.attributes[name];
  }
  addEventListener() {}
  removeEventListener() {}
  get textContent(): string {
    if (this.nodeType === 3) return this.nodeValue;
    return this.childNodes.map((c) => c.textContent).join('');
  }
  set textContent(text: string) {
    this.childNodes = [];
    if (text) this.appendChild(this.ownerDocument.createTextNode(text));
  }
}

const document = Object.assign(new FakeNode(9, '#document', null), {
  createElement: (tag: string) => new FakeNode(1, tag.toUpperCase(), document),
  createTextNode: (text: string) =>
    Object.assign(new FakeNode(3, '#text', document), { nodeValue: text }),
});

const find = (node: FakeNode, tag: string): FakeNode | undefined =>
  node.nodeName === tag
    ? node
    : node.childNodes.map((c) => find(c, tag)).find(Boolean);

/** Calls the click handler React attached to the element. */
const click = (node: FakeNode) => {
  const key = Object.keys(node).find((k) => k.startsWith('__reactProps$'))!;
  flushSync(() => (node as any)[key].onClick());
};

let failing = true;
let mounts = 0;
class Pods extends Component {
  constructor(props: object) {
    super(props);
    mounts++;
  }
  render() {
    if (failing) throw new Error('cannot read pods of undefined');
    return createElement('b', null, 'pods');
  }
}

let container: FakeNode;
let root: Root;
const render = (resetKey: string) =>
  flushSync(() =>
    root.render(
      createElement(ViewErrorBoundary, {
        resetKey,
        children: createElement(Pods),
      }),
    ),
  );

beforeAll(() => {
  (globalThis as any).window = { HTMLIFrameElement: class {} };
  (globalThis as any).document = document;
  // React reports every caught render error on the console as well.
  vi.spyOn(console, 'error').mockImplementation(() => {});
});

afterAll(() => {
  delete (globalThis as any).window;
  delete (globalThis as any).document;
  vi.restoreAllMocks();
});

beforeEach(() => {
  failing = true;
  mounts = 0;
  error.mockClear();
  root?.unmount();
  container = document.createElement('div');
  root = createRoot(container as any);
});

describe('ViewErrorBoundary', () => {
  it('shows a card instead of a view that throws, and logs the error', () => {
    render('pods');
    expect(container.textContent).toBe(
      'This view ran into an error.Reload view',
    );
    expect(container.firstChild?.attributes.role).toBe('alert');
    expect(error).toHaveBeenCalledWith(
      'View failed to render',
      expect.objectContaining({ error: 'cannot read pods of undefined' }),
    );
  });

  it('renders the view again, freshly mounted, on Reload view', () => {
    render('pods');
    failing = false;
    const before = mounts;
    click(find(container, 'BUTTON')!);
    expect(container.textContent).toBe('pods');
    expect(mounts).toBe(before + 1);
  });

  it('resets once the tab or selection changes', () => {
    render('pods');
    failing = false;
    render('pods');
    expect(container.textContent).toContain('Reload view');
    render('deployments');
    expect(container.textContent).toBe('pods');
  });

  it('leaves a healthy view mounted when the selection changes', () => {
    failing = false;
    render('pods');
    render('deployments');
    expect(container.textContent).toBe('pods');
    expect(mounts).toBe(1);
  });

  it('catches a view that fails in the render that changes the key', () => {
    failing = false;
    render('pods');
    failing = true;
    render('deployments');
    expect(container.textContent).toContain('Reload view');
    // Failing again after a reset is caught again, not left to the root.
    render('nodes');
    expect(container.textContent).toContain('Reload view');
  });
});
