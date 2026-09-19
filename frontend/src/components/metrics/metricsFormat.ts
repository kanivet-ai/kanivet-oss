/* Metric vocabulary and number formatting shared by the metrics cards. */

export type MetricType = 'cpu' | 'memory' | 'network_rx' | 'network_tx' | 'disk_read' | 'disk_write';
export type TimeRange = '5m' | '15m' | '1h' | '6h' | '24h' | string;

export const METRIC_ORDER: MetricType[] = ['cpu', 'memory', 'network_rx', 'network_tx', 'disk_read', 'disk_write'];

/** Short segment labels (sentence case). */
export const METRIC_LABELS: Record<MetricType, string> = {
  cpu: 'CPU',
  memory: 'Memory',
  network_rx: 'Net in',
  network_tx: 'Net out',
  disk_read: 'Disk read',
  disk_write: 'Disk write',
};

/** Chart titles. */
export const METRIC_TITLES: Record<MetricType, string> = {
  cpu: 'CPU usage',
  memory: 'Memory usage',
  network_rx: 'Network received',
  network_tx: 'Network transmitted',
  disk_read: 'Disk read',
  disk_write: 'Disk write',
};

export const TIME_RANGES: TimeRange[] = ['5m', '15m', '1h', '6h', '24h'];

export const isPresetTimeRange = (range: TimeRange): boolean => TIME_RANGES.includes(range);

/** "90m" → "1h 30m", "15m" → "15m", "6h" → "6h". */
export const describeTimeRange = (range: TimeRange): string => {
  const match = /^(\d+)\s*([mh])$/.exec(range || '');
  if (!match) return range;
  const n = parseInt(match[1], 10);
  const minutes = match[2] === 'h' ? n * 60 : n;
  if (minutes < 60) return `${minutes}m`;
  const h = Math.floor(minutes / 60);
  const m = minutes % 60;
  return m > 0 ? `${h}h ${m}m` : `${h}h`;
};

export interface FormattedValue {
  value: string;
  unit: string;
}

const KIB = 1024;
const MIB = KIB * 1024;
const GIB = MIB * 1024;
const TIB = GIB * 1024;

const trim = (n: number, digits: number): string => {
  const s = n.toFixed(digits);
  return s.includes('.') ? s.replace(/\.?0+$/, '') : s;
};

const formatBytes = (bytes: number, digits = 1): FormattedValue => {
  if (bytes >= TIB) return { value: trim(bytes / TIB, 2), unit: 'TiB' };
  if (bytes >= GIB) return { value: trim(bytes / GIB, 2), unit: 'GiB' };
  if (bytes >= MIB) return { value: trim(bytes / MIB, digits), unit: 'MiB' };
  if (bytes >= KIB) return { value: trim(bytes / KIB, digits), unit: 'KiB' };
  return { value: trim(bytes, 0), unit: 'B' };
};

/** Rates come from the backend in KB/s. */
const formatRateKB = (kbps: number): FormattedValue => {
  if (kbps >= 1_000_000) return { value: trim(kbps / 1_000_000, 2), unit: 'GB/s' };
  if (kbps >= 1000) return { value: trim(kbps / 1000, 2), unit: 'MB/s' };
  if (kbps >= 1) return { value: trim(kbps, 1), unit: 'KB/s' };
  return { value: trim(kbps * 1000, 0), unit: 'B/s' };
};

/**
 * Human value for the headline and tooltips.
 * CPU values are millicores; memory values are bytes; rates are KB/s.
 */
export const formatMetricValue = (metric: MetricType, value: number, unit?: string): FormattedValue => {
  if (!Number.isFinite(value)) return { value: '–', unit: '' };
  switch (metric) {
    case 'cpu':
      return value >= 1000
        ? { value: trim(value / 1000, 2), unit: 'cores' }
        : { value: Math.round(value).toString(), unit: 'm' };
    case 'memory':
      return formatBytes(value);
    case 'network_rx':
    case 'network_tx':
    case 'disk_read':
    case 'disk_write':
      return formatRateKB(value);
    default:
      return { value: trim(value, 2), unit: unit || '' };
  }
};

/** "250m", "1.2 cores", "512 MiB" — value and unit joined the way people read them. */
export const joinValue = ({ value, unit }: FormattedValue): string =>
  unit === 'm' ? `${value}m` : unit ? `${value} ${unit}` : value;

/** Compact axis tick: "250m" · "1.2" (cores) · "512Mi" · "42K" (KB/s). */
export const formatAxisTick = (metric: MetricType, value: number): string => {
  if (!Number.isFinite(value)) return '';
  switch (metric) {
    case 'cpu':
      return value >= 1000 ? trim(value / 1000, 1) : `${trim(value, 0)}m`;
    case 'memory': {
      const f = formatBytes(value, 1);
      return `${f.value}${f.unit.replace('iB', 'i').replace('B', '')}`;
    }
    default: {
      if (value >= 1000) return `${trim(value / 1000, 1)}M`;
      return `${trim(value, value < 10 ? 1 : 0)}K`;
    }
  }
};

/** Long tooltip value: "250 millicores" · "1.25 cores" · "512 MiB" · "42.5 KB/s". */
export const formatTooltipValue = (metric: MetricType, value: number, unit?: string): string => {
  const f = formatMetricValue(metric, value, unit);
  if (metric === 'cpu' && f.unit === 'm') return `${f.value} millicores`;
  return joinValue(f);
};

/** Reference lines (limit/request/capacity) in compact form: "500m", "1Gi". */
export const formatReferenceValue = (metric: MetricType, value: number): string => {
  if (metric === 'cpu') return value >= 1000 ? `${trim(value / 1000, 1)} cores` : `${trim(value, 0)}m`;
  if (metric === 'memory') {
    const f = formatBytes(value, 0);
    return `${f.value} ${f.unit}`;
  }
  return joinValue(formatMetricValue(metric, value));
};

/** Backend labels are "HH:MM:SS" (or ISO); axes show HH:MM. */
export const formatTimeLabel = (label: string): string => {
  if (!label) return '';
  const hm = /^(\d{1,2}):(\d{2})/.exec(label);
  if (hm) return `${hm[1].padStart(2, '0')}:${hm[2]}`;
  const date = new Date(label);
  if (Number.isNaN(date.getTime())) return label;
  return date.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', hour12: false });
};

/** Local-time value for a `datetime-local` input. */
export const toDateTimeLocal = (d: Date): string => {
  const pad = (n: number) => n.toString().padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
};

/** Minutes between two datetime-local values; null when unset or non-positive. */
export const customRangeMinutes = (start: string, end: string): number | null => {
  if (!start || !end) return null;
  const minutes = Math.round((new Date(end).getTime() - new Date(start).getTime()) / 60000);
  return Number.isFinite(minutes) && minutes > 0 ? minutes : null;
};
