import type { ChangeCategory, ChangeEntry, ChangeSource } from '../types/changes';

export const SOURCE_LABELS: Record<ChangeSource, string> = {
  replicaset: 'ReplicaSet',
  controllerrevision: 'ControllerRevision',
  helm: 'Helm',
  argocd: 'Argo CD',
  flux: 'Flux',
  managedfields: 'managedFields',
  event: 'Event',
  creation: 'Metadata',
};

export const CATEGORY_LABELS: Record<ChangeCategory, string> = {
  created: 'Created',
  deleted: 'Deleted',
  rollout: 'Rollout',
  image: 'Image',
  env: 'Env',
  config: 'Config',
  resources: 'Resources',
  replicas: 'Scaled',
  restart: 'Restart',
  secret: 'Secret',
  sync: 'Sync',
  edit: 'Edit',
  event: 'Event',
  other: 'Other',
};

/** Badge tone of a category, mapped onto the .ap-badge variants. */
export function categoryTone(c: ChangeCategory): 'blue' | 'orange' | 'green' | 'purple' | 'red' | 'gray' {
  switch (c) {
    case 'image':
    case 'rollout':
      return 'blue';
    case 'env':
    case 'config':
    case 'secret':
      return 'orange';
    case 'created':
      return 'green';
    case 'sync':
    case 'replicas':
      return 'purple';
    case 'deleted':
      return 'red';
    default:
      return 'gray';
  }
}

const TONE_BADGE = { blue: 'ap-badge--info', orange: 'ap-badge--warning', green: 'ap-badge--success', purple: 'ap-badge--purple', red: 'ap-badge--danger', gray: '' };

/** The .ap-badge modifier class of a category. */
export function categoryBadgeClass(c: ChangeCategory): string {
  return TONE_BADGE[categoryTone(c)];
}

/** The category that names an entry best (rollout is implied by most). */
export function primaryCategory(e: ChangeEntry): ChangeCategory {
  const order: ChangeCategory[] = ['deleted', 'created', 'image', 'env', 'config', 'secret', 'resources', 'replicas', 'restart', 'sync', 'rollout', 'edit', 'event', 'other'];
  for (const c of order) if (e.categories.includes(c)) return c;
  return 'other';
}

export type HistoryView = 'all' | 'revisions' | 'edits' | 'events';

/** Filters the History tab's timeline by its segmented control. */
export function filterHistory(entries: ChangeEntry[], view: HistoryView): ChangeEntry[] {
  switch (view) {
    case 'revisions':
      return entries.filter((e) => e.hasSnapshot);
    case 'edits':
      return entries.filter((e) => e.source === 'managedfields' || e.source === 'creation');
    case 'events':
      return entries.filter((e) => e.source === 'event');
    default:
      return entries;
  }
}

/**
 * Picks the default pair to diff when an entry is selected: the entry itself
 * against the next older entry from the same source that has a snapshot.
 */
export function defaultBaseline(entries: ChangeEntry[], selected: ChangeEntry): ChangeEntry | null {
  const idx = entries.findIndex((e) => e.id === selected.id);
  if (idx < 0) return null;
  for (let i = idx + 1; i < entries.length; i++) {
    const e = entries[i];
    if (e.hasSnapshot && e.source === selected.source) return e;
  }
  return null;
}

/** Day bucket label used to group the timeline ("Today", "Yesterday", date). */
export function dayLabel(iso: string, now: Date = new Date()): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime()) || d.getTime() <= 0) return 'Unknown time';
  const startOf = (x: Date) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
  const days = Math.round((startOf(now) - startOf(d)) / 86400000);
  if (days === 0) return 'Today';
  if (days === 1) return 'Yesterday';
  return d.toLocaleDateString(undefined, { year: d.getFullYear() === now.getFullYear() ? undefined : 'numeric', month: 'short', day: 'numeric' });
}

export function groupByDay(entries: ChangeEntry[], now: Date = new Date()): Array<{ label: string; entries: ChangeEntry[] }> {
  const out: Array<{ label: string; entries: ChangeEntry[] }> = [];
  for (const e of entries) {
    const label = dayLabel(e.time, now);
    const last = out[out.length - 1];
    if (last && last.label === label) last.entries.push(e);
    else out.push({ label, entries: [e] });
  }
  return out;
}

/** apiVersion string of a ref, for ResourceLink. */
export function apiVersionOf(ref: { group: string; version: string }): string {
  return ref.group ? `${ref.group}/${ref.version || 'v1'}` : ref.version || 'v1';
}

export function formatClock(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime()) || d.getTime() <= 0) return '';
  return d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });
}
