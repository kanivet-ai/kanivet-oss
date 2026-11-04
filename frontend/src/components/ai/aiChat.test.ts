import { describe, expect, it } from 'vitest';
import { auditBulkFixSeed, auditFixSeed, dedupeChips, issueFixSeed, looksLikeQuestion, paletteAskPosition, resourceAskSeed } from './aiContext';
import { groupEvents, messageResources, stripPatchBlocks, toolVerb } from './chatUtils';

describe('looksLikeQuestion (palette Ask AI heuristic)', () => {
  const cases: [string, boolean][] = [
    // questions
    ['why is checkout crashing?', true],
    ['why is checkout crashing', true],
    ['what changed in orbitpay today', true],
    ['how do I scale web', true],
    ['is redis healthy', true],
    ['are any nodes under pressure', true],
    ['does payments have a PDB', true],
    ['which pods restart the most', true],
    ['show me failing jobs', true],
    ['find pods without limits', true],
    ['explain the ingress for shop', true],
    ['pods?', true],
    ['the checkout service keeps timing out', true],
    // plain resource searches stay searches
    ['nginx', false],
    ['checkout-7d9f', false],
    ['kube-system', false],
    ['deploy web', false],
    ['pod nginx-abc', false],
    ['kind:pod', false],
    ['app=web', false],
    ['prod/web', false],
    ['ns:prod web', false],
    // commands and tiny input
    ['> restart deployment', false],
    ['>why', false],
    ['ab', false],
    ['', false],
  ];
  it.each(cases)('%s → %s', (input, want) => {
    expect(looksLikeQuestion(input)).toBe(want);
  });
});

describe('paletteAskPosition', () => {
  it('leads for questions, follows the network intent, never hijacks commands', () => {
    expect(paletteAskPosition('why is web down?', [], false)).toBe('first');
    expect(paletteAskPosition('nginx', [], false)).toBeNull();
    expect(paletteAskPosition('> show topology', [], false)).toBeNull();
    expect(paletteAskPosition('why is web down?', [], true)).toBeNull();
    const reach = [{ resource: { id: 'cmd:can-reach-run' } }];
    expect(paletteAskPosition('can web reach db on 5432?', reach, false)).toBe('last');
  });
});

describe('seeds', () => {
  const f = { checkId: 'no-limits', severity: 'warning', message: 'no memory limit', container: 'app', resource: { kind: 'Deployment', apiVersion: 'apps/v1', namespace: 'shop', name: 'web' } };
  const check = { title: 'Memory limits', explanation: 'Unbounded memory', fix: 'resources:\n  limits:\n    memory: 256Mi', fixPath: 'spec.template.spec.containers[]' };

  it('audit fix seeds carry the resource, the finding and the fix snippet', () => {
    const s = auditFixSeed('kind-dev', f, check);
    expect(s.intent).toBe('fix');
    expect(s.resource).toMatchObject({ kind: 'Deployment', name: 'web', cluster: 'kind-dev' });
    const res = s.chips!.find((c) => c.type === 'resource')!;
    expect(res.includeYaml).toBe(true);
    const finding = s.chips!.find((c) => c.type === 'finding')!;
    expect(finding.text).toContain('memory: 256Mi');
    expect(finding.text).toContain('app: no memory limit');
  });

  it('bulk fix lists every failing resource', () => {
    const s = auditBulkFixSeed('c', check, [f, { ...f, resource: { ...f.resource, name: 'api' } }]);
    expect(s.text).toContain('(2)');
    expect(s.chips!.some((c) => c.text?.includes('shop/api'))).toBe(true);
  });

  it('issue fix and inspector seeds', () => {
    const s = issueFixSeed('c', { title: 'CrashLoop', message: 'back-off', severity: 'critical', resource: { kind: 'Pod', namespace: 'p', name: 'x' } });
    expect(s.intent).toBe('fix');
    expect(resourceAskSeed('c', { kind: 'Pod', name: 'x' }, 'ask').autoSend).toBe(false);
    expect(resourceAskSeed('c', { kind: 'Pod', name: 'x' }, 'explain-yaml').autoSend).toBe(true);
  });

  it('dedupes chips', () => {
    const a = { type: 'cluster' as const, label: 'c', cluster: 'c' };
    expect(dedupeChips([a, { ...a }])).toHaveLength(1);
  });
});

describe('chat utils', () => {
  const events = [
    { seq: 1, time: 1000, type: 'text' as const, text: 'Looking.' },
    { seq: 2, time: 1100, type: 'tool' as const, tool: 'get_logs', input: '{"kind":"Pod","namespace":"shop","name":"checkout-7d9f"}' },
    { seq: 3, time: 1900, type: 'tool' as const, tool: 'Edit', input: '{"file_path":"/r/charts/web/values.yaml"}' },
    { seq: 4, time: 2300, type: 'tool_result' as const, ok: true },
    { seq: 5, time: 2400, type: 'tool_result' as const, ok: false },
  ];

  it('pairs tool calls with results in order', () => {
    const steps = groupEvents(events);
    expect(steps.map((s) => s.kind)).toEqual(['text', 'tool', 'tool']);
    const [, a, b] = steps as any[];
    expect(a.result.seq).toBe(4);
    expect(b.result.ok).toBe(false);
  });

  it('labels tool calls in plain language', () => {
    expect(toolVerb('get_logs', events[1].input)).toBe('Reading logs of Pod shop/checkout-7d9f');
    expect(toolVerb('mcp__kanivet__get_issues', '{"namespace":"orbitpay"}')).toBe('Checking issues in orbitpay');
    expect(toolVerb('Edit', events[2].input)).toBe('Editing values.yaml');
  });

  it('collects referenced resources', () => {
    const refs = messageResources({ id: 'm', role: 'assistant', time: '', text: 'See `Deployment/shop/web` and `Service/shop/web`.', events }, 'c');
    expect(refs.map((r) => `${r.kind}/${r.name}`)).toEqual(['Pod/checkout-7d9f', 'Deployment/web', 'Service/web']);
    expect(refs[0].cluster).toBe('c');
  });

  it('strips patch blocks from displayed text', () => {
    expect(stripPatchBlocks('Fix it.\n```kanivet-patch\n{"kind":"X"}\n```')).toBe('Fix it.');
  });
});
