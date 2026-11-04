import type { CompareRef, CompareSource } from '../types/compare';

export const splitApiVersion = (apiVersion: string): { group: string; version: string } => {
  if (!apiVersion) return { group: '', version: 'v1' };
  const i = apiVersion.indexOf('/');
  return i < 0 ? { group: '', version: apiVersion } : { group: apiVersion.slice(0, i), version: apiVersion.slice(i + 1) };
};

/** The ref of an object as the inspector holds it (apiVersion, kind, metadata). */
export const refFromObject = (obj: any, cluster: string, source: CompareSource = 'live'): CompareRef => {
  const { group, version } = splitApiVersion(obj?.apiVersion || '');
  return {
    cluster,
    group,
    version,
    kind: obj?.kind || '',
    namespace: obj?.metadata?.namespace || '',
    name: obj?.metadata?.name || '',
    source,
  };
};

/** The ref of a resource-list row, given the list's resource definition. */
export const refFromListItem = (item: any, def: any, cluster: string): CompareRef => ({
  cluster,
  group: def?.group || '',
  version: def?.version || 'v1',
  kind: def?.kind || item?.kind || '',
  namespace: item?.namespace ?? item?.metadata?.namespace ?? '',
  name: item?.name ?? item?.metadata?.name ?? '',
  source: 'live',
});

export const isCompleteRef = (r: Partial<CompareRef> | null | undefined): r is CompareRef =>
  !!(r && r.cluster && r.kind && r.name && r.version);

export const refKey = (r: CompareRef | null | undefined): string =>
  r ? [r.cluster, r.group, r.version, r.kind, r.namespace || '', r.name, r.source || 'live'].join('|') : '';

export const sourceLabel = (s: CompareSource | undefined): string =>
  s === 'last-applied' ? 'Last applied' : s === 'helm' ? 'Helm manifest' : 'Live';

/** "prod / default / Deployment web" style short label. */
export const refShortLabel = (r: CompareRef): string =>
  `${r.kind} ${r.namespace ? `${r.namespace}/` : ''}${r.name}`;

/**
 * Split a backend change path (spec.template.spec.containers[app].image,
 * metadata.labels["app.kubernetes.io/name"]) into segments; list segments
 * keep their brackets.
 */
export const splitChangePath = (path: string): string[] => {
  const out: string[] = [];
  let cur = '';
  let depth = 0;
  let inQuote = false;
  for (let i = 0; i < path.length; i++) {
    const ch = path[i];
    if (inQuote) {
      cur += ch;
      if (ch === '\\') { cur += path[++i] ?? ''; continue; }
      if (ch === '"') inQuote = false;
      continue;
    }
    if (ch === '"') { inQuote = true; cur += ch; continue; }
    if (ch === '[') {
      if (depth === 0 && cur) { out.push(cur); cur = ''; }
      depth++;
      cur += ch;
      continue;
    }
    if (ch === ']') {
      depth--;
      cur += ch;
      if (depth === 0) { out.push(cur); cur = ''; }
      continue;
    }
    if (ch === '.' && depth === 0) {
      if (cur) out.push(cur);
      cur = '';
      continue;
    }
    cur += ch;
  }
  if (cur) out.push(cur);
  return out;
};

const unquote = (seg: string): string => {
  const inner = seg.slice(1, -1);
  if (inner.startsWith('"')) {
    try { return JSON.parse(inner); } catch { return inner.slice(1, -1); }
  }
  return inner;
};

const escapeRe = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

const indentOf = (line: string): number => line.length - line.trimStart().length;

const yamlKeyMatches = (trimmed: string, key: string): boolean => {
  const t = trimmed.startsWith('- ') ? trimmed.slice(2) : trimmed;
  return t === `${key}:` || t.startsWith(`${key}: `) || t.startsWith(`"${key}":`) || t.startsWith(`'${key}':`);
};

/**
 * The 1-based line of `path` in YAML produced by the backend (2-space indent,
 * block style), or the line of its deepest ancestor found; 0 when nothing
 * matches. Good enough to scroll a diff to a summary card's change.
 */
export const lineForPath = (yaml: string, path: string): number => {
  if (!yaml || !path) return 0;
  const lines = yaml.split('\n');
  const segs = splitChangePath(path);
  let from = 0;
  let minIndent = 0;
  let found = 0;
  for (const seg of segs) {
    let hit = -1;
    if (seg.startsWith('[')) {
      const want = unquote(seg);
      if (seg.startsWith('["')) {
        // A quoted map key (labels["app.kubernetes.io/name"]).
        for (let i = from; i < lines.length; i++) {
          const t = lines[i].trimStart();
          if (i > from && t && indentOf(lines[i]) < minIndent) break;
          if (yamlKeyMatches(t, want)) { hit = i; break; }
        }
      } else {
        const isIndex = /^\d+$/.test(want);
        const nameRe = new RegExp(`^(- )?(name|key|mountPath|containerPort): ["']?${escapeRe(want)}["']?$`);
        let itemIndent = -1;
        let count = -1;
        for (let i = from; i < lines.length && hit < 0; i++) {
          const t = lines[i].trimStart();
          if (!t) continue;
          const ind = indentOf(lines[i]);
          if (ind < minIndent - 1) break;
          if (!t.startsWith('- ') && t !== '-') continue;
          if (itemIndent < 0) itemIndent = ind;
          if (ind !== itemIndent) continue;
          count++;
          if (isIndex) { if (count === Number(want)) hit = i; continue; }
          if (nameRe.test(t)) { hit = i; break; }
          for (let j = i + 1; j < lines.length && indentOf(lines[j]) > ind; j++) {
            if (indentOf(lines[j]) === ind + 2 && nameRe.test(lines[j].trimStart())) { hit = i; break; }
          }
        }
      }
    } else {
      for (let i = from; i < lines.length; i++) {
        const t = lines[i].trimStart();
        if (i > from && t && indentOf(lines[i]) < minIndent) break;
        if (yamlKeyMatches(t, seg) && indentOf(lines[i]) >= minIndent) { hit = i; break; }
      }
    }
    if (hit < 0) break;
    found = hit + 1;
    from = hit + 1;
    minIndent = indentOf(lines[hit]) + (lines[hit].trimStart().startsWith('- ') ? 2 : 1);
  }
  return found;
};
