import { describe, expect, it } from 'vitest';
import { groupTools } from './aiUtils';

describe('groupTools', () => {
  it('puts core first, then features in their order, reads before writes', () => {
    const g = groupTools([
      { name: 'get_issues', feature: 'health', write: false },
      { name: 'restart_workload', feature: 'actions', write: true },
      { name: 'list_resources', feature: 'core', write: false },
      { name: 'exec_pod', feature: 'actions', write: true },
      { name: 'zz', feature: 'unknown', write: false },
    ]);
    expect(g.map(([f]) => f)).toEqual(['core', 'actions', 'health', 'unknown']);
    expect(g[1][1].map((t) => t.name)).toEqual(['exec_pod', 'restart_workload']);
  });
});
