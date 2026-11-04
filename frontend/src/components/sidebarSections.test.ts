import { expect, it } from 'vitest';
import { groupSidebar, visibleRoots, NOT_DETECTED_KEY } from './sidebarSections';

const roots = [
  { id: 'overview', section: 'top' },
  { id: 'workloads', section: 'resources' },
  { id: 'changes', section: 'insights' },
  { id: 'finops', section: 'cost' },
  { id: 'helm', section: 'integrations' },
  { id: 'argocd', section: 'integrations', disabled: true },
  { id: 'settings', section: 'bottom' },
];

it('groups roots by section in order and folds undetected integrations', () => {
  const groups = groupSidebar(roots);
  expect(groups.map((g) => g.id)).toEqual(['top', 'resources', 'insights', 'cost', 'integrations', 'bottom']);
  const integrations = groups.find((g) => g.id === 'integrations')!;
  expect(integrations.nodes.map((n) => n.id)).toEqual(['helm']);
  expect(integrations.notDetected.map((n) => n.id)).toEqual(['argocd']);
});

it('keyboard order skips collapsed sections and the closed fold, unless searching', () => {
  const collapsed = new Set(['insights', NOT_DETECTED_KEY]);
  expect(visibleRoots(roots, collapsed, false).map((n) => n.id)).toEqual(['overview', 'workloads', 'finops', 'helm', 'settings']);
  expect(visibleRoots(roots, collapsed, true)).toHaveLength(roots.length);
  // Headerless sections never collapse.
  expect(visibleRoots(roots, new Set(['top']), false)[0].id).toBe('overview');
});
