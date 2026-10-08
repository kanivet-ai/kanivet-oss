import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import ResourceControlsBar from './ResourceControlsBar';
import type { ListSync } from '../../store/types';

const render = (totalCount: number, listSync?: ListSync | null) =>
  renderToStaticMarkup(
    createElement(ResourceControlsBar, { totalCount, listSync, showNamespaces: false, showBulkActions: false, showSearch: false }),
  );
const count = (totalCount: number, listSync?: ListSync | null) =>
  render(totalCount, listSync).replace(/<!-- -->/g, '').replace(/<[^>]+>/g, '');

describe('ResourceControlsBar item count', () => {
  it('shows the count alone once the list is complete', () => {
    expect(count(2369)).toBe('2369 items');
    expect(count(1, null)).toBe('1 item');
  });

  it('says the list is still loading, with how far along when the size is known', () => {
    expect(count(25, { loaded: 25, total: 2369 })).toBe('25 items · loading 1%');
    expect(count(1200, { loaded: 1200, total: 2369 })).toBe('1200 items · loading 50%');
    expect(count(40, { loaded: 40 })).toBe('40 items · loading…');
  });

  it('never reads as finished before the list is', () => {
    // Pods created during the listing can take it past the size first given.
    expect(count(2400, { loaded: 2400, total: 2369 })).toBe('2400 items · loading 99%');
    expect(count(3, { loaded: 3, total: 3 })).toBe('3 items · loading 99%');
  });

  it('marks the count busy for assistive technology while loading', () => {
    expect(render(1, { loaded: 1 })).toContain('aria-busy="true"');
    expect(render(1)).toContain('aria-busy="false"');
  });
});
