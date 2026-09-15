import React, { useCallback, useEffect, useRef, useState } from 'react';
import {
  ChevronDownIcon,
  ExclamationTriangleIcon,
} from '@radix-ui/react-icons';
import AWSIcon from '../AWSIcon';
import cloudService from '../../services/cloudService';
import {
  clearAWSIdentityCredentials,
  setAWSIdentityCredentials,
} from '../../services/api/awsIdentity';
import { invalidateAWSIdentityStatus } from './awsIdentityStatusCache';
import { AWSProfile } from '../../types/cloud';
import { AWSCredentials } from '../../types/awsIdentity';
import { credentialSourceLabel } from './awsIdentityUtils';
import './awsIdentity.css';
import './AWSCredentialsBar.css';

interface Props {
  cluster: string;
  credentials: AWSCredentials;
  onChanged: (credentials: AWSCredentials) => void;
  compact?: boolean;
}

const AWSCredentialsBar: React.FC<Props> = ({
  cluster,
  credentials,
  onChanged,
  compact = false,
}) => {
  const [open, setOpen] = useState(false);
  const [profiles, setProfiles] = useState<AWSProfile[]>([]);
  const [loadingProfiles, setLoadingProfiles] = useState(false);
  const [profile, setProfile] = useState(credentials.profile || '');
  const [region, setRegion] = useState(credentials.region || '');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const rootRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    setProfile(credentials.profile || '');
    setRegion(credentials.region || '');
  }, [credentials.profile, credentials.region]);

  useEffect(() => {
    if (!open) return;
    setLoadingProfiles(true);
    cloudService
      .listAWSProfiles()
      .then(setProfiles)
      .catch(() => setProfiles([]))
      .finally(() => setLoadingProfiles(false));
    const onMouseDown = (e: MouseEvent) => {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) {
        setOpen(false);
      }
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false);
    };
    document.addEventListener('mousedown', onMouseDown);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onMouseDown);
      document.removeEventListener('keydown', onKey);
    };
  }, [open]);

  const save = useCallback(async () => {
    if (!profile) return;
    setSaving(true);
    setError(null);
    try {
      const next = await setAWSIdentityCredentials(cluster, {
        profile,
        region: region || undefined,
      });
      invalidateAWSIdentityStatus(cluster);
      onChanged(next);
      setOpen(false);
    } catch (e: any) {
      setError(e?.response?.data?.error || e?.message || 'Failed to save');
    } finally {
      setSaving(false);
    }
  }, [cluster, profile, region, onChanged]);

  const reset = useCallback(async () => {
    setSaving(true);
    setError(null);
    try {
      const next = await clearAWSIdentityCredentials(cluster);
      invalidateAWSIdentityStatus(cluster);
      onChanged(next);
      setOpen(false);
    } catch (e: any) {
      setError(e?.response?.data?.error || e?.message || 'Failed to reset');
    } finally {
      setSaving(false);
    }
  }, [cluster, onChanged]);

  const hasCreds = credentials.source !== 'none';
  const chipParts = [
    credentials.accountId,
    credentials.profile,
    credentials.region,
  ].filter(Boolean);

  return (
    <div className={`awsid-creds ${compact ? 'compact' : ''}`} ref={rootRef}>
      <button
        type="button"
        className={`awsid-creds-chip ${credentials.error ? 'has-error' : ''} ${
          hasCreds ? '' : 'is-empty'
        }`}
        onClick={() => setOpen((o) => !o)}
        title={
          credentials.error ||
          (hasCreds
            ? `AWS credentials ${credentialSourceLabel(credentials)}`
            : 'Choose an AWS profile for this cluster')
        }
      >
        {credentials.error ? (
          <ExclamationTriangleIcon className="awsid-creds-warn" />
        ) : (
          <AWSIcon size={12} />
        )}
        {hasCreds ? (
          <>
            <span className="awsid-creds-text awsid-mono awsid-truncate">
              {chipParts.join(' · ')}
            </span>
            {!compact && (
              <span className="awsid-creds-source">
                {credentialSourceLabel(credentials)}
              </span>
            )}
          </>
        ) : (
          <span className="awsid-creds-text">Choose AWS profile</span>
        )}
        <ChevronDownIcon />
      </button>

      {open && (
        <div className="awsid-creds-popover" role="dialog">
          <div className="awsid-creds-popover-title">
            AWS credentials for this cluster
          </div>
          {credentials.source === 'exec' && (
            <div className="awsid-creds-note">
              Derived from the kubeconfig exec command (
              <span className="awsid-mono">aws eks get-token</span>). Override
              only if IAM lives in a different account or profile.
            </div>
          )}
          {credentials.error && (
            <div className="awsid-callout callout-warning">
              {credentials.error}
            </div>
          )}
          <label className="awsid-creds-field">
            <span>Profile</span>
            <select
              value={profile}
              onChange={(e) => {
                const p = profiles.find((x) => x.name === e.target.value);
                setProfile(e.target.value);
                if (p?.region && !region) setRegion(p.region);
              }}
              disabled={loadingProfiles}
            >
              <option value="">
                {loadingProfiles ? 'Loading profiles…' : 'Select a profile'}
              </option>
              {profiles.map((p) => (
                <option key={p.name} value={p.name}>
                  {p.name}
                  {p.accountId ? ` — ${p.accountId}` : ''}
                  {p.region ? ` (${p.region})` : ''}
                  {p.isSso ? ' · SSO' : ''}
                </option>
              ))}
            </select>
          </label>
          <label className="awsid-creds-field">
            <span>Region</span>
            <input
              type="text"
              value={region}
              placeholder={credentials.region || 'us-east-1'}
              onChange={(e) => setRegion(e.target.value.trim())}
              spellCheck={false}
            />
          </label>
          {error && <div className="awsid-creds-error">{error}</div>}
          <div className="awsid-creds-actions">
            {credentials.source === 'override' && (
              <button
                type="button"
                className="awsid-btn"
                onClick={reset}
                disabled={saving}
              >
                Use kubeconfig
              </button>
            )}
            <span className="awsid-creds-spacer" />
            <button
              type="button"
              className="awsid-btn"
              onClick={() => setOpen(false)}
              disabled={saving}
            >
              Cancel
            </button>
            <button
              type="button"
              className="awsid-btn awsid-btn-primary"
              onClick={save}
              disabled={saving || !profile}
            >
              {saving ? 'Saving…' : 'Save'}
            </button>
          </div>
        </div>
      )}
    </div>
  );
};

export default AWSCredentialsBar;
