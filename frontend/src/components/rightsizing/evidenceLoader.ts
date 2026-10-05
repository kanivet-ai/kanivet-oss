import {
  getRightsizingWorkload,
  type WorkloadRef,
} from '../../services/api/rightsizing';
import type {
  Evidence,
  RightsizingProfile,
  RightsizingWindow,
} from '../../types/rightsizing';

export interface EvidenceQuery {
  cluster: string;
  ref: WorkloadRef;
  profile: RightsizingProfile;
  window: RightsizingWindow;
  provider?: string;
}

export interface EvidenceState {
  evidence: Evidence | null;
  refreshing: boolean;
  error: string | null;
  /** The metrics store is paused: why, while the one retry waits. */
  busy: string | null;
}

/** A refresh, shared by every view of the workload and cancelled once none
 * is left: it holds the backend's interactive slot for up to two minutes. */
interface Flight {
  promise: Promise<Evidence>;
  controller: AbortController;
  refs: number;
  settled: boolean;
}

const held = new Map<string, Evidence>();
const inflight = new Map<string, Flight>();
const MAX_HELD = 24;
/** A view that comes straight back (StrictMode's remount, a quick reopen)
 * rejoins its refresh instead of cancelling and restarting it. */
const ABORT_GRACE_MS = 1_000;
/** When a 503 names no usable delay. */
const BUSY_RETRY_MS = 10_000;
const MAX_BUSY_RETRY_MS = 120_000;

function keyOf(q: EvidenceQuery) {
  return JSON.stringify([
    q.cluster,
    q.provider ?? 'auto',
    q.profile,
    q.window,
    q.ref.namespace,
    q.ref.vclusterNamespace ?? '',
    q.ref.kind,
    q.ref.name,
  ]);
}

function remember(key: string, evidence: Evidence) {
  held.delete(key);
  held.set(key, evidence);
  while (held.size > MAX_HELD) held.delete(held.keys().next().value!);
}

/** How long to wait before retrying a busy (503) answer; null for any other
 * failure. */
export function busyRetryMs(e: any): number | null {
  const res = e?.response;
  if (res?.status !== 503) return null;
  const s = Number(
    res.data?.retryAfterSeconds ?? res.headers?.['retry-after'] ?? NaN,
  );
  return s > 0 ? Math.min(s * 1000, MAX_BUSY_RETRY_MS) : BUSY_RETRY_MS;
}

const busyMessage = (e: any): string =>
  e?.response?.data?.error || 'The metrics store is busy';

function join(key: string, q: EvidenceQuery): Flight {
  let flight = inflight.get(key);
  if (!flight) {
    const controller = new AbortController();
    const f: Flight = {
      controller,
      refs: 0,
      settled: false,
      promise: getRightsizingWorkload(
        q.cluster,
        q.ref,
        q.profile,
        q.window,
        q.provider,
        'refresh',
        controller.signal,
      )
        .then((result) => {
          if (!result) throw new Error('No workload evidence returned');
          remember(key, result);
          return result;
        })
        .finally(() => {
          f.settled = true;
          if (inflight.get(key) === f) inflight.delete(key);
        }),
    };
    inflight.set(key, f);
    flight = f;
  }
  flight.refs++;
  return flight;
}

function leave(key: string, f: Flight) {
  if (--f.refs > 0 || f.settled) return;
  setTimeout(() => {
    if (f.refs > 0 || f.settled) return;
    f.controller.abort();
    if (inflight.get(key) === f) inflight.delete(key);
  }, ABORT_GRACE_MS);
}

export function loadEvidence(
  q: EvidenceQuery,
  update: (state: EvidenceState) => void,
): () => void {
  const key = keyOf(q);
  let live = true;
  let freshSettled = false;
  let freshSucceeded = false;
  let evidence = held.get(key) ?? null;
  let error: string | null = null;
  let busy: string | null = null;
  let flight: Flight | null = null;
  let retry: ReturnType<typeof setTimeout> | undefined;
  const cached = new AbortController();
  const emit = () => {
    if (live) update({ evidence, refreshing: !freshSettled, error, busy });
  };
  emit();

  if (!evidence) {
    void getRightsizingWorkload(
      q.cluster,
      q.ref,
      q.profile,
      q.window,
      q.provider,
      'cached',
      cached.signal,
    )
      .then((snapshot) => {
        // A slow cache read must never replace a newer upstream result.
        if (snapshot && !freshSucceeded && live) {
          evidence = snapshot;
          if (!held.has(key)) remember(key, snapshot);
          emit();
        }
      })
      .catch(() => {
        /* The fresh request reports errors. */
      });
  }

  const refresh = async (retried: boolean) => {
    if (flight) leave(key, flight);
    const mine = (flight = join(key, q));
    try {
      evidence = await mine.promise;
      freshSucceeded = true;
      error = busy = null;
    } catch (e: any) {
      const wait = busyRetryMs(e);
      if (wait !== null && !retried && live) {
        // The metrics store is paused: keep what is shown, say why, and try
        // once more when it says to.
        busy = busyMessage(e);
        emit();
        retry = setTimeout(() => void refresh(true), wait);
        return;
      }
      busy = null;
      error =
        wait !== null
          ? busyMessage(e)
          : e?.response?.data?.error ||
            e?.message ||
            'Failed to refresh evidence';
    }
    freshSettled = true;
    emit();
  };
  void refresh(false);

  return () => {
    live = false;
    clearTimeout(retry);
    cached.abort();
    if (flight) leave(key, flight);
    flight = null;
  };
}
