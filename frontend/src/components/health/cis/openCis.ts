import { openHealthPage } from '../healthActions';

/** The window event an open Audit page listens to for a view switch. */
export const AUDIT_VIEW_EVENT = 'kanivet:audit-view';

let pending: string | null = null;

/** The view a freshly mounted Audit page should start on, once. */
export function takePendingAuditView(): string | null {
  const v = pending;
  pending = null;
  return v;
}

/** Opens Audit on the CIS view (an already open page switches to it). */
export async function openCisBenchmark(cluster: string) {
  if (!cluster) return;
  pending = 'cis';
  window.dispatchEvent(
    new CustomEvent(AUDIT_VIEW_EVENT, { detail: { cluster, view: 'cis' } }),
  );
  await openHealthPage(cluster, 'audit');
}
