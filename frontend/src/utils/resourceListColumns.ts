import { kindSpecificColumns } from '../store/utils';

export type PrinterColumnCell = {
  name: string;
  type?: string;
  value?: string;
  priority?: number;
};

const IDENTITY_COLUMNS = new Set(['NAME', 'NAMESPACE']);

export function printerCellFor(
  item: { printerColumns?: PrinterColumnCell[] } | null | undefined,
  column: string,
): PrinterColumnCell | undefined {
  const cells = item?.printerColumns;
  if (!Array.isArray(cells) || !column) return undefined;
  const wanted = column.toUpperCase();
  return cells.find((cell) => String(cell?.name ?? '').toUpperCase() === wanted);
}

export function printerCellValue(
  item: { printerColumns?: PrinterColumnCell[] } | null | undefined,
  column: string,
): string | undefined {
  const cell = printerCellFor(item, column);
  return cell?.value;
}

export function printerColumnsFromItems(
  items: Array<{ printerColumns?: PrinterColumnCell[] }> | null | undefined,
): PrinterColumnCell[] | undefined {
  if (!items) return undefined;
  for (const item of items) {
    if (Array.isArray(item?.printerColumns) && item.printerColumns.length > 0) {
      return item.printerColumns;
    }
  }
  return undefined;
}

export function columnsFromPrinterCells(
  cells: PrinterColumnCell[] | undefined,
  namespaced: boolean,
): string[] | null {
  if (!cells || cells.length === 0) return null;
  const visible = cells.filter((cell) => (cell.priority ?? 0) === 0);
  if (visible.length === 0) return null;

  const columns: string[] = ['NAME'];
  if (namespaced) columns.push('NAMESPACE');

  let hasAge = false;
  for (const cell of visible) {
    const name = String(cell.name || '').toUpperCase();
    if (!name || IDENTITY_COLUMNS.has(name)) continue;
    if (name === 'AGE') {
      hasAge = true;
      continue;
    }
    columns.push(name);
  }
  columns.push('AGE');
  if (!hasAge && columns.length === (namespaced ? 3 : 2)) {
    // Only NAME/NAMESPACE/AGE — treat as no useful printer columns.
    return null;
  }
  return columns;
}

export function resolveListColumns(opts: {
  kind?: string;
  namespaced?: boolean;
  printerColumns?: PrinterColumnCell[] | null;
}): string[] {
  const kind = opts.kind || '';
  const namespaced = opts.namespaced ?? true;
  if (!kind) return ['NAME', 'STATUS', 'AGE'];

  const normalized = kind.toLowerCase();
  if (normalized === 'event' || normalized === 'events') {
    return [
      'TYPE',
      'MESSAGE',
      'NAMESPACE',
      'INVOLVED OBJECT',
      'SOURCE',
      'COUNT',
      'AGE',
      'LAST SEEN',
    ];
  }

  const base = namespaced ? ['NAME', 'NAMESPACE'] : ['NAME'];
  const specific = kindSpecificColumns[normalized];
  if (specific) return [...base, ...specific];

  const fromPrinter = columnsFromPrinterCells(opts.printerColumns || undefined, namespaced);
  if (fromPrinter) return fromPrinter;

  return [...base, 'STATUS', 'AGE'];
}
