import React, {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import { ChevronRightIcon, PlayIcon } from '@radix-ui/react-icons';
import { AWSAccessCheck } from '../../types/awsIdentity';
import { simulateAWSAccess } from '../../services/api/awsIdentity';
import { AWSDecisionPill, AWSStatusPill } from './AWSStatusPill';
import { checkSourceLabel } from './awsIdentityUtils';
import {
  ALL_IAM_ACTIONS,
  COMMON_IAM_ACTIONS,
  resourceExampleForAction,
} from './commonIamActions';
import './awsIdentity.css';
import './AWSAccessChecks.css';

interface PendingCheck {
  id: string;
  action: string;
  resource: string;
}

interface Props {
  cluster: string;
  roleArn?: string;
  checks: AWSAccessCheck[];
}

const MAX_SUGGESTIONS = 10;

const ActionCombobox: React.FC<{
  value: string;
  onChange: (value: string) => void;
  onPick: (value: string) => void;
  onSubmit: () => void;
  disabled?: boolean;
  inputRef?: React.RefObject<HTMLInputElement>;
}> = ({ value, onChange, onPick, onSubmit, disabled, inputRef }) => {
  const [open, setOpen] = useState(false);
  const [highlight, setHighlight] = useState(0);
  const rootRef = useRef<HTMLDivElement>(null);

  const suggestions = useMemo(() => {
    const q = value.trim().toLowerCase();
    const pool = q
      ? ALL_IAM_ACTIONS.filter((a) => a.toLowerCase().includes(q))
      : ALL_IAM_ACTIONS;
    return pool.slice(0, MAX_SUGGESTIONS);
  }, [value]);

  useEffect(() => {
    setHighlight(0);
  }, [suggestions]);

  useEffect(() => {
    if (!open) return;
    const onMouseDown = (e: MouseEvent) => {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) {
        setOpen(false);
      }
    };
    document.addEventListener('mousedown', onMouseDown);
    return () => document.removeEventListener('mousedown', onMouseDown);
  }, [open]);

  const pick = (action: string) => {
    onPick(action);
    setOpen(false);
  };

  return (
    <div className="awsid-combobox" ref={rootRef}>
      <input
        ref={inputRef}
        type="text"
        value={value}
        placeholder="s3:GetObject"
        spellCheck={false}
        disabled={disabled}
        onChange={(e) => {
          onChange(e.target.value);
          setOpen(true);
        }}
        onClick={() => setOpen(true)}
        onKeyDown={(e) => {
          if (e.key === 'ArrowDown') {
            e.preventDefault();
            setOpen(true);
            setHighlight((h) => Math.min(h + 1, suggestions.length - 1));
          } else if (e.key === 'ArrowUp') {
            e.preventDefault();
            setHighlight((h) => Math.max(h - 1, 0));
          } else if (e.key === 'Enter') {
            e.preventDefault();
            if (
              open &&
              suggestions[highlight] &&
              suggestions[highlight] !== value
            ) {
              pick(suggestions[highlight]);
            } else {
              setOpen(false);
              onSubmit();
            }
          } else if (e.key === 'Escape') {
            setOpen(false);
          }
        }}
      />
      {open && suggestions.length > 0 && (
        <div className="awsid-combobox-menu" role="listbox">
          {suggestions.map((action, i) => {
            const group = COMMON_IAM_ACTIONS.find((g) =>
              g.actions.includes(action),
            );
            return (
              <button
                type="button"
                key={action}
                role="option"
                aria-selected={i === highlight}
                className={`awsid-combobox-item ${i === highlight ? 'active' : ''}`}
                onMouseEnter={() => setHighlight(i)}
                onMouseDown={(e) => e.preventDefault()}
                onClick={() => pick(action)}
              >
                <span className="awsid-mono">{action}</span>
                {group && <span className="awsid-muted">{group.label}</span>}
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
};

const CheckRow: React.FC<{
  check: AWSAccessCheck;
  expanded: boolean;
  onToggle: () => void;
}> = ({ check, expanded, onToggle }) => {
  const hasDetails =
    (check.matchedStatements?.length || 0) > 0 ||
    (check.missingContextKeys?.length || 0) > 0 ||
    !!check.orgDecision ||
    !!check.boundaryDecision ||
    !!check.error;
  return (
    <>
      <tr
        className={`awsid-check-row ${hasDetails ? 'expandable' : ''} ${
          expanded ? 'expanded' : ''
        }`}
        onClick={hasDetails ? onToggle : undefined}
      >
        <td className="awsid-check-toggle">
          {hasDetails && <ChevronRightIcon />}
        </td>
        <td className="awsid-check-source" title={checkSourceLabel(check)}>
          {checkSourceLabel(check)}
        </td>
        <td className="awsid-mono awsid-check-action">{check.action}</td>
        <td className="awsid-mono awsid-check-resource" title={check.resource}>
          <span className="awsid-truncate">{check.resource}</span>
        </td>
        <td className="awsid-check-decision">
          <AWSDecisionPill decision={check.decision} title={check.error} />
        </td>
      </tr>
      {expanded && hasDetails && (
        <tr className="awsid-check-details-row">
          <td colSpan={5}>
            <div className="awsid-check-details">
              {check.error && (
                <div className="awsid-callout callout-error">{check.error}</div>
              )}
              {check.matchedStatements &&
                check.matchedStatements.length > 0 && (
                  <div className="awsid-kv">
                    <span className="kv-label">Decided by</span>
                    <span className="kv-value">
                      <ul className="awsid-check-statements">
                        {check.matchedStatements.map((m, i) => (
                          <li key={`${m.policyName}-${m.statementIndex}-${i}`}>
                            <span className="awsid-check-statement-policy">
                              {m.policyName}
                            </span>
                            <span className="awsid-muted">
                              {' '}
                              · {m.policyType}
                            </span>
                            <span className="awsid-muted">
                              {' '}
                              · {m.sid || `statement ${m.statementIndex + 1}`}
                            </span>
                            {m.startLine ? (
                              <span className="awsid-muted">
                                {' '}
                                · lines {m.startLine}
                                {m.endLine && m.endLine !== m.startLine
                                  ? `–${m.endLine}`
                                  : ''}
                              </span>
                            ) : null}
                          </li>
                        ))}
                      </ul>
                    </span>
                  </div>
                )}
              {check.decision === 'implicitDeny' &&
                !check.matchedStatements?.length && (
                  <div className="awsid-muted awsid-check-note">
                    No attached, inline, or boundary statement covers this
                    action on this resource.
                  </div>
                )}
              {check.missingContextKeys &&
                check.missingContextKeys.length > 0 && (
                  <div className="awsid-kv">
                    <span className="kv-label">Needs context</span>
                    <span className="kv-value mono">
                      {check.missingContextKeys.join(', ')}
                    </span>
                  </div>
                )}
              {check.orgDecision && (
                <div className="awsid-kv">
                  <span className="kv-label">Organization SCP</span>
                  <span className="kv-value">{check.orgDecision}</span>
                </div>
              )}
              {check.boundaryDecision && (
                <div className="awsid-kv">
                  <span className="kv-label">Permissions boundary</span>
                  <span className="kv-value">{check.boundaryDecision}</span>
                </div>
              )}
            </div>
          </td>
        </tr>
      )}
    </>
  );
};

const AWSAccessChecks: React.FC<Props> = ({ cluster, roleArn, checks }) => {
  const [manualChecks, setManualChecks] = useState<AWSAccessCheck[]>([]);
  const [pending, setPending] = useState<PendingCheck[]>([]);
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [action, setAction] = useState('');
  const [resource, setResource] = useState('');
  const actionInputRef = useRef<HTMLInputElement>(null);
  const [formError, setFormError] = useState<string | null>(null);

  useEffect(() => {
    setManualChecks([]);
    setPending([]);
    setExpanded(new Set());
  }, [roleArn]);

  const allChecks = useMemo(
    () => [...checks, ...manualChecks],
    [checks, manualChecks],
  );

  const toggle = useCallback((id: string) => {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }, []);

  const run = useCallback(async () => {
    const a = action.trim();
    const r = resource.trim();
    if (!roleArn) return;
    if (!a.includes(':')) {
      setFormError('Action must look like service:Action, e.g. s3:GetObject');
      return;
    }
    if (!r) {
      setFormError('Resource ARN is required (use * to test any resource)');
      return;
    }
    setFormError(null);
    const id = `manual-${Date.now()}`;
    setPending((p) => [...p, { id, action: a, resource: r }]);
    try {
      const result = await simulateAWSAccess(cluster, {
        roleArn,
        checks: [{ action: a, resource: r }],
      });
      const rows = result.checks.map((c, i) => ({
        ...c,
        id: c.id || `${id}-${i}`,
        source: c.source?.kind ? c.source : { kind: 'manual' },
      }));
      setManualChecks((m) => [...m, ...rows]);
      setAction('');
      actionInputRef.current?.focus();
    } catch (e: any) {
      setManualChecks((m) => [
        ...m,
        {
          id,
          source: { kind: 'manual' },
          service: a.split(':')[0],
          action: a,
          resource: r,
          decision: 'error',
          error: e?.response?.data?.error || e?.message || 'Simulation failed',
        },
      ]);
    } finally {
      setPending((p) => p.filter((x) => x.id !== id));
    }
  }, [action, resource, roleArn, cluster]);

  const denied = allChecks.filter((c) => c.decision !== 'allowed').length;

  return (
    <div className="awsid-checks">
      <div className="awsid-section-title">
        Access checks
        {allChecks.length > 0 && (
          <span className="awsid-checks-count">
            {allChecks.length} ·{' '}
            {denied === 0 ? 'all allowed' : `${denied} denied`}
          </span>
        )}
      </div>
      {allChecks.length === 0 && pending.length === 0 ? (
        <div className="awsid-empty">
          Nothing to check yet. Kanivet auto-derives checks from S3 CSI volumes
          and env vars such as <span className="awsid-mono">*_BUCKET</span>,{' '}
          <span className="awsid-mono">*QUEUE_URL</span>,{' '}
          <span className="awsid-mono">*TABLE_NAME</span>,{' '}
          <span className="awsid-mono">*SECRET_ARN</span>, or any literal ARN.
          Add one below to test something specific.
        </div>
      ) : (
        <div className="awsid-checks-table-wrap">
          <table className="awsid-checks-table">
            <thead>
              <tr>
                <th />
                <th>Source</th>
                <th>Action</th>
                <th>Resource</th>
                <th>Decision</th>
              </tr>
            </thead>
            <tbody>
              {allChecks.map((c) => (
                <CheckRow
                  key={c.id}
                  check={c}
                  expanded={expanded.has(c.id)}
                  onToggle={() => toggle(c.id)}
                />
              ))}
              {pending.map((p) => (
                <tr key={p.id} className="awsid-check-row pending">
                  <td />
                  <td className="awsid-check-source">manual</td>
                  <td className="awsid-mono">{p.action}</td>
                  <td
                    className="awsid-mono awsid-check-resource"
                    title={p.resource}
                  >
                    <span className="awsid-truncate">{p.resource}</span>
                  </td>
                  <td>
                    <AWSStatusPill status="pending" />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <div className="awsid-check-form">
        <ActionCombobox
          inputRef={actionInputRef}
          value={action}
          onChange={setAction}
          onPick={(a) => {
            setAction(a);
            if (!resource) setResource('');
          }}
          onSubmit={run}
          disabled={!roleArn}
        />
        <input
          type="text"
          className="awsid-check-resource-input awsid-mono"
          value={resource}
          placeholder={
            action
              ? resourceExampleForAction(action)
              : 'arn:aws:s3:::my-bucket/*'
          }
          spellCheck={false}
          disabled={!roleArn}
          onChange={(e) => setResource(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') run();
          }}
        />
        <button
          type="button"
          className="awsid-btn awsid-btn-primary"
          onClick={run}
          disabled={!roleArn || !action.trim()}
          title={roleArn ? 'Simulate this action' : 'Resolve a role first'}
        >
          <PlayIcon /> Run
        </button>
      </div>
      {formError && <div className="awsid-check-form-error">{formError}</div>}
    </div>
  );
};

export default AWSAccessChecks;
