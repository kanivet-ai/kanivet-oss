import React, { useState } from 'react';
import { CheckIcon, Cross2Icon, ExclamationTriangleIcon, MinusIcon, ChevronRightIcon } from '@radix-ui/react-icons';
import ResourceLink from '../ResourceLink';
import type { Hop, HopStatus, ObjectRef } from '../../types/network';
import { hopTone } from './networkUtils';

/**
 * A self-contained path diagram: a compact strip of hops joined by
 * connectors coloured by the hop they lead into, and below it each hop with
 * its explanation. It depends only on the hop list, so other views (or a
 * topology graph later) can reuse it.
 */

const HOP_LABEL: Record<Hop['kind'], string> = {
  source: 'Source',
  egress: 'Egress',
  service: 'Service',
  endpoints: 'Endpoints',
  ingress: 'Ingress',
  port: 'Port',
  destination: 'Destination',
};

export const StatusGlyph: React.FC<{ status: HopStatus; size?: number }> = ({ status, size = 16 }) => {
  const icon = status === 'pass' ? <CheckIcon /> : status === 'fail' ? <Cross2Icon /> : status === 'warn' ? <ExclamationTriangleIcon /> : <MinusIcon />;
  return (
    <span className={`net-glyph net-glyph--${hopTone(status)}`} style={{ width: size, height: size }} aria-label={status}>
      {icon}
    </span>
  );
};

const ObjLink: React.FC<{ cluster: string; obj: ObjectRef; children?: React.ReactNode }> = ({ cluster, obj, children }) => (
  <ResourceLink cluster={cluster} apiVersion={obj.apiVersion} kind={obj.kind} name={obj.name} namespace={obj.namespace}>
    {children || obj.name}
  </ResourceLink>
);

export interface PathDiagramProps {
  hops: Hop[];
  cluster: string;
  /** Hop ids expanded at first (defaults to the failing and warning ones). */
  initiallyOpen?: string[];
}

const PathDiagram: React.FC<PathDiagramProps> = ({ hops, cluster, initiallyOpen }) => {
  const [open, setOpen] = useState<Set<string>>(
    () => new Set(initiallyOpen ?? hops.filter((h) => h.status === 'fail' || h.status === 'warn').map((h) => h.id)),
  );
  const toggle = (id: string) => setOpen((prev) => {
    const next = new Set(prev);
    if (next.has(id)) next.delete(id); else next.add(id);
    return next;
  });
  // After the first failing hop, the rest are not reached.
  const failAt = hops.findIndex((h) => h.status === 'fail');

  return (
    <div className="net-path">
      <ol className="net-strip" aria-label="Path">
        {hops.map((h, i) => (
          <li key={h.id} className={`net-strip-item${failAt >= 0 && i > failAt ? ' net-strip-item--unreached' : ''}`}>
            {i > 0 && <span className={`net-strip-link net-strip-link--${hopTone(h.status)}`} aria-hidden="true" />}
            <button type="button" className="net-strip-node" onClick={() => toggle(h.id)} title={h.detail}>
              <StatusGlyph status={h.status} size={22} />
              <span className="net-strip-label">{HOP_LABEL[h.kind] || h.kind}</span>
            </button>
          </li>
        ))}
      </ol>

      <div className="net-hops ap-card">
        {hops.map((h, i) => {
          const expanded = open.has(h.id);
          const hasMore = (h.reasons?.length || 0) > 0 || (h.policies?.length || 0) > 0;
          return (
            <div key={h.id} className={`net-hop net-hop--${hopTone(h.status)}${failAt >= 0 && i > failAt ? ' net-hop--unreached' : ''}`}>
              <div className="net-hop-rail" aria-hidden="true">
                <StatusGlyph status={h.status} />
                {i < hops.length - 1 && <span className="net-hop-line" />}
              </div>
              <div className="net-hop-body">
                <div className="net-hop-head">
                  <span className="net-hop-kind">{HOP_LABEL[h.kind] || h.kind}</span>
                  <span className="net-hop-title">
                    {h.objects && h.objects.length > 0 && h.kind !== 'egress' && h.kind !== 'ingress'
                      ? <ObjLink cluster={cluster} obj={h.objects[0]}>{h.title}</ObjLink>
                      : h.title}
                  </span>
                  {hasMore && (
                    <button type="button" className={`net-hop-toggle${expanded ? ' is-open' : ''}`} onClick={() => toggle(h.id)} aria-expanded={expanded} aria-label={expanded ? 'Hide details' : 'Show details'}>
                      <ChevronRightIcon />
                    </button>
                  )}
                </div>
                <div className="net-hop-detail">
                  {h.detail}
                  {h.allowedBy && h.allowedBy.length > 0 && (
                    <span className="net-hop-rules">
                      {h.allowedBy.slice(0, 4).map((r) => (
                        <span key={`${r.policy.namespace}/${r.policy.name}/${r.index}`} className="ap-badge ap-badge--sm ap-badge--success">
                          <ObjLink cluster={cluster} obj={r.policy}>{r.policy.name}</ObjLink>&nbsp;rule {r.index}
                        </span>
                      ))}
                    </span>
                  )}
                </div>
                {expanded && hasMore && (
                  <div className="net-hop-more">
                    {h.policies && h.policies.length > 0 && (
                      <div className="net-hop-policies">
                        <span className="net-muted">Policies selecting these pods:</span>
                        {h.policies.map((p) => (
                          <ObjLink key={`${p.namespace}/${p.name}`} cluster={cluster} obj={p} />
                        ))}
                      </div>
                    )}
                    {h.reasons && h.reasons.length > 0 && (
                      <ul className="net-reasons">
                        {h.reasons.map((r, j) => <li key={j}>{r}</li>)}
                      </ul>
                    )}
                  </div>
                )}
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
};

export default PathDiagram;
