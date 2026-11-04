import type { ChatMode, ContextChip } from '../../services/api/aiChat';

/** A resource reference as the feature pages have it. */
export interface AIRef {
  kind: string;
  apiVersion?: string;
  namespace?: string;
  name: string;
}

/**
 * What an AI entry point hands to the chat: the message to pre-seed, the
 * context chips Kanivet gathered, and whether this is a fix (the user then
 * picks "in code" or "in cluster").
 */
export interface AskSeed {
  text: string;
  source: string;
  cluster?: string;
  title?: string;
  chips?: ContextChip[];
  /** 'fix' shows the remediation chooser first; otherwise a plain chat. */
  intent?: 'ask' | 'fix';
  mode?: ChatMode;
  /** The resource a fix targets (code-location suggestions use it). */
  resource?: AIRef & { cluster?: string };
  /** Send right away (default) or leave it in the composer to edit. */
  autoSend?: boolean;
  /** Open in the palette's diagnosis sheet instead of the side panel. */
  surface?: 'panel' | 'sheet';
}

/** The window event any page can dispatch to open the chat. */
export const ASK_AI_EVENT = 'kanivet:ask-ai';

/** Opens the AI chat from anywhere without importing the store. */
export function askAI(seed: AskSeed) {
  window.dispatchEvent(new CustomEvent<AskSeed>(ASK_AI_EVENT, { detail: seed }));
}

export function refLabel(r: AIRef): string {
  return r.namespace ? `${r.kind} ${r.namespace}/${r.name}` : `${r.kind} ${r.name}`;
}

export const clusterChip = (cluster: string): ContextChip => ({ type: 'cluster', label: cluster, cluster });
export const namespaceChip = (cluster: string, namespace: string): ContextChip => ({ type: 'namespace', label: `ns ${namespace}`, cluster, namespace });

export function resourceChip(cluster: string, r: AIRef, includeYaml = true): ContextChip {
  return { type: 'resource', label: refLabel(r), cluster, kind: r.kind, apiVersion: r.apiVersion, namespace: r.namespace, name: r.name, includeYaml };
}

export function textChip(label: string, text: string, type: ContextChip['type'] = 'finding'): ContextChip {
  return { type, label, text };
}

/** Removes chips that duplicate another (same type and target). */
export function dedupeChips(chips: ContextChip[]): ContextChip[] {
  const seen = new Set<string>();
  return chips.filter((c) => {
    const k = [c.type, c.cluster, c.kind, c.namespace, c.name, c.label].join('|');
    if (seen.has(k)) return false;
    seen.add(k);
    return true;
  });
}

// ---- seeds for each entry point -------------------------------------------

interface AuditFindingLike { checkId: string; message: string; container?: string; severity: string; resource: AIRef }
interface AuditCheckLike { id?: string; title: string; explanation: string; fix: string; fixPath?: string }

function checkText(check: AuditCheckLike | undefined, f?: AuditFindingLike): string {
  const lines: string[] = [];
  if (check) lines.push(`Check: ${check.title}`, `Why it matters: ${check.explanation}`);
  if (f) lines.push(`Finding: ${f.container ? f.container + ': ' : ''}${f.message} (severity ${f.severity})`);
  if (check?.fix) lines.push(`Suggested fix snippet${check.fixPath ? ` (at ${check.fixPath})` : ''}:\n${check.fix}`);
  return lines.join('\n');
}

export function auditFixSeed(cluster: string, f: AuditFindingLike, check?: AuditCheckLike): AskSeed {
  return {
    source: 'audit', intent: 'fix', cluster, resource: { ...f.resource, cluster },
    title: `Fix: ${check?.title || f.checkId} on ${f.resource.name}`,
    text: `Fix the audit finding "${check?.title || f.checkId}" on ${refLabel(f.resource)}. Apply the suggested fix adapted to this resource, keeping everything else unchanged.`,
    chips: [clusterChip(cluster), resourceChip(cluster, f.resource), textChip(`Audit: ${check?.title || f.checkId}`, checkText(check, f))],
  };
}

export function auditExplainSeed(cluster: string, f: AuditFindingLike, check?: AuditCheckLike): AskSeed {
  return {
    source: 'audit', intent: 'ask', cluster,
    title: `Explain: ${check?.title || f.checkId}`,
    text: `Explain this audit finding on ${refLabel(f.resource)}: what the risk is in this specific case, whether it is worth fixing, and how.`,
    chips: [clusterChip(cluster), resourceChip(cluster, f.resource), textChip(`Audit: ${check?.title || f.checkId}`, checkText(check, f))],
  };
}

export function auditBulkFixSeed(cluster: string, check: AuditCheckLike, findings: AuditFindingLike[]): AskSeed {
  const list = findings.slice(0, 40).map((f) => `- ${refLabel(f.resource)}${f.container ? ` (container ${f.container})` : ''}: ${f.message}`).join('\n');
  const more = findings.length > 40 ? `\n…and ${findings.length - 40} more` : '';
  return {
    source: 'audit', intent: 'fix', cluster, resource: findings[0] ? { ...findings[0].resource, cluster } : undefined,
    title: `Fix all failing: ${check.title}`,
    text: `Fix every resource failing the audit check "${check.title}" (${findings.length}). Prefer one consistent change (e.g. a shared Helm value or Kustomize patch) over many one-off edits.`,
    chips: [clusterChip(cluster), textChip(`Audit: ${check.title}`, checkText(check)), textChip(`Failing resources (${findings.length})`, list + more, 'text')],
  };
}

interface IssueLike { title: string; message: string; reason?: string; hint?: string; severity: string; resource: AIRef; count?: number; since?: string }

function issueText(i: IssueLike) {
  return [`${i.title} (${i.severity})`, i.message, i.reason ? `Reason: ${i.reason}` : '', i.hint ? `Hint: ${i.hint}` : '', i.count ? `Seen ${i.count}x since ${i.since || '?'}` : '']
    .filter(Boolean).join('\n');
}

export function issueInvestigateSeed(cluster: string, i: IssueLike): AskSeed {
  return {
    source: 'issues', intent: 'ask', cluster, title: `Investigate: ${i.title}`,
    text: `Investigate this issue on ${refLabel(i.resource)}: find the root cause with evidence and tell me how to fix it.`,
    chips: [clusterChip(cluster), resourceChip(cluster, i.resource), textChip(`Issue: ${i.title}`, issueText(i))],
  };
}

export function issueFixSeed(cluster: string, i: IssueLike): AskSeed {
  return {
    ...issueInvestigateSeed(cluster, i), intent: 'fix', resource: { ...i.resource, cluster }, title: `Fix: ${i.title}`,
    text: `Find the root cause of this issue on ${refLabel(i.resource)} and fix it.`,
  };
}

export function resourceAskSeed(cluster: string, r: AIRef, what: 'ask' | 'explain-yaml' | 'optimize' | 'fix' = 'ask'): AskSeed {
  const text = {
    ask: `Tell me about ${refLabel(r)}: is it healthy, and is anything worth my attention?`,
    'explain-yaml': `Explain the YAML of ${refLabel(r)} section by section, in plain language, and point out anything unusual or risky.`,
    optimize: `Review the CPU and memory requests/limits of ${refLabel(r)} against its actual usage and recommend better values.`,
    fix: `Fix what is wrong with ${refLabel(r)}.`,
  }[what];
  return {
    source: 'inspector', intent: what === 'fix' || what === 'optimize' ? (what === 'fix' ? 'fix' : 'ask') : 'ask', cluster,
    resource: { ...r, cluster }, autoSend: what !== 'ask',
    title: `${{ ask: 'Ask', 'explain-yaml': 'Explain YAML', optimize: 'Optimize', fix: 'Fix' }[what]}: ${r.name}`,
    text, chips: [clusterChip(cluster), resourceChip(cluster, r)],
  };
}

export function deprecatedApiSeed(cluster: string, r: AIRef, info: { deprecatedApi: string; replacement?: string; removedIn?: string; notes?: string }): AskSeed {
  return {
    source: 'upgrade', intent: 'fix', cluster, resource: { ...r, cluster }, title: `Migrate ${r.kind} ${r.name}`,
    text: `Migrate ${refLabel(r)} off the deprecated API ${info.deprecatedApi}${info.replacement ? ` to ${info.replacement}` : ''}. Update the manifest (or chart/Kustomize source) and any field changes the new version needs.`,
    chips: [clusterChip(cluster), resourceChip(cluster, r), textChip('Deprecated API', [`API: ${info.deprecatedApi}`, info.replacement ? `Replacement: ${info.replacement}` : '', info.removedIn ? `Removed in Kubernetes ${info.removedIn}` : '', info.notes || ''].filter(Boolean).join('\n'))],
  };
}

export function certFixSeed(cluster: string, r: AIRef, problem: string, detail: string): AskSeed {
  return {
    source: 'certs', intent: 'fix', cluster, resource: { ...r, cluster }, title: `Fix certificate: ${r.name}`,
    text: `Fix this TLS certificate problem on ${refLabel(r)}: ${problem}. Work out whether the Ingress/Gateway hosts, the cert-manager Certificate or the issuer needs changing.`,
    chips: [clusterChip(cluster), resourceChip(cluster, r, r.kind !== 'Secret'), textChip('Certificate', detail)],
  };
}

export function networkSeed(cluster: string, from: string, to: string, verdict: string, detail: string, kind: 'explain' | 'policy'): AskSeed {
  return {
    source: 'network', intent: kind === 'policy' ? 'fix' : 'ask', cluster,
    title: kind === 'policy' ? `NetworkPolicy: ${from} → ${to}` : `Why ${verdict}: ${from} → ${to}`,
    text: kind === 'policy'
      ? `Write the least-privilege NetworkPolicy that allows ${from} to reach ${to} (and nothing more), consistent with the policies already in the namespace.`
      : `Explain why traffic from ${from} to ${to} is ${verdict}: which policies decide it and what would change it.`,
    chips: [clusterChip(cluster), textChip(`Path check: ${from} → ${to}`, detail)],
  };
}

export function accessSeed(cluster: string, subject: string, detail: string, kind: 'explain' | 'tighten', role?: AIRef): AskSeed {
  return {
    source: 'access', intent: kind === 'tighten' ? 'fix' : 'ask', cluster, resource: role ? { ...role, cluster } : undefined,
    title: kind === 'tighten' ? `Tighten: ${role?.name || subject}` : `Explain permissions: ${subject}`,
    text: kind === 'tighten'
      ? `Tighten ${role ? refLabel(role) : `the roles bound to ${subject}`} to least privilege: remove wildcard and unused verbs/resources, keep what workloads actually need.`
      : `Explain what ${subject} can do in this cluster, in plain language, and flag anything risky.`,
    chips: [clusterChip(cluster), ...(role ? [resourceChip(cluster, role)] : []), textChip(`Access: ${subject}`, detail)],
  };
}

export function rightsizingSeed(cluster: string, r: AIRef, detail: string, source = 'rightsizing'): AskSeed {
  return {
    source, intent: 'fix', cluster, resource: { ...r, cluster }, title: `Apply recommendation: ${r.name}`,
    text: `Apply this resource recommendation to ${refLabel(r)}: set the requests/limits to the recommended values where they are defined.`,
    chips: [clusterChip(cluster), resourceChip(cluster, r), textChip('Recommendation', detail)],
  };
}

export function compareSeed(cluster: string, left: string, right: string, diffSummary: string, kind: 'explain' | 'reconcile'): AskSeed {
  return {
    source: 'compare', intent: kind === 'reconcile' ? 'fix' : 'ask', cluster,
    title: kind === 'reconcile' ? `Reconcile drift: ${left} ↔ ${right}` : `Explain differences: ${left} ↔ ${right}`,
    text: kind === 'reconcile'
      ? `Reconcile the drift between ${left} and ${right}: decide which side is intended and make the other match.`
      : `Explain the meaningful differences between ${left} and ${right} and which of them matter.`,
    chips: [clusterChip(cluster), textChip(`Diff ${left} ↔ ${right}`, diffSummary, 'yaml')],
  };
}

export function changeSeed(cluster: string, r: AIRef, summary: string, detail: string): AskSeed {
  return {
    source: 'changes', intent: 'ask', cluster, title: `Change: ${r.name}`,
    text: `What did this change to ${refLabel(r)} (${summary}) actually change, and could it have caused any current incident or errors?`,
    chips: [clusterChip(cluster), resourceChip(cluster, r, false), textChip(`Change: ${summary}`, detail, 'yaml')],
  };
}

// ---- command palette: does the input read like a question? -----------------

const QUESTION_STARTS = [
  'why', 'what', "what's", 'whats', 'how', 'is', 'are', 'can', 'could', 'does', 'do', 'did', 'which', 'where', 'when', 'who',
  'should', 'explain', 'show me', 'find', 'tell me', 'help', 'diagnose', 'investigate', 'summarize', 'summarise', 'compare',
];

/**
 * Cheap local heuristic: true when palette input looks like a question for
 * the AI rather than a resource search or a ">" command.
 */
export function looksLikeQuestion(raw: string): boolean {
  const s = raw.trim();
  if (s.length < 3 || s.startsWith('>') || s.startsWith('/') || s.startsWith(':')) return false;
  const lower = s.toLowerCase();
  // Structured search syntax stays a search.
  if (/^[\w.-]+:\S/.test(lower) && !lower.includes(' ')) return false; // kind:pod, ns:prod
  if (/^[\w.-]+=[\w.-]+$/.test(lower)) return false; // app=web
  if (/^[\w.-]+\/[\w.-]+$/.test(lower)) return false; // ns/name
  if (s.includes('?')) return true;
  const words = lower.split(/\s+/).filter(Boolean);
  if (words.length < 2) return false;
  for (const q of QUESTION_STARTS) {
    if (lower === q || lower.startsWith(q + ' ')) return true;
  }
  // Several plain words that do not look like identifiers.
  const plain = words.filter((w) => /^[a-z']+$/.test(w)).length;
  return words.length >= 4 && plain >= 3;
}
