import React from 'react';
import { formatStatus } from './formatters';
import LiveAge from '../components/common/LiveAge';

type BadgeTone = 'success' | 'warning' | 'danger' | 'neutral';

const SUCCESS_VALUES = [
  'true',
  'active',
  'bound',
  'deployed',
  'available',
  'healthy',
  'synced',
];

const SUCCESS_PARTIAL_VALUES = [
  'running',
  'ready',
  'succeeded',
  'complete',
  'completed',
];

const getBadgeTone = (label: string): BadgeTone => {
  const lower = String(label || '').toLowerCase();
  if (
    lower.includes('pending') ||
    lower.includes('not ready') ||
    lower.includes('notready') ||
    lower.includes('unknown') ||
    lower.includes('terminating') ||
    lower.includes('uninstalling')
  )
    return 'warning';

  if (
    SUCCESS_VALUES.includes(lower) ||
    SUCCESS_PARTIAL_VALUES.some((value) => lower.includes(value))
  )
    return 'success';

  if (
    lower.includes('failed') ||
    lower.includes('error') ||
    lower.includes('crash') ||
    lower === 'uninstalled'
  )
    return 'danger';

  if (lower === 'superseded')
    return 'neutral';

  return 'neutral';
};

export const StatusBadge = ({ item }: { item: any }): React.ReactElement => {
  const label = formatStatus(item);
  const tone = getBadgeTone(label);

  return (
    <span className={`status-badge ${tone}`} title={label}>
      {label || '-'}
    </span>
  );
};

export const RestartBadge = ({
  restarts,
}: {
  restarts: number;
}): React.ReactElement => {
  const count = typeof restarts === 'number' ? restarts : 0;
  let tone: BadgeTone = 'neutral';
  if (count >= 3 && count < 6) tone = 'warning';
  if (count >= 6) tone = 'danger';

  return (
    <span className={`restarts-badge ${tone}`} title={`${count} restarts`}>
      {count}
    </span>
  );
};

export const ReadyBadge = ({
  item,
}: {
  item: any;
}): React.ReactElement | string => {
  if (item.containerStatuses && Array.isArray(item.containerStatuses)) {
    const totalContainers = item.containerStatuses.length;
    const readyContainers = item.containerStatuses.filter((cs: any) => {
      if (cs.ready) return true;
      if (cs.state?.terminated?.reason === 'Completed') return true;
      return false;
    }).length;

    const isAllReady =
      readyContainers === totalContainers && totalContainers > 0;
    return (
      <span className={`ready-badge ${isAllReady ? 'success' : 'danger'}`}>
        {isAllReady ? 'Ready' : 'Not Ready'}
      </span>
    );
  }

  if (
    item.readyReplicas !== undefined ||
    item.statusReplicas !== undefined ||
    item.replicas !== undefined
  ) {
    const ready = item.readyReplicas ?? 0;
    const total = item.statusReplicas ?? item.replicas ?? 0;
    const isAllReady = ready === total && total > 0;
    const tone = isAllReady ? 'success' : ready > 0 ? 'warning' : 'danger';
    return (
      <span className={`ready-badge ${tone}`}>
        {ready}/{total}
      </span>
    );
  }

  if (
    item.numberReady !== undefined &&
    item.desiredNumberScheduled !== undefined
  ) {
    const isAllReady =
      item.numberReady === item.desiredNumberScheduled &&
      item.desiredNumberScheduled > 0;
    const tone = isAllReady
      ? 'success'
      : item.numberReady > 0
        ? 'warning'
        : 'danger';
    return (
      <span className={`ready-badge ${tone}`}>
        {item.numberReady}/{item.desiredNumberScheduled}
      </span>
    );
  }

  if (item.conditions) {
    const readyCondition = Array.isArray(item.conditions)
      ? item.conditions.find((c: any) => c.type === 'Ready')
      : null;
    const isReady = readyCondition?.status === 'True';
    return (
      <span className={`ready-badge ${isReady ? 'success' : 'danger'}`}>
        {isReady ? 'True' : 'False'}
      </span>
    );
  }

  return '-';
};

export const PrinterValueBadge = ({
  value,
  type,
}: {
  value?: string;
  type?: string;
}): React.ReactElement | string => {
  if (value === undefined || value === null || value === '') return '-';
  if (String(type).toLowerCase() === 'date') {
    return React.createElement(LiveAge, { timestamp: value });
  }
  const lower = String(value).toLowerCase();
  if (lower === 'true' || lower === 'false' || lower === 'unknown') {
    const tone: BadgeTone =
      lower === 'true' ? 'success' : lower === 'unknown' ? 'warning' : 'danger';
    return (
      <span className={`status-badge ${tone}`} title={value}>
        {value}
      </span>
    );
  }
  return value;
};
