import React, { useMemo, useState } from 'react';
import type { MatrixRow, PermissionMatrix } from '../../types/rbac';
import { cellTone, filterRows, pathText, resourceLabel, scopeLabel } from './accessUtils';
import PathList from './PathList';
import RefLink from './RefLink';
import RiskList from './RiskList';

interface Props {
  cluster: string;
  matrix: PermissionMatrix;
  /** Compact: the inspector's narrow column — fewer verbs, no bindings card. */
  compact?: boolean;
  showBindings?: boolean;
  showRisks?: boolean;
  emptyText?: string;
}

const COMPACT_VERBS = ['get', 'list', 'watch', 'create', 'update', 'patch', 'delete'];
const VERB_ABBR: Record<string, string> = {
  get: 'get', list: 'list', watch: 'watch', create: 'create', update: 'update', patch: 'patch',
  delete: 'delete', deletecollection: 'del-all',
};

const TONE_LABEL: Record<string, string> = {
  full: 'Allowed',
  limited: 'Allowed for named objects only',
  wildcard: 'Allowed through a wildcard',
};

const PermissionMatrixView: React.FC<Props> = ({ cluster, matrix, compact, showBindings = true, showRisks = true, emptyText }) => {
  const [filter, setFilter] = useState('');
  const [selected, setSelected] = useState<{ row: string; verb: string } | null>(null);
  const verbs = useMemo(() => {
    if (!compact) return matrix.verbs;
    const extra = matrix.verbs.filter((v) => !COMPACT_VERBS.includes(v) && v !== 'deletecollection' && matrix.rows.some((r) => r.cells[v]?.allowed));
    return [...COMPACT_VERBS, ...extra];
  }, [matrix, compact]);
  const rows = useMemo(() => filterRows(matrix.rows, filter), [matrix.rows, filter]);
  const rowKey = (r: MatrixRow) => `${r.group}|${r.resource}`;
  const selectedRow = selected ? matrix.rows.find((r) => rowKey(r) === selected.row) : undefined;
  const selectedCell = selectedRow && selected ? selectedRow.cells[selected.verb] : undefined;

  const empty = matrix.rows.length === 0 && !(matrix.nonResourceURLs || []).length;

  return (
    <div className={`access-matrix-wrap${compact ? ' is-compact' : ''}`}>
      {showRisks && matrix.risks && matrix.risks.length > 0 && (
        <RiskList cluster={cluster} risks={matrix.risks} compact />
      )}
      {empty ? (
        <div className="access-empty">{emptyText || 'No permissions granted.'}</div>
      ) : (
        <>
          {matrix.rows.length > 8 && (
            <div className="access-matrix-toolbar">
              <input
                className="ap-input ap-input--sm access-matrix-filter"
                placeholder="Filter resources"
                value={filter}
                onChange={(e) => setFilter(e.target.value)}
                aria-label="Filter resources"
              />
            </div>
          )}
          <div className="access-matrix-scroll">
            <table className="access-matrix" role="grid">
              <thead>
                <tr>
                  <th className="access-matrix-res">Resource</th>
                  {verbs.map((v) => (
                    <th key={v} title={v}>{compact ? VERB_ABBR[v] || v : v}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {rows.map((r) => (
                  <tr key={rowKey(r)}>
                    <td className="access-matrix-res" title={resourceLabel(r)}>
                      <span className="ap-mono">{resourceLabel(r)}</span>
                    </td>
                    {verbs.map((v) => {
                      const cell = r.cells[v];
                      const tone = cellTone(r, cell);
                      const isSel = selected?.row === rowKey(r) && selected?.verb === v;
                      const tip = cell?.allowed
                        ? `${TONE_LABEL[tone]}\n${(cell.paths || []).map(pathText).join('\n')}`
                        : `Not allowed: ${v} ${resourceLabel(r)}`;
                      return (
                        <td key={v} className="access-matrix-cell">
                          <button
                            type="button"
                            className={`access-cell access-cell--${tone}${isSel ? ' is-selected' : ''}`}
                            title={tip}
                            aria-label={`${v} ${resourceLabel(r)}: ${cell?.allowed ? TONE_LABEL[tone] : 'not allowed'}`}
                            disabled={!cell?.allowed}
                            onClick={() => setSelected(isSel ? null : { row: rowKey(r), verb: v })}
                          />
                        </td>
                      );
                    })}
                  </tr>
                ))}
                {rows.length === 0 && (
                  <tr>
                    <td colSpan={verbs.length + 1} className="access-empty">No resource matches “{filter}”.</td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
          <div className="access-legend" aria-hidden="true">
            <span><i className="access-cell access-cell--full" /> allowed</span>
            <span><i className="access-cell access-cell--wildcard" /> wildcard</span>
            <span><i className="access-cell access-cell--limited" /> named objects</span>
            <span className="access-legend-hint">Click a cell to see why</span>
          </div>
          {selectedRow && selectedCell?.allowed && selected && (
            <div className="access-provenance">
              <div className="access-provenance-title">
                Why {matrix.subject ? 'this subject' : 'this role'} can <b>{selected.verb}</b>{' '}
                <span className="ap-mono">{resourceLabel(selectedRow)}</span>
              </div>
              <PathList cluster={cluster} paths={selectedCell.paths || []} />
            </div>
          )}
          {(matrix.nonResourceURLs || []).length > 0 && (
            <div className="access-nonres">
              <div className="ap-group-label">Non-resource URLs</div>
              <div className="ap-card">
                {matrix.nonResourceURLs!.map((n) => (
                  <div key={n.url} className="ap-card-row">
                    <span className="ap-mono">{n.url}</span>
                    <span className="ap-card-row-value">{n.verbs.join(', ')}</span>
                  </div>
                ))}
              </div>
            </div>
          )}
        </>
      )}
      {showBindings && matrix.bindings.length > 0 && (
        <div className="access-bindings">
          <div className="ap-group-label">Granted by</div>
          <div className="ap-card">
            {matrix.bindings.map((b, i) => (
              <div key={i} className="ap-card-row access-binding-row">
                {b.binding.kind ? <RefLink cluster={cluster} refObj={b.binding} showKind /> : null}
                <span className="access-arrow" aria-hidden="true">→</span>
                <RefLink cluster={cluster} refObj={b.role} showKind qualified={false} />
                {b.roleMissing && <span className="ap-badge ap-badge--sm ap-badge--warning">role missing</span>}
                <span className="ap-card-row-value">
                  {scopeLabel(b.scope)}
                  {b.via ? ` · as ${b.via}` : ''}
                </span>
              </div>
            ))}
          </div>
        </div>
      )}
      {matrix.warnings?.map((w) => (
        <div key={w} className="access-warning">{w}</div>
      ))}
    </div>
  );
};

export default PermissionMatrixView;
