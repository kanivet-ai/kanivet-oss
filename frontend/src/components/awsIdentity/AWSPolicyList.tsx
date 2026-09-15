import React, { useState } from 'react';
import { ChevronRightIcon, ExternalLinkIcon } from '@radix-ui/react-icons';
import { AWSIAMPolicy, AWSPolicyStatement } from '../../types/awsIdentity';
import { AWSPolicyTypeBadge } from './AWSStatusPill';
import { prettyJson } from './awsIdentityUtils';
import './awsIdentity.css';
import './AWSPolicyList.css';

const joinList = (values: string[] | undefined, max = 6): string => {
  if (!values || values.length === 0) return '—';
  if (values.length <= max) return values.join(', ');
  return `${values.slice(0, max).join(', ')} … +${values.length - max}`;
};

const StatementRow: React.FC<{ statement: AWSPolicyStatement }> = ({
  statement,
}) => {
  const allow = statement.effect.toLowerCase() === 'allow';
  const actions = statement.actions?.length
    ? joinList(statement.actions)
    : statement.notActions?.length
      ? `NOT ${joinList(statement.notActions)}`
      : '—';
  const resources = statement.resources?.length
    ? joinList(statement.resources, 3)
    : statement.notResources?.length
      ? `NOT ${joinList(statement.notResources, 3)}`
      : '—';
  return (
    <div className="awsid-statement">
      <span
        className={`awsid-pill ${allow ? 'status-ok' : 'status-error'}`}
        title={statement.sid || `Statement ${statement.index}`}
      >
        {allow ? 'Allow' : 'Deny'}
      </span>
      <div className="awsid-statement-body">
        <div
          className="awsid-statement-actions awsid-mono"
          title={
            statement.actions?.join('\n') || statement.notActions?.join('\n')
          }
        >
          {actions}
        </div>
        <div
          className="awsid-statement-resources awsid-mono awsid-muted"
          title={
            statement.resources?.join('\n') ||
            statement.notResources?.join('\n')
          }
        >
          {resources}
        </div>
      </div>
      {statement.hasCondition && (
        <span
          className="awsid-statement-condition"
          title="This statement only applies when its Condition matches"
        >
          condition
        </span>
      )}
    </div>
  );
};

const PolicyCard: React.FC<{ policy: AWSIAMPolicy }> = ({ policy }) => {
  const [showRaw, setShowRaw] = useState(false);
  return (
    <div className={`awsid-policy type-${policy.type}`}>
      <div className="awsid-policy-header">
        <span className="awsid-policy-name awsid-truncate" title={policy.arn}>
          {policy.name}
        </span>
        <AWSPolicyTypeBadge type={policy.type} awsManaged={policy.awsManaged} />
        <span className="awsid-policy-count awsid-muted">
          {policy.statements.length}{' '}
          {policy.statements.length === 1 ? 'statement' : 'statements'}
        </span>
        <span className="awsid-policy-spacer" />
        {policy.consoleUrl && (
          <a
            className="awsid-link"
            href={policy.consoleUrl}
            target="_blank"
            rel="noopener noreferrer"
            title="Open in AWS console"
          >
            <ExternalLinkIcon />
          </a>
        )}
        <button
          type="button"
          className={`awsid-link awsid-policy-raw-toggle ${showRaw ? 'open' : ''}`}
          onClick={() => setShowRaw((v) => !v)}
        >
          <ChevronRightIcon />
          JSON
        </button>
      </div>
      <div className="awsid-policy-statements">
        {policy.statements.map((s) => (
          <StatementRow key={s.index} statement={s} />
        ))}
        {policy.statements.length === 0 && (
          <div className="awsid-muted awsid-policy-empty">No statements</div>
        )}
      </div>
      {showRaw && (
        <pre className="awsid-pre">{prettyJson(policy.document)}</pre>
      )}
    </div>
  );
};

const AWSPolicyList: React.FC<{ policies: AWSIAMPolicy[] }> = ({
  policies,
}) => {
  if (policies.length === 0) {
    return (
      <div className="awsid-empty">
        No policies are attached to this role — every request will be denied.
      </div>
    );
  }
  const boundary = policies.filter((p) => p.type === 'boundary');
  const rest = policies.filter((p) => p.type !== 'boundary');
  return (
    <div className="awsid-policy-list">
      {rest.map((p) => (
        <PolicyCard key={`${p.type}-${p.arn || p.name}`} policy={p} />
      ))}
      {boundary.length > 0 && (
        <>
          <div className="awsid-section-title">Permissions boundary</div>
          <div className="awsid-muted awsid-policy-boundary-note">
            The boundary caps what the policies above can grant; anything not
            allowed here is denied even if a policy allows it.
          </div>
          {boundary.map((p) => (
            <PolicyCard key={`${p.type}-${p.arn || p.name}`} policy={p} />
          ))}
        </>
      )}
    </div>
  );
};

export default AWSPolicyList;
