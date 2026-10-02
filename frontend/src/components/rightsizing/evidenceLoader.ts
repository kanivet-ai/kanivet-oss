import api from '../../services/api';
import type { WorkloadRef } from '../../services/api/rightsizing';
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
}

const held = new Map<string, Evidence>();
const inflight = new Map<string, Promise<Evidence | null>>();
const MAX_HELD = 24;

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
  const emit = () => {
    if (live) update({ evidence, refreshing: !freshSettled, error });
  };
  const request = (mode: 'cached' | 'refresh') =>
    api.getRightsizingWorkload(
      q.cluster,
      q.ref,
      q.profile,
      q.window,
      q.provider,
      mode,
    );
  emit();

  if (!evidence) {
    void request('cached')
      .then((cached) => {
        // A slow cache read must never replace a newer upstream result.
        if (cached && !freshSucceeded && live) {
          evidence = cached;
          if (!held.has(key)) remember(key, cached);
          emit();
        }
      })
      .catch(() => {
        /* The fresh request reports errors. */
      });
  }

  let fresh = inflight.get(key);
  if (!fresh) {
    fresh = request('refresh')
      .then((result) => {
        if (!result) throw new Error('No workload evidence returned');
        remember(key, result);
        return result;
      })
      .finally(() => {
        inflight.delete(key);
      });
    inflight.set(key, fresh);
  }
  void fresh
    .then((result) => {
      freshSucceeded = true;
      evidence = result;
    })
    .catch((e) => {
      error =
        e?.response?.data?.error || e?.message || 'Failed to refresh evidence';
    })
    .finally(() => {
      freshSettled = true;
      emit();
    });
  return () => {
    live = false;
  };
}
