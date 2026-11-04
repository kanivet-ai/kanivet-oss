import type { ImageLayer, ImageLayerChange, ImagePullContext } from '../../services/api/images';

export const IMAGE_PAGE_KIND = 'ImageInspector';

export interface ImageTabTarget {
  ref: string;
  pullContext: ImagePullContext;
}

/** "ghcr.io/org/app:v1@sha256:…" -> "app:v1". */
export function shortImageRef(ref: string): string {
  let r = ref.trim();
  let digest = '';
  const at = r.indexOf('@');
  if (at >= 0) {
    digest = r.slice(at + 1);
    r = r.slice(0, at);
  }
  const slash = r.lastIndexOf('/');
  const colon = r.lastIndexOf(':');
  let tag = '';
  if (colon > slash) {
    tag = r.slice(colon + 1);
    r = r.slice(0, colon);
  }
  const name = r.slice(slash + 1);
  if (tag) return `${name}:${tag}`;
  if (digest) return `${name}@${shortDigest(digest)}`;
  return name;
}

export function shortDigest(d?: string): string {
  if (!d) return '';
  const hex = d.includes(':') ? d.split(':')[1] : d;
  return hex.slice(0, 12);
}

/** The resource a tab carries: its name keeps one tab per image + context. */
export function imageTabResource(target: ImageTabTarget) {
  const pc = target.pullContext;
  const scope = [pc.namespace, pc.pod, pc.container].filter(Boolean).join('/');
  return {
    name: `image:${target.ref}${scope ? `|${scope}` : ''}`,
    group: '',
    version: 'v1',
    kind: IMAGE_PAGE_KIND,
    namespaced: false,
    title: shortImageRef(target.ref),
    image: target,
  };
}

export function formatSize(bytes?: number): string {
  if (bytes === undefined || bytes === null || Number.isNaN(bytes)) return '';
  if (bytes < 1024) return `${bytes} B`;
  const units = ['KB', 'MB', 'GB', 'TB'];
  let v = bytes / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v >= 100 ? v.toFixed(0) : v >= 10 ? v.toFixed(1) : v.toFixed(2)} ${units[i]}`;
}

/** Strips the Dockerfile shell prefix from a history line. */
export function cleanCreatedBy(s?: string): string {
  if (!s) return '';
  return s
    .replace(/^\/bin\/sh -c #\(nop\)\s*/, '')
    .replace(/^\/bin\/sh -c /, 'RUN ')
    .replace(/\s*# buildkit$/, '')
    .trim();
}

export interface LayerBar {
  index: number;
  size: number;
  /** Share of the total, 0..1. */
  share: number;
  /** Width relative to the largest layer, 0..1. */
  width: number;
  label: string;
}

export function layerBars(layers: ImageLayer[]): LayerBar[] {
  const total = layers.reduce((a, l) => a + (l.size || 0), 0) || 1;
  const max = layers.reduce((a, l) => Math.max(a, l.size || 0), 0) || 1;
  return layers.map((l) => ({
    index: l.index,
    size: l.size,
    share: (l.size || 0) / total,
    width: Math.max((l.size || 0) / max, 0.004),
    label: cleanCreatedBy(l.createdBy) || `Layer ${l.index + 1}`,
  }));
}

/** Monaco language id from a file path. */
export function languageForPath(path: string): string {
  const base = path.split('/').pop()?.toLowerCase() || '';
  if (base === 'dockerfile') return 'dockerfile';
  if (base === 'makefile') return 'plaintext';
  const ext = base.includes('.') ? base.split('.').pop() || '' : '';
  const map: Record<string, string> = {
    json: 'json', yaml: 'yaml', yml: 'yaml', xml: 'xml', html: 'html', htm: 'html', css: 'css',
    js: 'javascript', mjs: 'javascript', cjs: 'javascript', ts: 'typescript', py: 'python', go: 'go',
    sh: 'shell', bash: 'shell', zsh: 'shell', rb: 'ruby', rs: 'rust', java: 'java', kt: 'kotlin',
    c: 'c', h: 'c', cpp: 'cpp', hpp: 'cpp', md: 'markdown', sql: 'sql', ini: 'ini', conf: 'ini',
    cfg: 'ini', toml: 'ini', php: 'php', lua: 'lua', pl: 'perl', properties: 'ini',
  };
  return map[ext] || 'plaintext';
}

/** Groups a layer's change list into a sorted list with directories first
 * collapsed into their paths, for the "changes in this layer" view. */
export function sortChanges(changes: ImageLayerChange[], query = ''): ImageLayerChange[] {
  const q = query.trim().toLowerCase();
  return changes
    .filter((c) => !q || c.path.toLowerCase().includes(q))
    .sort((a, b) => a.path.localeCompare(b.path));
}

export function parentPath(p: string): string {
  const clean = p.replace(/\/+$/, '');
  const i = clean.lastIndexOf('/');
  return i <= 0 ? '/' : clean.slice(0, i);
}

/** Quote a command array the way a shell would show it. */
export function formatCommand(parts?: string[]): string {
  if (!parts || parts.length === 0) return '';
  return parts.map((p) => (/[\s"'$`\\]/.test(p) ? JSON.stringify(p) : p)).join(' ');
}
