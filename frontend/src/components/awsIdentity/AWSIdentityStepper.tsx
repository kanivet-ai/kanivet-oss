import React, { useEffect, useMemo, useRef, useState } from 'react';
import {
  CheckIcon,
  ChevronRightIcon,
  Cross2Icon,
  ExclamationTriangleIcon,
  ExternalLinkIcon,
  QuestionMarkIcon,
} from '@radix-ui/react-icons';
import {
  AWSIdentityDetail,
  AWSIdentityExplanation,
  AWSIdentityProblem,
  AWSIdentityStatus,
  AWSIdentityStep,
  AWSIdentityStepId,
  AWSTrustPolicy,
  AWSTrustStatement,
} from '../../types/awsIdentity';
import AWSPolicyList from './AWSPolicyList';
import ClipboardCopy from '../common/ClipboardCopy';
import { firstProblemStep, prettyJson } from './awsIdentityUtils';
import './awsIdentity.css';
import './AWSIdentityStepper.css';

const HORIZONTAL_MIN_WIDTH = 900;

const StatusGlyph: React.FC<{ status: AWSIdentityStatus }> = ({ status }) => {
  switch (status) {
    case 'ok':
      return <CheckIcon />;
    case 'error':
      return <Cross2Icon />;
    case 'warning':
      return <ExclamationTriangleIcon />;
    case 'skipped':
      return <span className="awsid-step-skip" />;
    default:
      return <QuestionMarkIcon />;
  }
};

export const AWSProblemCallout: React.FC<{
  problem: AWSIdentityProblem;
  status: AWSIdentityStatus;
}> = ({ problem, status }) => (
  <div
    className={`awsid-callout ${
      status === 'error' ? 'callout-error' : 'callout-warning'
    }`}
  >
    <div className="awsid-callout-title">{problem.message}</div>
    {(problem.expected || problem.actual) && (
      <div className="awsid-expected-actual">
        {problem.expected && (
          <>
            <span className="ea-label">expected</span>
            <span className="ea-expected">{problem.expected}</span>
          </>
        )}
        {problem.actual && (
          <>
            <span className="ea-label">actual</span>
            <span className="ea-actual">{problem.actual}</span>
          </>
        )}
      </div>
    )}
    {problem.hint && <div className="awsid-callout-hint">{problem.hint}</div>}
  </div>
);

const DetailRows: React.FC<{ details: AWSIdentityDetail[] }> = ({
  details,
}) => (
  <div className="awsid-kv">
    {details.map((d, i) => (
      <React.Fragment key={`${d.label}-${i}`}>
        <span className="kv-label">{d.label}</span>
        <span className={`kv-value ${d.mono ? 'mono' : ''}`}>
          {d.link ? (
            <a
              className="awsid-link"
              href={d.link}
              target="_blank"
              rel="noopener noreferrer"
            >
              {d.value}
              <ExternalLinkIcon />
            </a>
          ) : (
            d.value
          )}
          {d.mono && d.value && <ClipboardCopy text={d.value} />}
        </span>
      </React.Fragment>
    ))}
  </div>
);

const conditionRows = (
  conditions: AWSTrustStatement['conditions'],
): Array<{ operator: string; key: string; values: string[] }> => {
  if (!conditions) return [];
  return Object.entries(conditions).flatMap(([operator, keys]) =>
    Object.entries(keys).map(([key, values]) => ({ operator, key, values })),
  );
};

const TrustStatements: React.FC<{ trust: AWSTrustPolicy }> = ({ trust }) => {
  const [showRaw, setShowRaw] = useState(false);
  return (
    <div className="awsid-trust">
      <div className="awsid-section-title">Trust policy statements</div>
      {trust.statements.length === 0 && (
        <div className="awsid-empty">The trust policy has no statements.</div>
      )}
      {trust.statements.map((s) => {
        const allow = s.effect.toLowerCase() === 'allow';
        return (
          <div
            key={s.index}
            className={`awsid-trust-statement ${s.matches ? 'matches' : ''}`}
          >
            <div className="awsid-trust-statement-header">
              <span
                className={`awsid-pill ${allow ? 'status-ok' : 'status-error'}`}
              >
                {allow ? 'Allow' : 'Deny'}
              </span>
              <span className="awsid-trust-statement-name">
                {s.sid || `Statement ${s.index + 1}`}
              </span>
              {s.matches && (
                <span className="awsid-trust-match">
                  <CheckIcon /> matches this service account
                </span>
              )}
            </div>
            <div className="awsid-kv">
              {Object.entries(s.principal || {}).map(([type, values]) => (
                <React.Fragment key={type}>
                  <span className="kv-label">Principal · {type}</span>
                  <span className="kv-value mono">{values.join(', ')}</span>
                </React.Fragment>
              ))}
              <span className="kv-label">Action</span>
              <span className="kv-value mono">{s.actions.join(', ')}</span>
              {conditionRows(s.conditions).map((c) => (
                <React.Fragment key={`${c.operator}-${c.key}`}>
                  <span className="kv-label">{c.operator}</span>
                  <span className="kv-value mono">
                    {c.key} = {c.values.join(' | ')}
                  </span>
                </React.Fragment>
              ))}
            </div>
          </div>
        );
      })}
      <button
        type="button"
        className={`awsid-link awsid-trust-raw-toggle ${showRaw ? 'open' : ''}`}
        onClick={() => setShowRaw((v) => !v)}
      >
        <ChevronRightIcon />
        {showRaw ? 'Hide' : 'Show'} raw trust policy
      </button>
      {showRaw && <pre className="awsid-pre">{prettyJson(trust.document)}</pre>}
    </div>
  );
};

const StepBody: React.FC<{
  step: AWSIdentityStep;
  explanation: AWSIdentityExplanation;
}> = ({ step, explanation }) => (
  <div className="awsid-step-body">
    {step.problems?.map((p, i) => (
      <AWSProblemCallout
        key={`${p.code}-${i}`}
        problem={p}
        status={step.status === 'ok' ? 'warning' : step.status}
      />
    ))}
    {step.details && step.details.length > 0 && (
      <DetailRows details={step.details} />
    )}
    {step.id === 'trust' && explanation.trust && (
      <TrustStatements trust={explanation.trust} />
    )}
    {step.id === 'permissions' && explanation.role && (
      <AWSPolicyList policies={explanation.policies || []} />
    )}
    {!step.problems?.length &&
      !step.details?.length &&
      step.id !== 'trust' &&
      step.id !== 'permissions' && (
        <div className="awsid-muted awsid-step-nothing">{step.summary}</div>
      )}
  </div>
);

const pickInitialStep = (
  steps: AWSIdentityStep[],
): AWSIdentityStepId | null => {
  const problem = firstProblemStep(steps);
  if (problem) return problem.id;
  const permissions = steps.find((s) => s.id === 'permissions');
  if (permissions && permissions.status !== 'skipped') return permissions.id;
  return steps.length ? steps[steps.length - 1].id : null;
};

interface Props {
  explanation: AWSIdentityExplanation;
}

const AWSIdentityStepper: React.FC<Props> = ({ explanation }) => {
  const rootRef = useRef<HTMLDivElement>(null);
  const [horizontal, setHorizontal] = useState(true);
  const [active, setActive] = useState<AWSIdentityStepId | null>(() =>
    pickInitialStep(explanation.steps),
  );

  useEffect(() => {
    setActive(pickInitialStep(explanation.steps));
  }, [explanation.generatedAt, explanation.steps]);

  useEffect(() => {
    const el = rootRef.current;
    if (!el || typeof ResizeObserver === 'undefined') return;
    const update = () => setHorizontal(el.clientWidth >= HORIZONTAL_MIN_WIDTH);
    update();
    const observer = new ResizeObserver(update);
    observer.observe(el);
    return () => observer.disconnect();
  }, []);

  const activeStep = useMemo(
    () => explanation.steps.find((s) => s.id === active) || null,
    [explanation.steps, active],
  );

  const renderStepButton = (step: AWSIdentityStep, index: number) => (
    <button
      key={step.id}
      type="button"
      className={`awsid-step status-${step.status} ${
        step.id === active ? 'active' : ''
      }`}
      onClick={() =>
        setActive(step.id === active && !horizontal ? null : step.id)
      }
      aria-expanded={step.id === active}
      title={step.summary}
    >
      <span className="awsid-step-marker">
        <StatusGlyph status={step.status} />
      </span>
      <span className="awsid-step-text">
        <span className="awsid-step-title">
          <span className="awsid-step-index">{index + 1}</span>
          {step.title}
        </span>
        <span className="awsid-step-summary awsid-truncate">
          {step.summary}
        </span>
      </span>
      {!horizontal && (
        <span className="awsid-step-chevron">
          <ChevronRightIcon />
        </span>
      )}
    </button>
  );

  return (
    <div
      ref={rootRef}
      className={`awsid-stepper ${horizontal ? 'horizontal' : 'vertical'}`}
    >
      {horizontal ? (
        <>
          <div className="awsid-rail">
            {explanation.steps.map((step, i) => (
              <React.Fragment key={step.id}>
                {i > 0 && (
                  <span
                    className={`awsid-rail-line status-${
                      explanation.steps[i - 1].status
                    }`}
                  />
                )}
                {renderStepButton(step, i)}
              </React.Fragment>
            ))}
          </div>
          {activeStep && (
            <div className="awsid-step-panel">
              <StepBody step={activeStep} explanation={explanation} />
            </div>
          )}
        </>
      ) : (
        <div className="awsid-step-list">
          {explanation.steps.map((step, i) => (
            <div key={step.id} className="awsid-step-row">
              {renderStepButton(step, i)}
              {step.id === active && (
                <div className="awsid-step-panel">
                  <StepBody step={step} explanation={explanation} />
                </div>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  );
};

export default AWSIdentityStepper;
