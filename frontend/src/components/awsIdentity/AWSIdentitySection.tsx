import React, { useCallback, useEffect, useState } from 'react';
import { ExternalLinkIcon } from '@radix-ui/react-icons';
import PropertyGroup from '../detailView/shared/PropertyGroup';
import AWSIcon from '../AWSIcon';
import ClipboardCopy from '../common/ClipboardCopy';
import { useStore } from '../../store';
import { explainAWSIdentity } from '../../services/api/awsIdentity';
import { getCachedAWSIdentityStatus } from './awsIdentityStatusCache';
import { AWSMechanismBadge, AWSStatusPill } from './AWSStatusPill';
import {
  AWSIdentityClusterStatus,
  AWSIdentityExplanation,
} from '../../types/awsIdentity';
import {
  firstProblemStep,
  hasVisibleAWSIdentity,
  identityTargetFromResource,
  roleNameFromArn,
} from './awsIdentityUtils';
import './awsIdentity.css';
import './AWSIdentitySection.css';

interface Props {
  resource: any;
  cluster: string;
}

const AWSIdentitySection: React.FC<Props> = ({ resource, cluster }) => {
  const openBottomTab = useStore((s) => s.openBottomTab);
  const [status, setStatus] = useState<AWSIdentityClusterStatus | null>(null);
  const [explanation, setExplanation] = useState<AWSIdentityExplanation | null>(
    null,
  );
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [requested, setRequested] = useState(false);

  const target = identityTargetFromResource(resource);
  const visible = hasVisibleAWSIdentity(resource);
  const resourceKey = `${resource?.kind}/${resource?.metadata?.namespace}/${resource?.metadata?.name}`;

  useEffect(() => {
    let cancelled = false;
    getCachedAWSIdentityStatus(cluster)
      .then((s) => {
        if (!cancelled) setStatus(s);
      })
      .catch(() => {
        if (!cancelled)
          setStatus({ available: false, credentials: { source: 'none' } });
      });
    return () => {
      cancelled = true;
    };
  }, [cluster]);

  useEffect(() => {
    setExplanation(null);
    setError(null);
    setRequested(visible);
  }, [resourceKey, visible]);

  const load = useCallback(async () => {
    if (!target) return;
    setLoading(true);
    setError(null);
    try {
      const result = await explainAWSIdentity(cluster, {
        namespace: target.namespace,
        serviceAccount: target.serviceAccount,
        pod: target.podName,
      });
      setExplanation(result);
    } catch (e: any) {
      setError(e?.response?.data?.error || e?.message || 'Failed to load');
    } finally {
      setLoading(false);
    }
  }, [cluster, target?.namespace, target?.serviceAccount, target?.podName]);

  useEffect(() => {
    if (requested && !explanation && !loading && !error) void load();
  }, [requested, explanation, loading, error, load]);

  if (!target || !status?.available) return null;

  const problem = explanation ? firstProblemStep(explanation.steps) : undefined;
  const checks = explanation?.checks || [];
  const denied = checks.filter((c) => c.decision !== 'allowed').length;
  const roleArn = explanation?.role?.arn || explanation?.binding?.roleArn;

  return (
    <>
      <PropertyGroup
        title="AWS Identity"
        icon={<AWSIcon />}
        defaultOpen={visible}
        onOpenChange={(open) => {
          if (open) setRequested(true);
        }}
        actions={
          explanation ? (
            <AWSStatusPill
              status={explanation.overall}
              className="awsid-section-pill"
            />
          ) : undefined
        }
      >
        <div className="awsid-section">
          {loading && !explanation && (
            <div className="awsid-section-loading">
              <span className="awsid-spinner" /> Resolving identity…
            </div>
          )}
          {error && (
            <div className="awsid-section-error">
              {error}{' '}
              <button type="button" className="link-button" onClick={load}>
                Retry
              </button>
            </div>
          )}
          {explanation && (
            <>
              <div className="awsid-section-row">
                <AWSMechanismBadge mechanism={explanation.mechanism} />
                {explanation.mechanism === 'none' && (
                  <span className="awsid-muted">
                    Uses node credentials or none.
                  </span>
                )}
              </div>
              {roleArn && (
                <div
                  className="awsid-section-row awsid-section-role"
                  title={roleArn}
                >
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
                </div>
              )}
              {explanation.mechanism !== 'none' && (
                <div className="awsid-section-row">
                  <span className="awsid-section-label">Trust</span>
                  <AWSStatusPill
                    status={explanation.trust?.verdict || 'unknown'}
                    label={
                      explanation.trust?.verdict === 'ok'
                        ? 'Trusted'
                        : explanation.trust?.verdict === 'error'
                          ? 'Broken'
                          : explanation.trust?.verdict === 'warning'
                            ? 'Check'
                            : 'Unknown'
                    }
                  />
                  {problem?.problems?.[0] && (
                    <span
                      className="awsid-section-problem awsid-truncate"
                      title={problem.problems[0].message}
                    >
                      {problem.problems[0].message}
                    </span>
                  )}
                </div>
              )}
              {explanation.permissionError && (
                <div className="awsid-section-row awsid-section-problem">
                  Kanivet&apos;s AWS credentials can&apos;t read IAM.
                </div>
              )}
              {explanation.mechanism !== 'none' && (
                <div className="awsid-section-row">
                  <span className="awsid-section-label">Checks</span>
                  <span
                    className={
                      denied > 0 ? 'awsid-section-denied' : 'awsid-muted'
                    }
                  >
                    {checks.length === 0
                      ? 'no derived checks'
                      : `${checks.length} ${checks.length === 1 ? 'check' : 'checks'} · ${
                          denied === 0 ? 'all allowed' : `${denied} denied`
                        }`}
                  </span>
                </div>
              )}
              <button
                type="button"
                className="awsid-btn awsid-btn-primary awsid-section-open"
                onClick={() => openBottomTab('aws-identity', resource, cluster)}
              >
                Open identity trace
              </button>
            </>
          )}
        </div>
      </PropertyGroup>
      <div className="section-divider" />
    </>
  );
};

export default AWSIdentitySection;
