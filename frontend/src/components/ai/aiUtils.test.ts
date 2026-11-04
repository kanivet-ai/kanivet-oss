import { describe, expect, it } from 'vitest';
import {
  apiVersionFor,
  collectResources,
  formatElapsed,
  maskToken,
  mergeInvestigation,
  parseInline,
  parseMarkdown,
  prettyClient,
  refFromCode,
  timeAgo,
} from './aiUtils';
import type { Investigation } from '../../services/api/ai';

const base: Investigation = {
  id: 'i',
  agent: 'Claude Code',
  target: { cluster: 'kind-dev', kind: 'Deployment', namespace: 'prod', name: 'web' },
  status: 'running',
  started: new Date(0).toISOString(),
  events: [
    { seq: 1, time: 0, type: 'status', text: 'start' },
    { seq: 2, time: 0, type: 'tool', tool: 'diagnose' },
  ],
  toolCalls: 1,
};

describe('mergeInvestigation', () => {
  it('appends only unseen events in order', () => {
    const merged = mergeInvestigation(base, {
      ...base,
      status: 'done',
      events: [
        { seq: 2, time: 0, type: 'tool', tool: 'diagnose' },
        { seq: 3, time: 0, type: 'tool_result', ok: true },
      ],
    });
    expect(merged.events.map((e) => e.seq)).toEqual([1, 2, 3]);
    expect(merged.status).toBe('done');
  });
  it('takes the snapshot when nothing is held', () => {
    expect(mergeInvestigation(undefined, base)).toBe(base);
  });
});

describe('collectResources', () => {
  it('dedupes resources and fills the cluster', () => {
    const inv: Investigation = {
      ...base,
      findings: {
        summary: 's',
        resources: [{ kind: 'Pod', namespace: 'prod', name: 'web-1' }],
        findings: [{ title: 't', resources: [{ kind: 'Pod', namespace: 'prod', name: 'web-1' }, { kind: 'Node', name: 'n1' }] }],
      },
    };
    const r = collectResources(inv);
    expect(r).toHaveLength(2);
    expect(r[0].cluster).toBe('kind-dev');
  });
});

describe('helpers', () => {
  it('infers apiVersion for well-known kinds', () => {
    expect(apiVersionFor({ kind: 'Deployment' })).toBe('apps/v1');
    expect(apiVersionFor({ kind: 'Widget', apiVersion: 'x.io/v1' })).toBe('x.io/v1');
  });
  it('formats elapsed and relative times', () => {
    expect(formatElapsed(4200)).toBe('4s');
    expect(formatElapsed(125000)).toBe('2m 05s');
    expect(timeAgo(1000, 3000)).toBe('just now');
    expect(timeAgo(0, 120000)).toBe('2m ago');
  });
  it('masks tokens', () => {
    const t = 'kmcp_0123456789abcdef0123456789';
    const m = maskToken(t);
    expect(m.startsWith('kmcp_0123')).toBe(true);
    expect(m.endsWith('6789')).toBe(true);
    expect(m).not.toContain('abcdef');
  });
  it('names agents', () => {
    expect(prettyClient('claude-code 2.1.0')).toBe('Claude Code');
    expect(prettyClient('my-agent 1')).toBe('my-agent');
  });
});

describe('markdown', () => {
  it('parses headings, lists, code and paragraphs', () => {
    const blocks = parseMarkdown('## Summary\nweb is down\nbecause\n\n- a\n- b\n\n1. one\n```\ncode\n```');
    expect(blocks.map((b) => b.type)).toEqual(['h', 'p', 'ul', 'ol', 'code']);
    expect(blocks[1]).toEqual({ type: 'p', text: 'web is down because' });
  });
  it('splits inline code and bold', () => {
    expect(parseInline('see `Pod/prod/web-1` **now**')).toEqual([
      { type: 'text', text: 'see ' },
      { type: 'code', text: 'Pod/prod/web-1' },
      { type: 'text', text: ' ' },
      { type: 'bold', text: 'now' },
    ]);
  });
  it('recognises resource references in code spans', () => {
    expect(refFromCode('Pod/prod/web-1')).toEqual({ kind: 'Pod', namespace: 'prod', name: 'web-1' });
    expect(refFromCode('Node/ip-10-0-0-1.ec2.internal')).toEqual({ kind: 'Node', namespace: undefined, name: 'ip-10-0-0-1.ec2.internal' });
    expect(refFromCode('kubectl get pods')).toBeNull();
  });
});
