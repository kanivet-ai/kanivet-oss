import { useState, useCallback, useMemo } from 'react';

try {
  Object.keys(localStorage)
    .filter((k) => k.startsWith('kanivet.columnWidths'))
    .forEach((k) => localStorage.removeItem(k));
} catch {}

interface UseResizableColumnsProps {
  columns: string[];
  resourceKind: string;
  autoWidths?: ColumnWidths;
  minWidth?: number;
}

interface ColumnWidths {
  [key: string]: number;
}

export function useResizableColumns({
  columns,
  autoWidths,
  minWidth = 40,
}: UseResizableColumnsProps) {
  const [overrides, setOverrides] = useState<ColumnWidths>({});

  const columnWidths = useMemo(() => {
    const result: ColumnWidths = {};
    for (const col of columns) {
      result[col] = overrides[col] ?? autoWidths?.[col] ?? getDefaultColumnWidth(col);
    }
    return result;
  }, [columns, overrides, autoWidths]);

  const startResize = useCallback(
    (column: string, event: React.MouseEvent, _nextColumn?: string) => {
      event.preventDefault();
      const startX = event.clientX;

      const th = (event.target as HTMLElement).closest('th');
      const headerRow = th?.closest('tr');

      const allRenderedWidths: ColumnWidths = {};
      if (headerRow) {
        const allThs = headerRow.querySelectorAll('th.resizable-column');
        allThs.forEach((thEl, idx) => {
          if (columns[idx]) {
            allRenderedWidths[columns[idx]] = thEl.getBoundingClientRect().width;
          }
        });
      }

      const startWidth = allRenderedWidths[column] || columnWidths[column];

      setOverrides((prev) => ({ ...prev, ...allRenderedWidths }));

      const handleMouseMove = (e: MouseEvent) => {
        const delta = e.clientX - startX;
        const newWidth = Math.max(minWidth, startWidth + delta);
        setOverrides((prev) => ({ ...prev, [column]: newWidth }));
      };

      const handleMouseUp = () => {
        document.removeEventListener('mousemove', handleMouseMove);
        document.removeEventListener('mouseup', handleMouseUp);
      };

      document.addEventListener('mousemove', handleMouseMove);
      document.addEventListener('mouseup', handleMouseUp);
    },
    [columns, columnWidths, minWidth],
  );

  const resetToDefaults = useCallback(() => {
    setOverrides({});
  }, []);

  const hasOverrides = Object.keys(overrides).length > 0;

  return {
    columnWidths,
    startResize,
    resetToDefaults,
    hasOverrides,
  };
}

function getDefaultColumnWidth(column: string): number {
  const widthMap: Record<string, number> = {
    NAME: 200,
    NAMESPACE: 120,
    AGE: 60,

    STATUS: 100,
    READY: 50,
    PHASE: 100,
    RESTARTS: 80,

    NODE: 150,
    'NOMINATED NODE': 150,
    CONTAINERS: 150,
    'SERVICE ACCOUNT': 150,
    REPLICAS: 80,
    STRATEGY: 120,
    'UPDATE STRATEGY': 120,
    SELECTOR: 200,
    TYPE: 100,
    'CLUSTER-IP': 120,
    'EXTERNAL-IP': 120,
    PORTS: 120,
    'PORT(S)': 120,

    LABELS: 250,
    ANNOTATIONS: 250,
    OWNER: 150,

    DATA: 80,
    'BINARY DATA': 100,
    CAPACITY: 100,
    'ACCESS MODES': 120,
    'ACCESS-MODES': 120,
    'RECLAIM POLICY': 120,
    'STORAGE CLASS': 120,
    'STORAGE-CLASS': 120,
    VOLUME: 150,
    CLAIM: 150,

    ROLES: 100,
    TAINTS: 150,
    VERSION: 100,
    'INTERNAL-IP': 120,
    'OS-IMAGE': 150,
    'KERNEL-VERSION': 120,
    'CONTAINER-RUNTIME': 150,
    'ALLOCATABLE CPU': 120,
    'ALLOCATABLE MEMORY': 120,

    COMPLETIONS: 100,
    PARALLELISM: 100,
    'BACKOFF LIMIT': 100,
    DURATION: 100,
    SCHEDULE: 120,
    SUSPEND: 80,
    ACTIVE: 80,
    'LAST SCHEDULE': 120,
    'LAST-SCHEDULE': 120,

    'SERVICE NAME': 150,
    DESIRED: 80,
    CURRENT: 80,
    'UP-TO-DATE': 100,
    AVAILABLE: 100,
    'POD STATUS': 150,
    REVISION: 80,

    PRIORITY: 80,
    QOS: 80,
    IP: 120,
    SECRETS: 80,
    'AUTOMOUNT TOKEN': 120,
    'SESSION AFFINITY': 120,
    CLASS: 100,
    HOSTS: 200,
    ADDRESS: 150,
  };

  return widthMap[column] || 80;
}
