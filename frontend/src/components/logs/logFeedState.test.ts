import { beforeAll, describe, expect, it } from 'vitest';
import { LogBuffer } from './logEngine';
import { createLogFeedState, endedLabel, FeedEnd, FeedStatus } from './logFeedState';

beforeAll(() => {
  (globalThis as any).requestAnimationFrame ??= (fn: () => void) => setTimeout(fn, 0);
});

function setup() {
  const buffer = new LogBuffer();
  const state = { status: 'connecting' as FeedStatus, error: null as string | null, end: null as FeedEnd | null };
  const feed = createLogFeedState(buffer, {
    setStatus: (u) => { state.status = typeof u === 'function' ? u(state.status) : u; },
    setError: (e) => { state.error = e; },
    setEnd: (e) => { state.end = e; },
    setPods: () => {},
    setContainers: () => {},
  });
  return { buffer, state, feed };
}

const batch = (seq: number, ...lines: string[]) => ({ type: 'batch', key: 'k', sequence: seq, lines: lines.map((data) => ({ data })) });
const texts = (b: LogBuffer) => b.lines.map((l) => l.text);

describe('log feed', () => {
  it('a missing pod ends the stream instead of showing it live', () => {
    const { feed, state } = setup();
    feed.restart();
    feed.handle({ type: 'connected', key: 'k' });
    feed.handle({ type: 'end', key: 'k', reason: 'not_found', message: 'pod ns/web no longer exists' });
    expect(state.status).toBe('ended');
    expect(endedLabel(state.end, false)).toBe('Stream ended: pod no longer exists');
  });

  it('a reconnect to a deleted pod keeps its lines after a marker', () => {
    const { feed, buffer, state } = setup();
    feed.restart();
    feed.handle(batch(1, 'a', 'b'));
    feed.restart(); // connection restored
    expect(texts(buffer)).toEqual(['a', 'b']);
    feed.handle({ type: 'connected', key: 'k' });
    feed.handle({ type: 'end', key: 'k', reason: 'not_found', message: 'pod ns/web no longer exists' });
    expect(texts(buffer)).toEqual(['a', 'b', 'Stream ended: pod ns/web no longer exists']);
    expect(buffer.lines[2].marker).toBe(true);
    expect(state.status).toBe('ended');
  });

  it('a reconnect replaces the lines with the new stream once it sends them', () => {
    const { feed, buffer } = setup();
    feed.restart();
    feed.handle(batch(1, 'a', 'b'));
    feed.restart();
    feed.handle({ type: 'connected', key: 'k' });
    expect(texts(buffer)).toEqual(['a', 'b']);
    feed.handle(batch(1, 'b', 'c'));
    expect(texts(buffer)).toEqual(['b', 'c']);
    feed.handle(batch(2, 'd'));
    expect(texts(buffer)).toEqual(['b', 'c', 'd']);
  });

  it('an error from the new stream replaces the old lines', () => {
    const { feed, buffer, state } = setup();
    feed.restart();
    feed.handle(batch(1, 'a'));
    feed.restart();
    feed.handle({ type: 'error', key: 'k', error: 'forbidden' });
    expect(buffer.lines).toHaveLength(0);
    expect(state.status).toBe('error');
  });

  it('a plain end keeps its legacy label', () => {
    expect(endedLabel(null, false)).toBe('Stream ended');
    expect(endedLabel(null, true)).toBe('Previous instance');
  });
});
