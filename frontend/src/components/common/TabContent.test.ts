import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';

vi.mock('../../store', () => ({ useStore: (select: any) => select({ openBottomTab: () => {} }) }));
vi.mock('../../services/api', () => ({ default: { getResourceDetails: vi.fn(), checkCrossplaneResource: vi.fn() } }));
vi.mock('../detailView/ResourceDetailView', () => ({ default: () => createElement('div', { className: 'full-view' }) }));
vi.mock('../ResourceDetailSkeleton', () => ({ default: () => createElement('div', { className: 'skeleton' }) }));
vi.mock('../ContainerSelector', () => ({ default: () => null }));
vi.mock('../icons/CrossplaneIcon', () => ({ default: () => null }));
vi.mock('./Tooltip', () => ({ Tooltip: ({ children }: any) => children }));
vi.mock('../../hooks/useResourceNavigation', () => ({ default: () => ({ navigateToLink: () => {} }) }));
vi.mock('../MonacoEditor', () => ({ preloadEditor: () => {} }));
vi.mock('../../utils/lazyView', () => ({ lazyView: () => () => null }));
vi.mock('../DetailView.css', () => ({}));
vi.mock('./TabContent.css', () => ({}));

const { default: TabContent, deletedDescription } = await import('./TabContent');

const render = (tab: any, isDeleted?: boolean) => renderToStaticMarkup(createElement(TabContent, { tab, isDeleted }));
const placeholder = { kind: 'Pod', metadata: { name: 'api-0', namespace: 'ns' } };
const full = { ...placeholder, spec: { containers: [{ name: 'app' }] } };

describe('TabContent states', () => {
  it('shows a deleted object that never loaded as deleted, not as a skeleton that never finishes', () => {
    const html = render({ cluster: 'c', item: placeholder }, true);
    expect(html).toContain('This resource no longer exists');
    expect(html).toContain(deletedDescription(placeholder));
    expect(html).not.toContain('skeleton');
  });

  it('keeps the last copy of a deleted object readable under the banner', () => {
    const html = render({ cluster: 'c', item: full }, true);
    expect(html).toContain('This resource no longer exists');
    expect(html).toContain('full-view');
  });

  it('says when the object shown was recreated', () => {
    const html = render({ cluster: 'c', item: full, isRecreated: true });
    expect(html).toContain('deleted and created again');
  });

  it('describes the deleted object by kind, name and namespace', () => {
    expect(deletedDescription(placeholder)).toBe('Pod api-0 in ns was deleted.');
    expect(deletedDescription({ name: 'n1' })).toBe('Resource n1 was deleted.');
  });
});
