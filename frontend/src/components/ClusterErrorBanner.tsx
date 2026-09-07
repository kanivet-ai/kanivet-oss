import { useEffect, useRef, useState } from 'react';
import { ExclamationTriangleIcon, ReloadIcon } from '@radix-ui/react-icons';
import { useStore, ClusterError } from '../store';
import { useShallow } from 'zustand/react/shallow';
import './ClusterErrorBanner.css';

const getErrorTitle = (errorCode: string) => {
  switch (errorCode) {
    case 'aws_sso_expired': return 'AWS SSO Session Expired';
    case 'aws_token_expired': return 'AWS Token Expired';
    case 'azure_auth_expired': return 'Azure Login Expired';
    case 'azure_kubelogin': return 'Azure Authentication Failed';
    case 'gcp_auth_expired': return 'GCP Credentials Expired';
    case 'sso_expired': return 'SSO Session Expired';
    case 'token_expired': return 'Token Expired';
    case 'unauthorized': return 'Authentication Failed';
    case 'forbidden': return 'Access Denied';
    case 'exec_failed': return 'Credential Error';
    case 'connection_failed': return 'Connection Failed';
    case 'timeout': return 'Connection Timeout';
    case 'cert_error': return 'Certificate Error';
    default: return 'Cluster Error';
  }
};

const detectCloudProvider = (cluster: string): 'aws' | 'azure' | 'gcp' | 'other' => {
  if (cluster.includes('arn:aws') || cluster.includes('eks')) return 'aws';
  if (cluster.includes('azmk8s.io') || cluster.includes('aks')) return 'azure';
  if (cluster.startsWith('gke_') || cluster.includes('gke')) return 'gcp';
  return 'other';
};

const getRefreshCommand = (errorCode: string, cluster: string) => {
  const provider = detectCloudProvider(cluster);
  if (errorCode === 'aws_sso_expired' || errorCode === 'aws_token_expired' ||
      (provider === 'aws' && (errorCode === 'unauthorized' || errorCode === 'token_expired'))) {
    return 'aws sso login --profile <your-profile>';
  }
  if (errorCode === 'azure_auth_expired' || errorCode === 'azure_kubelogin' ||
      (provider === 'azure' && (errorCode === 'unauthorized' || errorCode === 'token_expired'))) {
    return 'az login && kubelogin convert-kubeconfig';
  }
  if (errorCode === 'gcp_auth_expired' ||
      (provider === 'gcp' && (errorCode === 'unauthorized' || errorCode === 'token_expired'))) {
    return 'gcloud auth login';
  }
  return null;
};

const ClusterErrorBanner = () => {
  const { currentTab, clusterErrors, clearClusterError } = useStore(useShallow((s) => ({ currentTab: s.currentTab, clusterErrors: s.clusterErrors, clearClusterError: s.clearClusterError })));
  const [isRetrying, setIsRetrying] = useState(false);
  const retryStartRef = useRef<number>(0);

  useEffect(() => {
    setIsRetrying(false);
  }, [currentTab]);

  useEffect(() => {
    const handleRetryDone = (event: CustomEvent<{ cluster: string; healthy: boolean }>) => {
      if (event.detail.cluster !== currentTab) return;
      setIsRetrying(false);
      if (event.detail.healthy) clearClusterError(event.detail.cluster);
    };
    window.addEventListener('cluster:retry-done', handleRetryDone as EventListener);
    return () => window.removeEventListener('cluster:retry-done', handleRetryDone as EventListener);
  }, [currentTab, clearClusterError]);

  if (!currentTab) return null;
  const error: ClusterError | undefined = clusterErrors[currentTab];
  const vclusterStatuses = useStore((s) => s.vclusterStatuses);
  const vcStatus = vclusterStatuses?.[currentTab];
  const transient = vcStatus && (vcStatus.state === 'connecting' || vcStatus.state === 'reconnecting');
  if (transient) {
    return (
      <div className="cluster-error-pane">
        <div className="cluster-error-pane-card vcluster-transient">
          <div className="cluster-error-pane-header">
            <div className="cluster-error-pane-icon">
              <ReloadIcon width={20} height={20} className="cluster-error-spinning" />
            </div>
            <div className="cluster-error-pane-titles">
              <div className="cluster-error-pane-title">
                {vcStatus.state === 'connecting' ? 'Connecting to virtual cluster…' : 'Reconnecting to virtual cluster…'}
              </div>
              <div className="cluster-error-pane-cluster">{currentTab}</div>
            </div>
          </div>
          {vcStatus.detail && <div className="cluster-error-pane-message">{vcStatus.detail}</div>}
        </div>
      </div>
    );
  }
  if (!error) return null;

  const refreshCommand = getRefreshCommand(error.errorCode, currentTab);

  const handleRetry = () => {
    if (isRetrying) return;
    setIsRetrying(true);
    retryStartRef.current = Date.now();
    window.dispatchEvent(new CustomEvent('cluster:retry', { detail: { cluster: currentTab } }));
  };

  return (
    <div className="cluster-error-pane">
      <div className="cluster-error-pane-card">
        <div className="cluster-error-pane-header">
          <div className="cluster-error-pane-icon">
            <ExclamationTriangleIcon width={28} height={28} />
          </div>
          <div className="cluster-error-pane-titles">
            <div className="cluster-error-pane-title">{getErrorTitle(error.errorCode)}</div>
            <div className="cluster-error-pane-cluster">{currentTab}</div>
          </div>
        </div>

        <div className="cluster-error-pane-message">{error.errorMessage}</div>

        {refreshCommand && (
          <div className="cluster-error-pane-section">
            <div className="cluster-error-pane-section-label">Suggested command</div>
            <code className="cluster-error-pane-command">{refreshCommand}</code>
          </div>
        )}

        {error.details && error.details !== error.errorMessage && (
          <div className="cluster-error-pane-section">
            <div className="cluster-error-pane-section-label">Details</div>
            <pre className="cluster-error-pane-details">{error.details}</pre>
          </div>
        )}

        <div className="cluster-error-pane-actions">
          {error.recoverable && (
            <button
              className="cluster-error-pane-btn cluster-error-pane-btn-primary"
              onClick={handleRetry}
              disabled={isRetrying}
            >
              <ReloadIcon width={14} height={14} className={isRetrying ? 'cluster-error-spinning' : ''} />
              {isRetrying ? 'Retrying…' : 'Retry Connection'}
            </button>
          )}
        </div>
      </div>
    </div>
  );
};

export default ClusterErrorBanner;
