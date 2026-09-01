import { wsManager } from './websocket';
import { getApiBase } from './types';

export type StreamChunk = (type: string, data: any) => void;

async function buildHeaders(): Promise<Record<string, string>> {
  await wsManager.waitForSessionSecret();
  const headers: Record<string, string> = {};
  const sessionSecret = wsManager.getSessionSecret();
  if (sessionSecret) headers['X-Session-Secret'] = sessionSecret;
  return headers;
}

async function consume(res: Response, onChunk: StreamChunk) {
  if (!res.body) throw new Error('stream has no body');
  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  let buf = '';
  const flush = (line: string) => {
    const trimmed = line.trim();
    if (!trimmed) return;
    try {
      const obj = JSON.parse(trimmed);
      onChunk(obj.type, obj.data);
    } catch { /* ignore malformed */ }
  };
  while (true) {
    const { value, done } = await reader.read();
    if (done) break;
    buf += decoder.decode(value, { stream: true });
    let nl: number;
    while ((nl = buf.indexOf('\n')) !== -1) {
      flush(buf.slice(0, nl));
      buf = buf.slice(nl + 1);
    }
  }
  flush(buf);
}

export async function streamJsonLines(path: string, label: string, onChunk: StreamChunk, signal?: AbortSignal): Promise<void> {
  const url = `${getApiBase()}${path}`;
  let res = await fetch(url, { headers: await buildHeaders(), signal });
  if (res.status === 403) {
    let body: any = null;
    try { body = await res.clone().json(); } catch { /* not json */ }
    const refreshHdr = res.headers.get('x-session-refresh-required') === 'true';
    if (body?.error === 'session_secret_mismatch' || body?.error === 'session_secret_missing' || refreshHdr) {
      const refreshed = await wsManager.refreshSessionSecret();
      if (refreshed) res = await fetch(url, { headers: await buildHeaders(), signal });
    }
  }
  if (!res.ok || !res.body) throw new Error(`${label} stream failed: ${res.status}`);
  await consume(res, onChunk);
}
