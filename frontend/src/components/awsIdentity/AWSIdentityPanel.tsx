import React, { useCallback, useEffect, useState } from 'react';
import { ExternalLinkIcon, ReloadIcon } from '@radix-ui/react-icons';
import {
  AWSCredentials,
  AWSIdentityExplanation,
} from '../../types/awsIdentity';
import { explainAWSIdentity } from '../../services/api/awsIdentity';
import ClipboardCopy from '../common/ClipboardCopy';
import AWSIdentityStepper from './AWSIdentityStepper';
import AWSAccessChecks from './AWSAccessChecks';
import AWSCredentialsBar from './AWSCredentialsBar';
import AWSPermissionError from './AWSPermissionError';
import { AWSMechanismBadge, AWSStatusPill } from './AWSStatusPill';
import { roleNameFromArn } from './awsIdentityUtils';
import './awsIdentity.css';
import './AWSIdentityPanel.css';

export interface AWSIdentityPanelProps {
  cluster: string;
  namespace: string;
  serviceAccount?: string;
  podName?: string;
}

const Skeleton: React.FC = () => (
  <div className="awsid-panel-skeleton">
    <div className="awsid-panel-header">
      <span className="awsid-skeleton" style={{ width: 64, height: 18 }} />
      <span className="awsid-skeleton" style={{ width: 180, height: 14 }} />
      <span className="awsid-skeleton" style={{ width: 80, height: 18 }} />
      <span className="awsid-panel-spacer" />
      <span className="awsid-skeleton" style={{ width: 220, height: 24 }} />
    </div>
    <div className="awsid-panel-skeleton-rail">
      {Array.from({ length: 6 }).map((_, i) => (
        <span key={i} className="awsid-skeleton" style={{ height: 40 }} />
      ))}
    </div>
    <span className="awsid-skeleton" style={{ height: 120 }} />
  </div>
);

const AWSIdentityPanel: React.FC<AWSIdentityPanelProps> = ({
  cluster,
  namespace,
  serviceAccount,
  podName,
}) => {
  const [explanation, setExplanation] = useState<AWSIdentityExplanation | null>(
    null,
  );
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const result = await explainAWSIdentity(cluster, {
        namespace,
        serviceAccount,
        pod: podName,
      });
      setExplanation(result);
    } catch (e: any) {
      setError(
        e?.response?.data?.error ||
          e?.message ||
          'Failed to explain the AWS identity',
      );
    } finally {
      setLoading(false);
    }
  }, [cluster, namespace, serviceAccount, podName]);

  useEffect(() => {
    void load();
  }, [load]);

  const handleCredentialsChanged = useCallback(
    (_next: AWSCredentials) => {
      void load();
    },
    [load],
  );

  if (loading && !explanation) {
    return (
      <div className="awsid-panel">
        <Skeleton />
      </div>
    );
  }

  if (error && !explanation) {
    return (
      <div className="awsid-panel">
        <div className="awsid-panel-error">
          <div className="awsid-callout callout-error">
            <div className="awsid-callout-title">
              Couldn&apos;t load the identity trace
            </div>
            <div>{error}</div>
          </div>
          <button type="button" className="awsid-btn" onClick={load}>
            <ReloadIcon /> Retry
          </button>
        </div>
      </div>
    );
  }

  if (!explanation) return null;

  const subject = explanation.podName
    ? `${explanation.namespace}/${explanation.podName}`
    : `${explanation.namespace}/${explanation.serviceAccount}`;
  const roleArn = explanation.role?.arn || explanation.binding?.roleArn;

  return (
    <div className={`awsid-panel ${loading ? 'refreshing' : ''}`}>
      <div className="awsid-panel-header">
        <AWSMechanismBadge mechanism={explanation.mechanism} />
        <span className="awsid-panel-subject awsid-truncate" title={subject}>
          {subject}
        </span>
        {roleArn && (
          <span className="awsid-panel-role" title={roleArn}>
            <span className="awsid-muted">→</span>
            <span className="awsid-mono awsid-truncate">
              {roleNameFromArn(roleArn)}
            </span>
            <ClipboardCopy text={roleArn} />
            {explanation.role?.consoleUrl && (
              <a
                className="awsid-link"
                href={explanation.role.consoleUrl}
                target="_blank"
                rel="noopener noreferrer"
                title="Open role in AWS console"
              >
                <ExternalLinkIcon />
              </a>
            )}
          </span>
        )}
        <AWSStatusPill status={explanation.overall} />
        <span className="awsid-panel-spacer" />
        <AWSCredentialsBar
          cluster={cluster}
          credentials={explanation.credentials}
          onChanged={handleCredentialsChanged}
        />
        <button
          type="button"
          className="awsid-btn awsid-btn-icon"
          onClick={load}
          disabled={loading}
          title="Re-run the trace"
        >
          <ReloadIcon className={loading ? 'awsid-spin' : ''} />
        </button>
      </div>

      {error && (
        <div className="awsid-callout callout-error">
          <div className="awsid-callout-title">Refresh failed</div>
          <div>{error}</div>
        </div>
      )}

      {explanation.permissionError && (
        <AWSPermissionError
          cluster={cluster}
          message={explanation.permissionError}
          requiredPermissions={explanation.requiredPermissions}
          credentials={explanation.credentials}
          onCredentialsChanged={handleCredentialsChanged}
        />
      )}

      <AWSIdentityStepper explanation={explanation} />

      {explanation.mechanism !== 'none' && (
        <AWSAccessChecks
          cluster={cluster}
          roleArn={explanation.role?.arn}
          checks={explanation.checks || []}
        />
      )}
    </div>
  );
};

export default AWSIdentityPanel;
