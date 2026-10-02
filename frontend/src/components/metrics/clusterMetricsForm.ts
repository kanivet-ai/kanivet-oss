import type { MonitoringSettings } from '../../store/types';
import type { MetricsProvidersStatus } from './metricsProvider';

export type ProviderKey = 'prometheus' | 'mimir' | 'metrics-server';

/** Providers whose row has settings behind a gear. */
export type ConfigurableProvider = 'mimir';

/**
 * Which provider's settings start open. Whoever opened the form knows best
 * (the rightsizing card asking for a tenant, say); otherwise Mimir opens when
 * it is the chosen provider or when detection says it needs a tenant, because
 * that is where the one field the user must fill in lives.
 */
export function defaultExpandedProvider(
  initial: ConfigurableProvider | null | undefined,
  preferred: MonitoringSettings['preferredProvider'],
  detected: MetricsProvidersStatus | null,
): ConfigurableProvider | null {
  if (initial) return initial;
  if (preferred === 'mimir') return 'mimir';
  if (detected?.mimir?.found && detected.mimir.needsTenant) return 'mimir';
  return null;
}

/** The fields of `edits` that differ from what is stored, so a save writes
 * only what the user changed and leaves newer values from elsewhere alone. */
export function changedSettings(
  edits: Partial<MonitoringSettings>,
  stored: MonitoringSettings,
): Partial<MonitoringSettings> {
  const out: Partial<MonitoringSettings> = {};
  (Object.keys(edits) as (keyof MonitoringSettings)[]).forEach((key) => {
    if (edits[key] !== undefined && edits[key] !== stored[key]) {
      (out as Record<string, unknown>)[key] = edits[key];
    }
  });
  return out;
}

export interface MimirSettings {
  tenant: string;
  /** "namespace/service", or empty for automatic discovery. */
  instance: string;
}

export type BackendSettingsChange = {
  mimirTenant?: string;
  mimirService?: string;
  mimirNamespace?: string;
};

/** The backend update that turns `saved` into `form`, or null when nothing
 * changed. */
export function mimirSettingsChange(
  saved: MimirSettings,
  form: MimirSettings,
): BackendSettingsChange | null {
  const change: BackendSettingsChange = {};
  if (form.tenant !== saved.tenant) change.mimirTenant = form.tenant;
  if (form.instance !== saved.instance) {
    const [namespace, service] = form.instance
      ? form.instance.split('/')
      : ['', ''];
    change.mimirNamespace = namespace;
    change.mimirService = service;
  }
  return Object.keys(change).length > 0 ? change : null;
}

export interface SaveSteps {
  local: Partial<MonitoringSettings>;
  /** The stored values `local` replaces, put back if the backend refuses. */
  previousLocal: Partial<MonitoringSettings>;
  backend: BackendSettingsChange | null;
  writeLocal: (settings: Partial<MonitoringSettings>) => void;
  writeBackend: (change: BackendSettingsChange) => Promise<void>;
  describe: (err: unknown, fallback: string) => string;
}

export type SaveResult = { ok: true } | { ok: false; message: string };

/**
 * Saves the form in an order that never leaves half of it applied. Local
 * preferences go first because they are the write most likely to fail
 * (storage full) and the cheapest to undo; the Mimir settings go to the
 * backend last, and if it refuses them the local change is put back. When
 * even that undo fails, the message says exactly which part stuck.
 */
export async function saveClusterMetrics(
  steps: SaveSteps,
): Promise<SaveResult> {
  const { local, previousLocal, backend, writeLocal, writeBackend, describe } =
    steps;
  const hasLocal = Object.keys(local).length > 0;
  if (hasLocal) {
    try {
      writeLocal(local);
    } catch (err) {
      return {
        ok: false,
        message: `Nothing was saved: ${describe(err, 'could not store the preferences')}`,
      };
    }
  }
  if (!backend) return { ok: true };
  try {
    await writeBackend(backend);
    return { ok: true };
  } catch (err) {
    const reason = describe(err, 'could not save the Mimir settings');
    if (!hasLocal)
      return { ok: false, message: `Nothing was saved: ${reason}` };
    try {
      writeLocal(previousLocal);
      return { ok: false, message: `Nothing was saved: ${reason}` };
    } catch {
      return {
        ok: false,
        message: `The provider and display choices were saved, but the Mimir settings were not: ${reason}`,
      };
    }
  }
}
