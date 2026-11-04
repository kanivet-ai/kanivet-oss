import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import api from '../../services/api';
import { apiClient } from '../../services/api/client';
import * as rbacApi from '../../services/api/rbac';
import type {
  GraphInfo,
  PermissionMatrix,
  RbacRisk,
  SelfAccess,
  SubjectRef,
  SubjectSummary,
  WhoCanResult,
} from '../../types/rbac';
import { getErrorMessage } from '../../utils/errorMessage';
import { AccessMode, useAccessIntent } from './accessIntent';
import { COMMON_RESOURCES, parseResourceInput, scopeLabel, subjectKey, subjectLabel, VERBS } from './accessUtils';
import PathList from './PathList';
import PermissionMatrixView from './PermissionMatrixView';
import RefLink from './RefLink';
import RiskList from './RiskList';
import './Access.css';

interface Props {
  cluster: string;
}

const MODES: { id: AccessMode; label: string }[] = [
  { id: 'who-can', label: 'Who can…' },
  { id: 'subject', label: 'Subject' },
  { id: 'mine', label: 'My access' },
  { id: 'risks', label: 'Risky grants' },
];

type Load<T> = { data?: T; loading: boolean; error?: string };

function useLoad<T>(fn: (() => Promise<T>) | null, deps: unknown[]): Load<T> & { reload: () => void } {
  const [state, setState] = useState<Load<T>>({ loading: false });
  const [tick, setTick] = useState(0);
  const seq = useRef(0);
  useEffect(() => {
    if (!fn) {
      setState({ loading: false });
      return;
    }
    const mine = ++seq.current;
    setState((s) => ({ data: s.data, loading: true }));
    fn().then(
      (data) => mine === seq.current && setState({ data, loading: false }),
      (err) => mine === seq.current && setState({ loading: false, error: getErrorMessage(err, 'Request failed') }),
    );
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, tick]);
  return { ...state, reload: () => setTick((t) => t + 1) };
}

const NamespacePicker: React.FC<{ value: string; onChange: (v: string) => void; namespaces: string[]; allLabel?: string }> = ({
  value,
  onChange,
  namespaces,
  allLabel = 'All namespaces',
}) => (
  <select className="ap-select access-select" value={value} onChange={(e) => onChange(e.target.value)} aria-label="Namespace">
    <option value="">{allLabel}</option>
    {namespaces.map((n) => (
      <option key={n} value={n}>{n}</option>
    ))}
  </select>
);

const State: React.FC<{ loading: boolean; error?: string; hasData: boolean; onRetry: () => void }> = ({ loading, error, hasData, onRetry }) => {
  if (error) {
    return (
      <div className="access-empty">
        <div>{error}</div>
        <button type="button" className="ap-btn ap-btn--sm" onClick={onRetry}>Try again</button>
      </div>
    );
  }
  if (loading && !hasData) return <div className="access-empty"><span className="ap-spinner" /> Loading…</div>;
  return null;
};

const AccessPage: React.FC<Props> = ({ cluster }) => {
  const intent = useAccessIntent((s) => s.intent);
  const [mode, setMode] = useState<AccessMode>(intent.mode);
  const [namespaces, setNamespaces] = useState<string[]>([]);
  const [refreshKey, setRefreshKey] = useState(0);

  // Who can…
  const [verb, setVerb] = useState('get');
  const [resourceText, setResourceText] = useState('secrets');
  const [whoNs, setWhoNs] = useState('');
  const [objName, setObjName] = useState('');
  const [expanded, setExpanded] = useState<Set<string>>(new Set());

  // Subject
  const [subject, setSubject] = useState<SubjectRef | undefined>(intent.subject);
  const [subjectNs, setSubjectNs] = useState(intent.namespace || '');
  const [subjectFilter, setSubjectFilter] = useState('');
  const [kindFilter, setKindFilter] = useState<'all' | 'ServiceAccount' | 'User' | 'Group'>('all');

  // Mine / risks
  const [mineNs, setMineNs] = useState('default');
  const [includeSystem, setIncludeSystem] = useState(false);
  const [riskNs, setRiskNs] = useState('');

  // Follow requests from the palette and inspector links, even while open.
  useEffect(() => {
    if (intent.seq === 0) return;
    setMode(intent.mode);
    if (intent.subject) setSubject(intent.subject);
    if (intent.namespace !== undefined) {
      if (intent.mode === 'mine') setMineNs(intent.namespace || 'default');
      else setSubjectNs(intent.namespace);
    }
  }, [intent.seq]);

  useEffect(() => {
    if (!cluster) return;
    api.getNamespaces(cluster).then(setNamespaces, () => setNamespaces([]));
  }, [cluster]);

  const summary = useLoad<{ subjects: SubjectSummary[]; info: GraphInfo }>(
    cluster ? () => rbacApi.getRbacSummary(cluster) : null,
    [cluster, refreshKey],
  );

  const whoQuery = useMemo(() => {
    const parsed = parseResourceInput(resourceText);
    if (!parsed.resource || !verb) return null;
    const known = COMMON_RESOURCES.find((o) => o.resource === (parsed.subresource ? `${parsed.resource}/${parsed.subresource}` : parsed.resource) && !parsed.group);
    return {
      verb,
      resource: parsed.resource,
      subresource: parsed.subresource,
      group: parsed.group ?? (known ? known.group : undefined),
      namespace: whoNs || undefined,
      name: objName.trim() || undefined,
    };
  }, [verb, resourceText, whoNs, objName]);

  const [debouncedQuery, setDebouncedQuery] = useState(whoQuery);
  useEffect(() => {
    const t = setTimeout(() => setDebouncedQuery(whoQuery), 250);
    return () => clearTimeout(t);
  }, [whoQuery]);

  const who = useLoad<WhoCanResult>(
    mode === 'who-can' && cluster && debouncedQuery ? () => rbacApi.whoCan(cluster, debouncedQuery) : null,
    [mode, cluster, JSON.stringify(debouncedQuery), refreshKey],
  );
  const canMe = useLoad<{ allowed: boolean }>(
    mode === 'who-can' && cluster && debouncedQuery ? () => rbacApi.canI(cluster, debouncedQuery) : null,
    [mode, cluster, JSON.stringify(debouncedQuery)],
  );
  const subjectMatrix = useLoad<PermissionMatrix>(
    mode === 'subject' && cluster && subject ? () => rbacApi.getSubjectPermissions(cluster, subject, subjectNs || undefined) : null,
    [mode, cluster, subject && subjectKey(subject), subjectNs, refreshKey],
  );
  const mine = useLoad<SelfAccess>(
    mode === 'mine' && cluster ? () => rbacApi.getSelfAccess(cluster, mineNs) : null,
    [mode, cluster, mineNs, refreshKey],
  );
  const risks = useLoad<RbacRisk[]>(
    mode === 'risks' && cluster ? () => rbacApi.getRiskyGrants(cluster, { includeSystem, namespace: riskNs || undefined }) : null,
    [mode, cluster, includeSystem, riskNs, refreshKey],
  );

  const refresh = useCallback(async () => {
    try {
      await apiClient.getAxios().post('/rbac/refresh', null, { params: { cluster } });
    } catch {
      // A failed invalidation still re-reads the cached graph.
    }
    setRefreshKey((k) => k + 1);
  }, [cluster]);

  const showSubject = useCallback((s: SubjectRef, ns?: string) => {
    setSubject(s);
    if (ns !== undefined) setSubjectNs(ns);
    setMode('subject');
  }, []);

  const subjects = summary.data?.subjects || [];
  const filteredSubjects = useMemo(() => {
    const t = subjectFilter.trim().toLowerCase();
    return subjects.filter(
      (s) => (kindFilter === 'all' || s.subject.kind === kindFilter) && (!t || subjectLabel(s.subject).toLowerCase().includes(t)),
    );
  }, [subjects, subjectFilter, kindFilter]);

  const info = summary.data?.info;

  return (
    <div className="access-page">
      <div className="access-toolbar">
        <div className="access-title">Access</div>
        <div className="ap-segmented" role="tablist" aria-label="Access mode">
          {MODES.map((m) => (
            <button key={m.id} type="button" role="tab" aria-selected={mode === m.id} onClick={() => setMode(m.id)}>
              {m.label}
            </button>
          ))}
        </div>
        <div className="access-toolbar-spacer" />
        <button type="button" className="ap-btn ap-btn--sm" onClick={refresh} title="Reload roles and bindings">
          Refresh
        </button>
      </div>

      <div className="access-body">
        {mode === 'who-can' && (
          <div className="access-section">
            <form className="access-query" onSubmit={(e) => e.preventDefault()}>
              <span className="access-query-word">Who can</span>
              <select className="ap-select access-select" value={verb} onChange={(e) => setVerb(e.target.value)} aria-label="Verb">
                {VERBS.map((v) => (
                  <option key={v} value={v}>{v}</option>
                ))}
              </select>
              <input
                className="ap-input access-input"
                list="access-resources"
                value={resourceText}
                onChange={(e) => setResourceText(e.target.value)}
                placeholder="resource, e.g. pods/exec"
                aria-label="Resource"
              />
              <datalist id="access-resources">
                {COMMON_RESOURCES.map((o) => (
                  <option key={o.label} value={o.label} />
                ))}
              </datalist>
              <span className="access-query-word">in</span>
              <NamespacePicker value={whoNs} onChange={setWhoNs} namespaces={namespaces} allLabel="any namespace" />
              <input
                className="ap-input access-input access-input--sm"
                value={objName}
                onChange={(e) => setObjName(e.target.value)}
                placeholder="name (optional)"
                aria-label="Object name"
              />
            </form>
            {canMe.data && (
              <div className={`access-me ${canMe.data.allowed ? 'is-allowed' : 'is-denied'}`}>
                <span className={`ap-dot ${canMe.data.allowed ? 'ap-dot--success' : 'ap-dot--muted'}`} />
                You {canMe.data.allowed ? 'can' : 'cannot'} do this.
              </div>
            )}
            <State loading={who.loading} error={who.error} hasData={!!who.data} onRetry={who.reload} />
            {who.data && !who.error && (
              who.data.subjects.length === 0 ? (
                <div className="access-empty">No subject is allowed this.</div>
              ) : (
                <ul className="access-subject-list">
                  {who.data.subjects.map((s) => {
                    const key = subjectKey(s.subject);
                    const open = expanded.has(key);
                    return (
                      <li key={key} className="access-subject">
                        <div className="access-subject-row">
                          <button
                            type="button"
                            className="access-disclosure"
                            aria-expanded={open}
                            aria-label={open ? 'Hide why' : 'Show why'}
                            onClick={() => setExpanded((prev) => {
                              const next = new Set(prev);
                              if (next.has(key)) next.delete(key); else next.add(key);
                              return next;
                            })}
                          >
                            <span className={`access-chevron${open ? ' is-open' : ''}`} />
                          </button>
                          <span className="access-chip-kind">{s.subject.kind === 'ServiceAccount' ? 'SA' : s.subject.kind}</span>
                          <RefLink cluster={cluster} refObj={s.subject} />
                          <span className={`ap-badge ap-badge--sm ${s.clusterWide ? 'ap-badge--purple' : ''}`}>
                            {s.clusterWide ? 'cluster-wide' : scopeLabel(s.paths[0]?.scope || '')}
                          </span>
                          <span className="access-subject-paths">
                            {s.paths.length} {s.paths.length === 1 ? 'binding' : 'bindings'}
                          </span>
                          <button type="button" className="ap-btn ap-btn--ghost ap-btn--sm" onClick={() => showSubject(s.subject, whoNs)}>
                            Permissions
                          </button>
                        </div>
                        {open && <PathList cluster={cluster} paths={s.paths} />}
                      </li>
                    );
                  })}
                </ul>
              )
            )}
            {who.data?.warnings?.map((w) => <div key={w} className="access-warning">{w}</div>)}
          </div>
        )}

        {mode === 'subject' && (
          <div className="access-split">
            <aside className="access-subject-picker">
              <input
                className="ap-input ap-input--sm"
                placeholder="Find a subject"
                value={subjectFilter}
                onChange={(e) => setSubjectFilter(e.target.value)}
                aria-label="Find a subject"
              />
              <div className="ap-segmented ap-segmented--sm access-kind-filter" role="group" aria-label="Subject kind">
                {(['all', 'ServiceAccount', 'User', 'Group'] as const).map((k) => (
                  <button key={k} type="button" aria-pressed={kindFilter === k} onClick={() => setKindFilter(k)}>
                    {k === 'all' ? 'All' : k === 'ServiceAccount' ? 'SAs' : `${k}s`}
                  </button>
                ))}
              </div>
              <State loading={summary.loading} error={summary.error} hasData={!!summary.data} onRetry={summary.reload} />
              <ul className="access-picker-list" role="listbox" aria-label="Subjects">
                {filteredSubjects.slice(0, 500).map((s) => {
                  const key = subjectKey(s.subject);
                  const sel = subject && subjectKey(subject) === key;
                  return (
                    <li
                      key={key}
                      role="option"
                      aria-selected={!!sel}
                      className={`access-picker-item${sel ? ' is-selected' : ''}`}
                      onClick={() => setSubject(s.subject)}
                    >
                      <span className="access-chip-kind">{s.subject.kind === 'ServiceAccount' ? 'SA' : s.subject.kind}</span>
                      <span className="access-picker-name ap-truncate">{subjectLabel(s.subject)}</span>
                      <span className="access-picker-count ap-tnum">{s.bindings || ''}</span>
                    </li>
                  );
                })}
                {summary.data && filteredSubjects.length === 0 && <li className="access-empty">No subject matches.</li>}
              </ul>
            </aside>
            <section className="access-subject-detail">
              {!subject ? (
                <div className="access-empty">Pick a service account, user or group to see what it can do.</div>
              ) : (
                <>
                  <div className="access-subject-head">
                    <span className="access-chip-kind">{subject.kind}</span>
                    <RefLink cluster={cluster} refObj={subject} />
                    <div className="access-toolbar-spacer" />
                    <span className="access-query-word">Scope</span>
                    <NamespacePicker value={subjectNs} onChange={setSubjectNs} namespaces={namespaces} allLabel="All scopes" />
                  </div>
                  <State loading={subjectMatrix.loading} error={subjectMatrix.error} hasData={!!subjectMatrix.data} onRetry={subjectMatrix.reload} />
                  {subjectMatrix.data && !subjectMatrix.error && (
                    <PermissionMatrixView
                      cluster={cluster}
                      matrix={subjectMatrix.data}
                      emptyText={`No binding grants ${subjectLabel(subject)} anything${subjectNs ? ` in ${subjectNs}` : ''}.`}
                    />
                  )}
                </>
              )}
            </section>
          </div>
        )}

        {mode === 'mine' && (
          <div className="access-section">
            <div className="access-subject-head">
              <span className="access-query-word">What you can do in</span>
              <NamespacePicker value={mineNs} onChange={(v) => setMineNs(v || 'default')} namespaces={namespaces} allLabel="default" />
            </div>
            <State loading={mine.loading} error={mine.error} hasData={!!mine.data} onRetry={mine.reload} />
            {mine.data && !mine.error && (
              <>
                {mine.data.incomplete && (
                  <div className="access-warning">
                    The API server could not list every rule (for example, a webhook authorizer is in use). Grants may be missing.
                  </div>
                )}
                <PermissionMatrixView cluster={cluster} matrix={mine.data.matrix} showBindings={false} showRisks={false} />
              </>
            )}
          </div>
        )}

        {mode === 'risks' && (
          <div className="access-section">
            <div className="access-subject-head">
              <NamespacePicker value={riskNs} onChange={setRiskNs} namespaces={namespaces} />
              <label className="access-toggle">
                <input type="checkbox" className="ap-checkbox" checked={includeSystem} onChange={(e) => setIncludeSystem(e.target.checked)} />
                Include system bindings
              </label>
            </div>
            <State loading={risks.loading} error={risks.error} hasData={!!risks.data} onRetry={risks.reload} />
            {risks.data && !risks.error && (
              <RiskList cluster={cluster} risks={risks.data} onOpenSubject={(s) => showSubject(s, '')} />
            )}
          </div>
        )}
      </div>

      <div className="ap-statusbar">
        {info
          ? `${info.clusterRoles} cluster roles · ${info.roles} roles · ${info.clusterRoleBindings + info.roleBindings} bindings · ${subjects.length} subjects`
          : summary.error || 'Loading roles and bindings…'}
        {info?.warnings?.length ? ` · ${info.warnings.length} kind${info.warnings.length === 1 ? '' : 's'} not readable` : ''}
      </div>
    </div>
  );
};

export default AccessPage;
