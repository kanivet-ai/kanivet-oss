import React, { useMemo } from 'react';
import { LockClosedIcon } from '@radix-ui/react-icons';
import ClipboardCopy from '../common/ClipboardCopy';
import AWSCredentialsBar from './AWSCredentialsBar';
import { AWSCredentials } from '../../types/awsIdentity';
import { requiredPermissionsPolicy } from './awsIdentityUtils';
import './awsIdentity.css';
import './AWSPermissionError.css';

interface Props {
  cluster: string;
  message: string;
  requiredPermissions?: string[];
  credentials: AWSCredentials;
  onCredentialsChanged: (credentials: AWSCredentials) => void;
}

const AWSPermissionError: React.FC<Props> = ({
  cluster,
  message,
  requiredPermissions = [],
  credentials,
  onCredentialsChanged,
}) => {
  const policy = useMemo(
    () => requiredPermissionsPolicy(requiredPermissions),
    [requiredPermissions],
  );

  return (
    <div className="awsid-callout callout-warning awsid-permission-error">
      <div className="awsid-permission-header">
        <LockClosedIcon />
        <span className="awsid-callout-title">
          Kanivet&apos;s AWS credentials can&apos;t read IAM
        </span>
      </div>
      <div className="awsid-permission-message">{message}</div>
      <div className="awsid-permission-actions">
        <span className="awsid-muted">
          Switch to a profile with IAM read access, or grant these read-only
          permissions to the current one:
        </span>
        <AWSCredentialsBar
          cluster={cluster}
          credentials={credentials}
          onChanged={onCredentialsChanged}
          compact
        />
      </div>
      {requiredPermissions.length > 0 && (
        <div className="awsid-permission-policy">
          <pre className="awsid-pre">{policy}</pre>
          <div className="awsid-permission-copy">
            <ClipboardCopy text={policy} alwaysShow />
          </div>
        </div>
      )}
    </div>
  );
};

export default AWSPermissionError;
