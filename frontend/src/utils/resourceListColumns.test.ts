import { describe, expect, it } from 'vitest';
import {
  columnsFromPrinterCells,
  printerCellValue,
  resolveListColumns,
} from './resourceListColumns';

const tenantPrinterColumns = [
  { name: 'INFRA', type: 'string', value: 'True', priority: 0 },
  { name: 'APPS', type: 'string', value: 'False', priority: 0 },
  { name: 'APPS-REASON', type: 'string', value: 'AppsPending', priority: 1 },
  { name: 'SYNCED', type: 'string', value: 'True', priority: 0 },
  { name: 'READY', type: 'string', value: 'False', priority: 0 },
  { name: 'COMPOSITION', type: 'string', value: 'tenant-v2', priority: 0 },
  { name: 'AGE', type: 'date', value: '2026-06-07T00:00:00Z', priority: 0 },
];

describe('resolveListColumns', () => {
  it('keeps kind-specific columns for known resources', () => {
    expect(resolveListColumns({ kind: 'Pod', namespaced: true, printerColumns: tenantPrinterColumns })).toEqual([
      'NAME',
      'NAMESPACE',
      'CONTAINERS',
      'STATUS',
      'READY',
      'RESTARTS',
      'AGE',
    ]);
  });

  it('uses CRD printer columns for unknown cluster-scoped CRs', () => {
    expect(resolveListColumns({ kind: 'TenantV2', namespaced: false, printerColumns: tenantPrinterColumns })).toEqual([
      'NAME',
      'INFRA',
      'APPS',
      'SYNCED',
      'READY',
      'COMPOSITION',
      'AGE',
    ]);
  });

  it('hides priority 1 printer columns to match kubectl get, not -o wide', () => {
    const columns = resolveListColumns({
      kind: 'TenantV2',
      namespaced: false,
      printerColumns: tenantPrinterColumns,
    });
    expect(columns).not.toContain('APPS-REASON');
  });

  it('falls back to STATUS and AGE when a CR has no printer columns', () => {
    expect(resolveListColumns({ kind: 'TenantV2', namespaced: false })).toEqual([
      'NAME',
      'STATUS',
      'AGE',
    ]);
  });
});

describe('columnsFromPrinterCells', () => {
  it('ignores NAME and AGE duplicates from the CRD', () => {
    const columns = columnsFromPrinterCells(
      [
        { name: 'Name', value: 'x' },
        { name: 'READY', value: 'True' },
        { name: 'AGE', type: 'date', value: '2026-06-07T00:00:00Z' },
      ],
      false,
    );
    expect(columns).toEqual(['NAME', 'READY', 'AGE']);
  });
});

describe('printerCellValue', () => {
  it('matches printer cells case-insensitively', () => {
    expect(printerCellValue({ printerColumns: tenantPrinterColumns }, 'infra')).toBe('True');
    expect(printerCellValue({ printerColumns: tenantPrinterColumns }, 'COMPOSITION')).toBe('tenant-v2');
  });
});
