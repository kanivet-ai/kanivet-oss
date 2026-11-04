import type { FindingResource, InvestigationEvent } from '../../services/api/ai';
import type { ChatMessage } from '../../services/api/aiChat';
import { refFromCode } from './aiUtils';

/** Removes ```kanivet-patch blocks (rendered as approval cards instead). */
export function stripPatchBlocks(md: string): string {
  return md.replace(/```kanivet-patch[\s\S]*?```/g, '').replace(/```json\s*\{[\s\S]*?"summary"[\s\S]*?\}\s*```\s*$/g, '').trim();
}

function args(input?: string): Record<string, any> {
  if (!input) return {};
  try {
    const v = JSON.parse(input);
    return v && typeof v === 'object' ? v : {};
  } catch {
    return {};
  }
}

const base = (p?: string) => (p ? String(p).split(/[\\/]/).pop() : '');

/** Human label for a tool call: "Reading logs of checkout-7d9f", "Editing values.yaml". */
export function toolVerb(tool: string, input?: string): string {
  const a = args(input);
  const target = a.name ? `${a.kind ? a.kind + ' ' : ''}${a.namespace ? a.namespace + '/' : ''}${a.name}` : a.namespace ? `namespace ${a.namespace}` : '';
  const on = target ? ` ${target}` : '';
  const t = tool.replace(/^mcp__kanivet__/, '');
  switch (t) {
    case 'get_incidents': return `Checking incidents${a.namespace ? ' in ' + a.namespace : ''}`;
    case 'get_issues': return `Checking issues${a.namespace ? ' in ' + a.namespace : ''}`;
    case 'diagnose': return `Diagnosing${on}`;
    case 'get_logs':
    case 'get_workload_logs': return `Reading logs of${on || ' a pod'}`;
    case 'get_events': return `Reading events${on ? ' for' + on : ''}`;
    case 'get_resource': return `Reading${on || ' a resource'}`;
    case 'list_resources': return `Listing ${a.kind || 'resources'}${a.namespace ? ' in ' + a.namespace : ''}`;
    case 'search': return `Searching for "${a.query || a.q || ''}"`;
    case 'get_metrics':
    case 'top_resources': return `Checking usage${on}`;
    case 'audit': return 'Running the audit';
    case 'get_changes': return `Looking at recent changes${on}`;
    case 'get_topology':
    case 'get_related': return `Mapping dependencies${on}`;
    case 'check_network_path': return 'Checking the network path';
    case 'who_can':
    case 'subject_permissions': return 'Checking permissions';
    case 'list_certificates': return 'Checking certificates';
    case 'Read': return `Reading ${base(a.file_path) || 'a file'}`;
    case 'Edit':
    case 'MultiEdit': return `Editing ${base(a.file_path) || 'a file'}`;
    case 'Write': return `Writing ${base(a.file_path) || 'a file'}`;
    case 'Glob': return `Finding files ${a.pattern || ''}`.trim();
    case 'Grep': return `Searching code for "${a.pattern || ''}"`;
    default: return t.replace(/_/g, ' ');
  }
}

export type Step =
  | { kind: 'text'; key: string; text: string }
  | { kind: 'thinking'; key: string; text: string }
  | { kind: 'tool'; key: string; e: InvestigationEvent; result?: InvestigationEvent }
  | { kind: 'status'; key: string; text: string; error?: boolean };

/** Pairs tool calls with their results, in order. */
export function groupEvents(events: InvestigationEvent[] = []): Step[] {
  const out: Step[] = [];
  const open: Extract<Step, { kind: 'tool' }>[] = [];
  for (const e of events) {
    const key = String(e.seq);
    switch (e.type) {
      case 'tool_result': {
        const s = open.shift();
        if (s) s.result = e;
        break;
      }
      case 'tool': {
        const s = { kind: 'tool' as const, key, e };
        open.push(s);
        out.push(s);
        break;
      }
      case 'text':
        out.push({ kind: 'text', key, text: e.text || '' });
        break;
      case 'thinking':
        out.push({ kind: 'thinking', key, text: e.text || '' });
        break;
      default:
        out.push({ kind: 'status', key, text: e.text || '', error: e.type === 'error' });
    }
  }
  return out;
}

/** Resources referenced in a message (findings + `Kind/ns/name` spans + tool args), first seen first. */
export function messageResources(m: ChatMessage, cluster?: string): FindingResource[] {
  const seen = new Set<string>();
  const out: FindingResource[] = [];
  const add = (r: FindingResource | null) => {
    if (!r || !r.kind || !r.name) return;
    const k = `${r.kind}/${r.namespace || ''}/${r.name}`;
    if (seen.has(k)) return;
    seen.add(k);
    out.push({ ...r, cluster: r.cluster || cluster });
  };
  for (const e of m.events || []) {
    if (e.type === 'tool') {
      const a = args(e.input);
      if (a.kind && a.name && typeof a.kind === 'string') add({ kind: a.kind[0].toUpperCase() + a.kind.slice(1), namespace: a.namespace, name: a.name });
    }
  }
  for (const span of (m.text || '').matchAll(/`([^`]+)`/g)) add(refFromCode(span[1]));
  m.findings?.resources?.forEach(add);
  m.findings?.findings?.forEach((f) => f.resources?.forEach(add));
  return out;
}

export function toolCount(m: ChatMessage): number {
  return (m.events || []).filter((e) => e.type === 'tool').length;
}
