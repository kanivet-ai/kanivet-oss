import React, { useEffect, useState } from 'react';
import PropertyGroup from '../detailView/shared/PropertyGroup';
import ResourceLink from '../ResourceLink';
import { getErrorMessage } from '../../utils/errorMessage';
import { getNetworkPolicyDetail, getResourceNetwork } from '../../services/api/network';
import type { DirectionView, ObjectRef, PolicyDetail, ResourceNetwork, RuleView } from '../../types/network';
import { NetworkIcon } from '../icons/kube';
import { openNetworkPage } from './openNetwork';
import './Network.css';

/** Kinds the inspector shows a Network section for. */
export const NETWORK_INSPECTOR_KINDS = new Set(['Pod', 'Deployment', 'StatefulSet', 'DaemonSet', 'ReplicaSet', 'Job', 'Service', 'NetworkPolicy']);

const Link: React.FC<{ cluster: string; obj: ObjectRef; children?: React.ReactNode }> = ({ cluster, obj, children }) => (
  <ResourceLink cluster={cluster} apiVersion={obj.apiVersion} kind={obj.kind} name={obj.name} namespace={obj.namespace}>{children || obj.name}</ResourceLink>
);

const Rules: React.FC<{ cluster: string; rules: RuleView[]; dir: 'ingress' | 'egress'; showPolicy?: boolean }> = ({ cluster, rules, dir, showPolicy = true }) => (
  <div className="net-rules">
    {rules.map((r) => (
      <div key={`${r.rule.policy.name}/${r.rule.index}`} className="net-rule">
        <span className="net-rule-id">
          {showPolicy && <><Link cluster={cluster} obj={r.rule.policy} />{' '}</>}rule {r.rule.index}
        </span>
        <span className="net-rule-text">
          {dir === 'ingress' ? 'from ' : 'to '}{r.peers.join(' or ')} on {r.ports.join(', ')}
          {r.matches >= 0 && <span className="net-muted"> · {r.matches} {r.matches === 1 ? 'workload' : 'workloads'} match</span>}
        </span>
      </div>
    ))}
  </div>
);

const Direction: React.FC<{ cluster: string; title: string; dir: 'ingress' | 'egress'; view: DirectionView }> = ({ cluster, title, dir, view }) => (
  <div className="net-dir">
    <div className="net-dir-head">
      <span className={`ap-dot ap-dot--${view.isolated ? (view.rules.length ? 'warning' : 'danger') : 'success'}`} />
      <span className="net-dir-title">{title}</span>
    </div>
    <div className="net-dir-summary">{view.summary}</div>
    {view.rules.length > 0 && <Rules cluster={cluster} rules={view.rules} dir={dir} />}
    {view.isolated && view.peers.length > 0 && (
      <div className="net-peers">
        {view.peers.slice(0, 30).map((p) => (
          <span key={`${p.workload.namespace}/${p.workload.kind}/${p.workload.name}`} className="net-peer">
            <span className="net-ns">{p.workload.namespace}/</span><Link cluster={cluster} obj={p.workload} />
            <span className="net-muted ap-tnum"> {p.ports.join(', ')}</span>
          </span>
        ))}
        {(view.peers.length > 30 || view.peersTruncated) && <span className="net-muted">and more</span>}
      </div>
    )}
    {view.externalCidrs && view.externalCidrs.length > 0 && (
      <div className="net-muted">Addresses: {view.externalCidrs.join('; ')}</div>
    )}
  </div>
);

const ResourceBody: React.FC<{ cluster: string; data: ResourceNetwork; kind: string; namespace: string; name: string }> = ({ cluster, data, kind, namespace, name }) => (
  <div className="net-inspector">
    {data.warnings?.map((w, i) => <div key={i} className="net-banner net-banner--warning">{w}</div>)}
    {data.services.length > 0 && (
      <div className="net-svc-ports">
        {data.services.map((s) => (
          <div key={s.service.name} className="net-svc">
            {kind !== 'Service' && <div className="net-dir-title">Exposed by <Link cluster={cluster} obj={s.service} /> <span className="net-muted">{s.type}</span></div>}
            {s.ports.map((p) => (
              <div key={`${p.port}/${p.protocol}`} className="net-portmap">
                <span className={`ap-dot ap-dot--${p.exposed ? 'success' : p.resolvedPort ? 'warning' : 'danger'}`} />
                <span className="ap-tnum net-portmap-arrow">{p.port}/{p.protocol}{p.name ? ` (${p.name})` : ''} → {p.targetPort}{p.resolvedPort && String(p.resolvedPort) !== p.targetPort ? ` = ${p.resolvedPort}` : ''}</span>
                <span className="net-muted ap-tnum">{p.readyEndpoints}/{p.totalEndpoints} ready</span>
                <span className="net-portmap-detail">{p.detail}</span>
              </div>
            ))}
          </div>
        ))}
      </div>
    )}
    <Direction cluster={cluster} title="What can reach it" dir="ingress" view={data.ingress} />
    <Direction cluster={cluster} title="What it can reach" dir="egress" view={data.egress} />
    <div className="net-inspector-actions">
      {kind !== 'Service' && (
        <button type="button" className="ap-btn ap-btn--sm" onClick={() => void openNetworkPage(cluster, { view: 'path', source: { kind, namespace, name } })}>
          Check a path from here
        </button>
      )}
      <button type="button" className="ap-btn ap-btn--sm ap-btn--ghost" onClick={() => void openNetworkPage(cluster, { view: 'path', destination: { kind, namespace, name } })}>
        Check a path to here
      </button>
    </div>
  </div>
);

const PolicyBody: React.FC<{ cluster: string; data: PolicyDetail }> = ({ cluster, data }) => (
  <div className="net-inspector">
    {data.warnings?.map((w, i) => <div key={i} className="net-banner net-banner--warning">{w}</div>)}
    <div className="net-dir">
      {data.summary.map((s, i) => <div key={i} className={i === 0 ? 'net-dir-summary' : 'net-muted'}>{s}</div>)}
    </div>
    <div className="net-dir">
      <div className="net-dir-title">Selected workloads ({data.selectedWorkloads.length})</div>
      {data.selectedWorkloads.length === 0 ? (
        <div className="net-muted">No running pods match the selector {data.selector}.</div>
      ) : (
        <div className="net-peers">
          {data.selectedWorkloads.slice(0, 40).map((w) => (
            <span key={`${w.kind}/${w.name}`} className="net-peer"><span className="net-kind">{w.kind}</span><Link cluster={cluster} obj={w} /></span>
          ))}
        </div>
      )}
    </div>
    {data.policyTypes.includes('Ingress') && (
      <div className="net-dir">
        <div className="net-dir-title">Ingress allowed</div>
        {data.ingress.length === 0 ? <div className="net-muted">Nothing: denies all ingress.</div> : <Rules cluster={cluster} rules={data.ingress} dir="ingress" showPolicy={false} />}
      </div>
    )}
    {data.policyTypes.includes('Egress') && (
      <div className="net-dir">
        <div className="net-dir-title">Egress allowed</div>
        {data.egress.length === 0 ? <div className="net-muted">Nothing: denies all egress.</div> : <Rules cluster={cluster} rules={data.egress} dir="egress" showPolicy={false} />}
      </div>
    )}
    <div className="net-inspector-actions">
      <button type="button" className="ap-btn ap-btn--sm ap-btn--ghost" onClick={() => void openNetworkPage(cluster, { view: 'coverage', namespace: data.policy.namespace })}>
        Namespace coverage
      </button>
    </div>
  </div>
);

interface Props {
  resource: any;
  cluster: string;
}

/** The inspector's Network section for pods, workloads, Services and NetworkPolicies. */
const NetworkInspectorSection: React.FC<Props> = ({ resource, cluster }) => {
  const kind: string = resource?.kind;
  const namespace: string = resource?.metadata?.namespace || '';
  const name: string = resource?.metadata?.name || '';
  const rv: string = resource?.metadata?.resourceVersion || '';
  const [open, setOpen] = useState(false);
  const [data, setData] = useState<ResourceNetwork | PolicyDetail | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!open || !NETWORK_INSPECTOR_KINDS.has(kind) || !name) return;
    let live = true;
    setError(null);
    const req = kind === 'NetworkPolicy'
      ? getNetworkPolicyDetail(cluster, namespace, name)
      : getResourceNetwork(cluster, { kind, namespace, name });
    req.then((d) => { if (live) setData(d); })
      .catch((e) => { if (live) setError(getErrorMessage(e, 'Could not analyse the network')); });
    return () => { live = false; };
  }, [open, cluster, kind, namespace, name, rv]);

  if (!NETWORK_INSPECTOR_KINDS.has(kind)) return null;

  return (
    <>
      <PropertyGroup title="Network" icon={<NetworkIcon />} persistKey="network" defaultOpen={kind === 'NetworkPolicy'} onOpenChange={setOpen}>
        <OpenProbe onMount={() => setOpen(true)} />
        {error ? (
          <div className="net-banner net-banner--danger">{error}</div>
        ) : !data ? (
          <div className="net-muted net-inspector-loading">Analysing policies…</div>
        ) : kind === 'NetworkPolicy' ? (
          <PolicyBody cluster={cluster} data={data as PolicyDetail} />
        ) : (
          <ResourceBody cluster={cluster} data={data as ResourceNetwork} kind={kind} namespace={namespace} name={name} />
        )}
      </PropertyGroup>
      <div className="section-divider" />
    </>
  );
};

/** Rendered only while the group's content is mounted, i.e. open: starts the fetch lazily. */
const OpenProbe: React.FC<{ onMount: () => void }> = ({ onMount }) => {
  useEffect(() => { onMount(); }, [onMount]);
  return null;
};

export default NetworkInspectorSection;
