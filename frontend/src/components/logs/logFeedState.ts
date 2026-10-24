import type { LogBuffer, RawBatchLine } from './logEngine';

export type FeedStatus = 'connecting' | 'live' | 'ended' | 'error';

/** Why a stream ended, when the backend says. */
export interface FeedEnd {
  /** e.g. 'not_found': the pod or workload does not exist (any more). */
  reason: string;
  message?: string;
}

export interface FeedSink {
  setStatus(update: FeedStatus | ((s: FeedStatus) => FeedStatus)): void;
  setError(error: string | null): void;
  setEnd(end: FeedEnd | null): void;
  setPods(pods: any[]): void;
  setContainers(containers: any[]): void;
}

/** The status-bar text for a stream that ended. */
export function endedLabel(end: FeedEnd | null, previous: boolean): string {
  if (end?.reason === 'not_found') {
    const what = end.message?.split(' ')[0] || 'pod';
    return `Stream ended: ${what} no longer exists`;
  }
  return previous ? 'Previous instance' : 'Stream ended';
}

/**
 * The state of one log feed across its (re)starts. A restart of the same
 * stream (a reconnect) keeps the lines shown until the new stream answers: its
 * first batch, or an end or error, replaces them. When the answer is that
 * the pod is gone, the old lines stay, after a marker saying so, instead of
 * being wiped for an empty viewer.
 */
export function createLogFeedState(buffer: LogBuffer, sink: FeedSink) {
  let lastSeq = 0;
  let started = false;
  let replacing = false;

  const replaceOld = () => {
    if (!replacing) return;
    replacing = false;
    buffer.clear();
  };

  return {
    restart() {
      lastSeq = 0;
      if (started) {
        replacing = true;
      } else {
        // A new feed (other pod, container or options): the lines shown are
        // not this stream's.
        started = true;
        replacing = false;
        buffer.clear();
      }
      sink.setStatus('connecting');
      sink.setError(null);
      sink.setEnd(null);
    },

    disconnect() {
      sink.setStatus('connecting');
    },

    handle(d: any) {
      switch (d.type) {
        case 'connected':
          sink.setStatus('live');
          break;
        case 'pods':
          sink.setPods(d.pods || []);
          break;
        case 'containers':
          sink.setContainers(d.containers || []);
          break;
        case 'end': {
          if (d.reason === 'not_found') {
            replacing = false;
            const message = d.message || 'the resource no longer exists';
            if (buffer.lines.length > 0) buffer.appendMarker(`Stream ended: ${message}`);
            sink.setEnd({ reason: d.reason, message });
          } else {
            replaceOld();
            sink.setEnd(d.reason ? { reason: d.reason, message: d.message } : null);
          }
          sink.setStatus('ended');
          break;
        }
        case 'error':
          replaceOld();
          sink.setError(d.error || 'log stream error');
          sink.setStatus('error');
          break;
        case 'batch': {
          if (typeof d.sequence === 'number') {
            if (d.sequence <= lastSeq) return;
            lastSeq = d.sequence;
          }
          replaceOld();
          const lines: RawBatchLine[] = Array.isArray(d.lines)
            ? d.lines.map((l: any) => (typeof l === 'string' ? { data: l } : l))
            : [];
          buffer.append(lines);
          sink.setStatus((s) => (s === 'connecting' ? 'live' : s));
          break;
        }
      }
    },
  };
}
