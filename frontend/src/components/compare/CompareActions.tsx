import React, { useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { MixIcon } from '@radix-ui/react-icons';
import { useStore } from '../../store';
import { getCompareBaselines } from '../../services/api/compare';
import type { CompareBaselines } from '../../types/compare';
import { refFromObject } from '../../utils/compareRefs';
import { openCompare } from './compareLauncher';

/**
 * The inspector's "Compare" action: compare with another resource, with the
 * same resource in another cluster, or with what was declared for it
 * (last-applied or Helm), when that exists.
 */
const CompareActions: React.FC<{ resource: any; cluster: string }> = ({ resource, cluster }) => {
  const [menu, setMenu] = useState<{ x: number; y: number } | null>(null);
  const [baselines, setBaselines] = useState<CompareBaselines | null>(null);
  const clusters = useStore((s) => s.clusters);
  const menuRef = useRef<HTMLDivElement>(null);

  const name = resource?.metadata?.name;
  const kind = resource?.kind;
  const helmHint = !!resource?.metadata?.annotations?.['meta.helm.sh/release-name'];

  useEffect(() => {
    if (!menu || !name || !kind) return;
    const ctrl = new AbortController();
    getCompareBaselines(refFromObject(resource, cluster), ctrl.signal)
      .then(setBaselines)
      .catch(() => { if (!ctrl.signal.aborted) setBaselines({ lastApplied: false }); });
    return () => ctrl.abort();
  }, [menu, name, kind, cluster, resource]);

  useEffect(() => {
    if (!menu) return;
    const onDown = (e: MouseEvent) => { if (menuRef.current && !menuRef.current.contains(e.target as Node)) setMenu(null); };
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') setMenu(null); };
    document.addEventListener('mousedown', onDown);
    document.addEventListener('keydown', onKey);
    return () => { document.removeEventListener('mousedown', onDown); document.removeEventListener('keydown', onKey); };
  }, [menu]);

  if (!name || !kind || !resource?.apiVersion) return null;
  const self = refFromObject(resource, cluster);
  const otherCluster = clusters.find((c) => c !== cluster);

  const run = (fn: () => void) => { setMenu(null); fn(); };

  return (
    <>
      <button
        className="quick-action-btn tab-content-action-btn"
        onClick={(e) => {
          const r = (e.currentTarget as HTMLElement).getBoundingClientRect();
          setMenu(menu ? null : { x: r.left, y: r.bottom + 6 });
        }}
        title="Compare"
        aria-label="Compare"
        aria-haspopup="menu"
        aria-expanded={!!menu}
      >
        <span className="ap-action-icon"><MixIcon /></span>
        <span className="ap-action-label">Compare</span>
      </button>
      {menu && createPortal(
        <div ref={menuRef} className="ap-menu" role="menu" style={{ position: 'fixed', left: menu.x, top: menu.y, zIndex: 1000 }}>
          <div className="ap-menu-item" role="menuitem" onClick={() => run(() => openCompare({ a: self, b: null }))}>
            Compare with…
          </div>
          <div
            className="ap-menu-item"
            role="menuitem"
            aria-disabled={!otherCluster}
            style={otherCluster ? undefined : { opacity: 0.5, pointerEvents: 'none' }}
            onClick={() => otherCluster && run(() => openCompare({ a: self, b: { ...self, cluster: otherCluster } }))}
            title={otherCluster ? 'Same kind and name in another context; change the cluster on side B' : 'Only one cluster is configured'}
          >
            Compare across clusters
          </div>
          <div className="ap-menu-sep" />
          {(!baselines || baselines.lastApplied) && <div
            className="ap-menu-item"
            role="menuitem"
            aria-disabled={!baselines?.lastApplied}
            style={baselines?.lastApplied ? undefined : { opacity: 0.5, pointerEvents: 'none' }}
            onClick={() => run(() => openCompare({ a: { ...self, source: 'last-applied' }, b: self }))}
          >
            Show drift from last applied
            {!baselines && <span className="ap-menu-shortcut">…</span>}
          </div>}
          {(helmHint || baselines?.helmRelease) && (
            <div
              className="ap-menu-item"
              role="menuitem"
              aria-disabled={!baselines?.helmRelease}
              style={baselines?.helmRelease ? undefined : { opacity: 0.5, pointerEvents: 'none' }}
              onClick={() => run(() => openCompare({ a: { ...self, source: 'helm' }, b: self }))}
            >
              Show drift from Helm release
              {baselines?.helmRelease && <span className="ap-menu-shortcut">{baselines.helmRelease}</span>}
            </div>
          )}
        </div>,
        document.body,
      )}
    </>
  );
};

export default CompareActions;
