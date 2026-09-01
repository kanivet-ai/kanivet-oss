export type LogLevel = 'error' | 'warn' | 'info' | 'debug' | 'none';

export interface Seg {
  text: string;
  cls: string;
}

export interface LogLine {
  id: number;
  text: string;
  lower: string;
  segs: Seg[] | null;
  pod: string;
  container: string;
  ts: number;
  tsStr: string;
  level: LogLevel;
}

export interface RawBatchLine {
  data?: string;
  podName?: string;
  container?: string;
  timestamp?: string;
}

const MAX_LINES = 50000;
const TRIM_TO = 45000;

// eslint-disable-next-line no-control-regex
const CTRL_RE = /[\x00-\x08\x0B\x0C\x0E-\x1F\x7F]/g;
// eslint-disable-next-line no-control-regex
const ESC_RE = /\x1b\[([0-9;]*)m|\x1b\[[0-9;?]*[A-Za-z]/g;

interface SgrState {
  fg: number;
  bold: boolean;
}

function applySgr(state: SgrState, params: string) {
  const codes = params === '' ? [0] : params.split(';').map((c) => parseInt(c, 10) || 0);
  for (let i = 0; i < codes.length; i++) {
    const c = codes[i];
    if (c === 0) {
      state.fg = 0;
      state.bold = false;
    } else if (c === 1) state.bold = true;
    else if (c === 22) state.bold = false;
    else if ((c >= 30 && c <= 37) || (c >= 90 && c <= 97)) state.fg = c;
    else if (c === 39) state.fg = 0;
    else if (c === 38 || c === 48) {
      if (codes[i + 1] === 5) i += 2;
      else if (codes[i + 1] === 2) i += 4;
    }
  }
}

function clsOf(state: SgrState): string {
  let cls = state.fg ? `lv-a${state.fg}` : '';
  if (state.bold) cls = cls ? `${cls} lv-ab` : 'lv-ab';
  return cls;
}

export function parseRaw(raw: string): { text: string; segs: Seg[] | null } {
  let s = raw;
  if (s.includes('\r')) {
    const parts = s.split('\r').filter((p) => p.length);
    s = parts[parts.length - 1] ?? '';
  }
  if (!s.includes('\x1b')) return { text: s.replace(CTRL_RE, ''), segs: null };

  const segs: Seg[] = [];
  const state: SgrState = { fg: 0, bold: false };
  let plain = '';
  let styled = false;
  let idx = 0;
  const push = (t: string) => {
    if (!t) return;
    const cls = clsOf(state);
    if (cls) styled = true;
    const last = segs[segs.length - 1];
    if (last && last.cls === cls) last.text += t;
    else segs.push({ text: t, cls });
    plain += t;
  };
  ESC_RE.lastIndex = 0;
  let m: RegExpExecArray | null;
  while ((m = ESC_RE.exec(s))) {
    push(s.slice(idx, m.index).replace(CTRL_RE, ''));
    idx = ESC_RE.lastIndex;
    if (m[1] !== undefined) applySgr(state, m[1]);
  }
  push(s.slice(idx).replace(CTRL_RE, ''));
  return { text: plain, segs: styled ? segs : null };
}

const KLOG_RE = /^([EWIF])\d{4}\s/;
const LEVEL_RE =
  /\b(fatal|panic|crit(?:ical)?|error|err)\b|\b(warn(?:ing)?)\b|\b(info)\b|\b(debug|trace|dbg|trc)\b/i;

export function detectLevel(text: string): LogLevel {
  const head = text.length > 300 ? text.slice(0, 300) : text;
  const k = KLOG_RE.exec(head);
  if (k) return k[1] === 'E' || k[1] === 'F' ? 'error' : k[1] === 'W' ? 'warn' : 'info';
  const m = LEVEL_RE.exec(head);
  if (!m) return 'none';
  if (m[1]) return 'error';
  if (m[2]) return 'warn';
  if (m[3]) return 'info';
  return 'debug';
}

const pad = (n: number, w = 2) => n.toString().padStart(w, '0');

export function fmtTs(ms: number): string {
  if (!ms) return '';
  const d = new Date(ms);
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}.${pad(d.getMilliseconds(), 3)}`;
}

const emptyCounts = (): Record<LogLevel, number> => ({ error: 0, warn: 0, info: 0, debug: 0, none: 0 });

export class LogBuffer {
  lines: LogLine[] = [];
  counts: Record<LogLevel, number> = emptyCounts();
  version = 0;
  private nextId = 1;
  private listeners = new Set<() => void>();
  private raf = 0;

  append(batch: RawBatchLine[]) {
    for (const l of batch) {
      const { text, segs } = parseRaw(l.data ?? '');
      const parsed = l.timestamp ? Date.parse(l.timestamp) : 0;
      const ts = Number.isNaN(parsed) ? 0 : parsed;
      const line: LogLine = {
        id: this.nextId++,
        text,
        lower: text.toLowerCase(),
        segs,
        pod: l.podName || '',
        container: l.container || '',
        ts,
        tsStr: fmtTs(ts),
        level: detectLevel(text),
      };
      this.lines.push(line);
      this.counts[line.level]++;
    }
    if (this.lines.length > MAX_LINES) {
      const removed = this.lines.splice(0, this.lines.length - TRIM_TO);
      for (const r of removed) this.counts[r.level]--;
    }
    this.bump();
  }

  clear() {
    this.lines = [];
    this.counts = emptyCounts();
    this.bump();
  }

  subscribe = (fn: () => void) => {
    this.listeners.add(fn);
    return () => {
      this.listeners.delete(fn);
    };
  };

  getVersion = () => this.version;

  private bump() {
    this.version++;
    if (this.raf) return;
    this.raf = requestAnimationFrame(() => {
      this.raf = 0;
      this.listeners.forEach((fn) => fn());
    });
  }
}

export interface Matcher {
  test(line: LogLine): boolean;
  ranges(text: string, lower: string): [number, number][];
}

export function buildMatcher(
  query: string,
  caseSensitive: boolean,
  useRegex: boolean,
): Matcher | null | 'invalid' {
  if (!query) return null;
  if (useRegex) {
    let reTest: RegExp;
    let reAll: RegExp;
    try {
      reTest = new RegExp(query, caseSensitive ? '' : 'i');
      reAll = new RegExp(query, caseSensitive ? 'g' : 'gi');
    } catch {
      return 'invalid';
    }
    return {
      test: (line) => reTest.test(line.text),
      ranges: (text) => {
        const out: [number, number][] = [];
        reAll.lastIndex = 0;
        let m: RegExpExecArray | null;
        while ((m = reAll.exec(text))) {
          if (m[0] === '') {
            reAll.lastIndex++;
            continue;
          }
          out.push([m.index, m.index + m[0].length]);
          if (out.length > 200) break;
        }
        return out;
      },
    };
  }
  const needle = caseSensitive ? query : query.toLowerCase();
  return {
    test: (line) => (caseSensitive ? line.text : line.lower).includes(needle),
    ranges: (text, lower) => {
      const hay = caseSensitive ? text : lower;
      const out: [number, number][] = [];
      let idx = hay.indexOf(needle);
      while (idx !== -1) {
        out.push([idx, idx + needle.length]);
        if (out.length > 200) break;
        idx = hay.indexOf(needle, idx + needle.length);
      }
      return out;
    },
  };
}
