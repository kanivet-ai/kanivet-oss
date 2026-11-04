import type { CNPGHealth, CNPGInstance } from '../../services/api/integrations';
import type { Tone } from '../gitops/statusParts';

export const cnpgHealthTone = (h?: CNPGHealth | string): Tone => {
  switch (h) {
    case 'Healthy': return 'success';
    case 'Degraded': return 'warning';
    case 'Failing': return 'danger';
    case 'Progressing': return 'info';
  }
  return 'neutral';
};

export const backupPhaseTone = (phase?: string): Tone => {
  switch ((phase || '').toLowerCase()) {
    case 'completed': return 'success';
    case 'failed': return 'danger';
    case 'running': case 'started': case 'pending': return 'info';
    case 'walarchivingfailing': return 'danger';
  }
  return 'neutral';
};

/** Replica lag in words: "in sync" under 1s, else "4.2 s", "3 min". */
export const formatLag = (seconds?: number): string => {
  if (seconds === undefined || seconds === null || Number.isNaN(seconds)) return '';
  if (seconds < 1) return 'in sync';
  if (seconds < 60) return `${seconds.toFixed(seconds < 10 ? 1 : 0)} s`;
  if (seconds < 3600) return `${Math.round(seconds / 60)} min`;
  return `${(seconds / 3600).toFixed(1)} h`;
};

export const lagTone = (seconds?: number): Tone => {
  if (seconds === undefined) return 'neutral';
  if (seconds < 5) return 'success';
  if (seconds < 60) return 'warning';
  return 'danger';
};

/** Replicas that can be promoted: ready, not the primary, not failed. */
export const promotable = (instances: CNPGInstance[]): CNPGInstance[] =>
  instances.filter((i) => i.role === 'replica' && i.ready && i.status !== 'failed');

/** Human label for the cluster's backup method. */
export const backupMethodLabel = (method?: string, plugin?: string): string => {
  switch (method) {
    case 'barmanObjectStore': return 'Barman object store';
    case 'volumeSnapshot': return 'Volume snapshot';
    case 'plugin': return plugin ? `Plugin ${plugin}` : 'Plugin';
  }
  return 'Not configured';
};
