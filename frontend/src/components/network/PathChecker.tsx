import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { LightningBoltIcon, UpdateIcon } from '@radix-ui/react-icons';
import Dialog from '../common/Dialog';
import Combobox from '../common/Combobox';
import ClipboardCopy from '../common/ClipboardCopy';
import { getErrorMessage } from '../../utils/errorMessage';
import { checkNetworkPath, runNetworkProbe } from '../../services/api/network';
import type { NetRef, PathResult, ProbeResult, Target, Targets } from '../../types/network';
import PathDiagram from './PathDiagram';
import TargetPicker from './TargetPicker';
import { matchTarget, parseReachQuery, verdictText, type NetworkIntent } from './networkUtils';

interface PathCheckerProps {
  cluster: string;
  targets: Targets | null;
  intent: NetworkIntent | null;
  onIntentUsed: () => void;
}

const portsOf = (t?: Target) => (t?.ports || []).map((p, i) => ({ port: p, name: t?.portNames?.[i] }));

const PathChecker: React.FC<PathCheckerProps> = ({ cluster, targets, intent, onIntentUsed }) => {
  const [source, setSource] = useState<NetRef | null>(null);
  const [dest, setDest] = useState<NetRef | null>(null);
  const [destTarget, setDestTarget] = useState<Target | undefined>();
  const [port, setPort] = useState('');
  const [protocol, setProtocol] = useState('TCP');
  const [result, setResult] = useState<PathResult | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [intentNote, setIntentNote] = useState<string | null>(null);
  const [probeOpen, setProbeOpen] = useState(false);
  const [mimic, setMimic] = useState(true);
  const [probing, setProbing] = useState(false);
  const [probe, setProbe] = useState<ProbeResult | null>(null);
  const [probeError, setProbeError] = useState<string | null>(null);
  const autoRun = useRef(false);
  const request = useRef(0);

  const list = targets?.targets || [];
  const findTarget = useCallback((r: NetRef | null) => list.find((t) => t.ref.kind === r?.kind && t.ref.namespace === r?.namespace && t.ref.name === r?.name), [list]);

  const run = useCallback(async (src = source, dst = dest, p = port, proto = protocol) => {
    if (!src || !dst) return;
    const id = ++request.current;
    setLoading(true);
    setError(null);
    setProbe(null);
    setProbeError(null);
    try {
      const res = await checkNetworkPath(cluster, { source: src, destination: dst, port: Number(p) || 0, protocol: proto });
      if (id !== request.current) return;
      setResult(res);
      if (!p && res.port) setPort(String(res.port));
    } catch (e) {
      if (id === request.current) setError(getErrorMessage(e, 'The path check failed'));
    } finally {
      if (id === request.current) setLoading(false);
    }
  }, [cluster, source, dest, port, protocol]);

  // Apply an intent from the palette or an inspector once the targets are in.
  useEffect(() => {
    if (!intent || intent.view !== 'path') return;
    if (intent.query && !targets) return;
    let src = intent.source || null;
    let dst = intent.destination || null;
    let p = intent.port ? String(intent.port) : '';
    setIntentNote(null);
    if (intent.query && targets) {
      const q = parseReachQuery(intent.query);
      if (q) {
        src = matchTarget(targets.targets, q.source, 'source', q.source.namespace);
        dst = matchTarget(targets.targets, q.destination, 'destination', q.destination.namespace || src?.namespace);
        if (q.port) p = String(q.port);
        const missing = [!src && q.source.name, !dst && q.destination.name].filter(Boolean);
        if (missing.length) setIntentNote(`Could not find ${missing.join(' or ')}; pick it below.`);
      }
    }
    setSource(src);
    setDest(dst);
    setDestTarget(findTarget(dst));
    setPort(p);
    setResult(null);
    onIntentUsed();
    autoRun.current = !!(src && dst);
  }, [intent, targets, findTarget, onIntentUsed]);

  useEffect(() => {
    if (autoRun.current && source && dest) {
      autoRun.current = false;
      void run(source, dest, port, protocol);
    }
  }, [source, dest, port, protocol, run]);

  const swap = () => {
    if (dest?.kind === 'Service' || dest?.kind === 'IP') return;
    setSource(dest);
    setDest(source);
    setDestTarget(findTarget(source));
    setResult(null);
  };

  const destPorts = useMemo(() => portsOf(destTarget), [destTarget]);
  const canCheck = !!source && !!dest && !loading;

  const runProbe = async () => {
    if (!source || !dest) return;
    setProbing(true);
    setProbeError(null);
    try {
      const res = await runNetworkProbe(cluster, { source, destination: dest, port: Number(port) || result?.port || 0, mimicLabels: mimic, confirm: true });
      setProbe(res);
      setProbeOpen(false);
    } catch (e) {
      setProbeError(getErrorMessage(e, 'The probe failed'));
    } finally {
      setProbing(false);
    }
  };

  return (
    <div className="net-checker">
      <div className="net-form ap-card">
        <TargetPicker label="From" role="source" targets={list} value={source} onChange={(r) => { setSource(r); setResult(null); }} autoFocus={!intent} />
        <button type="button" className="ap-icon-btn net-swap" onClick={swap} disabled={!source || !dest || dest.kind === 'Service' || dest.kind === 'IP'} aria-label="Swap source and destination" title="Swap">
          <UpdateIcon />
        </button>
        <TargetPicker
          label="To"
          role="destination"
          targets={list}
          value={dest}
          onChange={(r, t) => {
            setDest(r);
            setDestTarget(t);
            setResult(null);
            const ps = portsOf(t);
            if (ps.length === 1) setPort(String(ps[0].port));
            else if (r?.kind !== dest?.kind || r?.name !== dest?.name) setPort('');
          }}
        />
        <div className="net-port">
          <span className="net-picker-label">Port</span>
          <Combobox
            inputClassName="net-port-input ap-tnum"
            inputMode="numeric"
            placeholder="any"
            value={port}
            onChange={setPort}
            sanitize={(v) => v.replace(/[^0-9]/g, '').slice(0, 5)}
            onEnter={() => { if (canCheck) void run(); }}
            options={destPorts.map((p) => ({ value: String(p.port), hint: p.name }))}
            menuMinWidth={140}
            aria-label="Port"
          />
        </div>
        <label className="net-proto">
          <span className="net-picker-label">Protocol</span>
          <select className="ap-select" value={protocol} onChange={(e) => setProtocol(e.target.value)} aria-label="Protocol">
            <option>TCP</option>
            <option>UDP</option>
            <option>SCTP</option>
          </select>
        </label>
        <button type="button" className="ap-btn ap-btn--primary net-check-btn" disabled={!canCheck} onClick={() => void run()}>
          {loading ? 'Checking…' : 'Check path'}
        </button>
      </div>

      {destPorts.length > 1 && (
        <div className="net-port-chips" role="group" aria-label="Destination ports">
          {destPorts.map((p) => (
            <button key={p.port} type="button" className={`net-chip${String(p.port) === port ? ' is-active' : ''}`} onClick={() => setPort(String(p.port))}>
              {p.port}{p.name ? ` ${p.name}` : ''}
            </button>
          ))}
        </div>
      )}

      {intentNote && <div className="net-banner net-banner--warning">{intentNote}</div>}
      {error && <div className="net-banner net-banner--danger" role="alert">{error}</div>}

      {!result && !loading && !error && (
        <div className="net-empty">
          <div className="net-empty-title">Can one workload reach another?</div>
          <div className="net-empty-text">
            Pick a source and a destination. Kanivet follows the Service to its endpoints and target port and checks
            every NetworkPolicy on the way, both egress and ingress. Each step is explained.
          </div>
        </div>
      )}

      {result && (
        <div className={`net-result${loading ? ' is-stale' : ''}`}>
          <div className={`net-verdict net-verdict--${result.verdict}`}>
            <div className="net-verdict-main">
              <span className="net-verdict-title">{verdictText(result.verdict)}</span>
              <span className="net-verdict-meta ap-tnum">
                {result.protocol}{result.port ? `/${result.port}` : ' any port'}
                {result.pairs > 1 && ` · ${result.pairsAllowed} of ${result.pairs} pod pairs allowed`}
                {result.enforcer && ` · enforced by ${result.enforcer}`}
              </span>
            </div>
            <div className="net-verdict-summary">{result.summary}</div>
            <div className="net-verdict-actions">
              <button type="button" className="ap-btn ap-btn--sm" onClick={() => { setProbeError(null); setProbeOpen(true); }} disabled={!source || !dest || probing}>
                <LightningBoltIcon /> {probing ? 'Probing…' : 'Live probe'}
              </button>
            </div>
          </div>
          {result.warnings?.map((w, i) => <div key={i} className="net-banner net-banner--warning">{w}</div>)}
          {probe && <ProbeCard probe={probe} />}
          {probeError && !probeOpen && <div className="net-banner net-banner--danger" role="alert">{probeError}</div>}
          <PathDiagram key={JSON.stringify(result.hops.map((h) => h.id + h.status))} hops={result.hops} cluster={cluster} />
        </div>
      )}

      <Dialog
        isOpen={probeOpen}
        onClose={() => setProbeOpen(false)}
        onConfirm={() => void runProbe()}
        title="Run a live probe?"
        confirmText="Create probe pod"
        isLoading={probing}
        loadingText="Probing…"
        variant="warning"
      >
        <div className="net-probe-confirm">
          <p>
            This <strong>creates a pod</strong> in <code>{source?.namespace}</code>. The pod tries one TCP connection to the
            destination with <code>nc</code> and is deleted as soon as it has an answer. It runs as non-root with no service
            account token and a 60 second deadline.
          </p>
          <label className="net-toggle-row">
            <input type="checkbox" className="ap-checkbox" checked={mimic} onChange={(e) => setMimic(e.target.checked)} />
            <span>
              Use the source pod's labels, so label-based policies apply to the probe as they do to the source. The probe is
              owned by the source pod and never becomes ready, so no controller adopts it and no Service routes to it.
            </span>
          </label>
          {probeError && <div className="net-banner net-banner--danger">{probeError}</div>}
        </div>
      </Dialog>
    </div>
  );
};

const ProbeCard: React.FC<{ probe: ProbeResult }> = ({ probe }) => {
  const tone = probe.status === 'reached' ? 'success' : probe.status === 'failed' ? 'danger' : 'warning';
  return (
    <div className={`net-probe ap-card net-probe--${tone}`}>
      <div className="net-probe-head">
        <span className={`ap-dot ap-dot--${tone}`} />
        <span className="net-probe-title">
          Live probe: {probe.status === 'reached' ? 'connected' : probe.status === 'failed' ? 'could not connect' : probe.status}
        </span>
        <span className="net-muted ap-tnum">{probe.target}{probe.durationMs ? ` · ${(probe.durationMs / 1000).toFixed(1)}s` : ''}</span>
      </div>
      <div className="net-probe-detail">{probe.detail}</div>
      {probe.podName && (
        <div className="net-muted">
          Pod {probe.namespace}/{probe.podName} {probe.cleaned ? 'was deleted.' : 'could not be deleted. Remove it by hand (label kanivet.io/network-probe).'}
        </div>
      )}
      {probe.status !== 'reached' && (
        <div className="net-probe-cmd">
          <code className="ap-mono">{probe.command}</code>
          <ClipboardCopy text={probe.command} />
        </div>
      )}
    </div>
  );
};

export default PathChecker;
