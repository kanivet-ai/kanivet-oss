import React, { useState } from 'react';
import {
  CheckCircledIcon,
  CopyIcon,
  Cross2Icon,
  ExclamationTriangleIcon,
  ExternalLinkIcon,
  ReloadIcon,
} from '@radix-ui/react-icons';
import './LoginProgress.css';

export type LoginProgressState =
  | 'pending'
  | 'succeeded'
  | 'failed'
  | 'cancelled'
  | 'expired';

interface LoginProgressProps {
  state: LoginProgressState;
  /** Heading while pending, e.g. "Approve the sign-in in your browser". */
  title?: string;
  /** Secondary line while pending. */
  waitingText?: string;
  /** Device code to show and copy, when the flow has one. */
  code?: string;
  /** URL the user can reopen if the browser tab was closed. */
  url?: string;
  error?: string;
  successText?: string;
  onCancel?: () => void;
  onRetry?: () => void;
  onDismiss?: () => void;
  compact?: boolean;
}

/** Opens a URL in the system browser (Electron routes window.open externally). */
export const openExternal = (url: string) => {
  if (!url) return;
  window.open(url, '_blank', 'noopener,noreferrer');
};

/**
 * One consistent panel for every browser-based sign-in Kanivet can start:
 * AWS device code, `gcloud auth login`, `az login`. It never starts anything
 * itself; it only mirrors backend state and offers open / copy / cancel.
 */
const LoginProgress: React.FC<LoginProgressProps> = ({
  state,
  title = 'Approve the sign-in in your browser',
  waitingText = 'Waiting for approval…',
  code,
  url,
  error,
  successText = 'Signed in',
  onCancel,
  onRetry,
  onDismiss,
  compact = false,
}) => {
  const [copied, setCopied] = useState(false);

  const copyCode = async () => {
    if (!code) return;
    try {
      await navigator.clipboard.writeText(code);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      /* clipboard unavailable */
    }
  };

  if (state === 'succeeded') {
    return (
      <div
        className={`login-progress login-progress--success ${compact ? 'compact' : ''}`}
        role="status"
      >
        <CheckCircledIcon />
        <span>{successText}</span>
        {onDismiss && (
          <button
            className="ap-icon-btn ap-icon-btn--sm"
            onClick={onDismiss}
            aria-label="Dismiss"
          >
            <Cross2Icon />
          </button>
        )}
      </div>
    );
  }

  if (state === 'failed' || state === 'cancelled' || state === 'expired') {
    const message =
      error ||
      (state === 'cancelled'
        ? 'Sign-in cancelled.'
        : state === 'expired'
          ? 'The sign-in request expired before it was approved.'
          : 'Sign-in failed.');
    return (
      <div
        className={`login-progress login-progress--error ${compact ? 'compact' : ''}`}
        role="alert"
      >
        <ExclamationTriangleIcon />
        <span className="login-progress-message">{message}</span>
        {onRetry && (
          <button className="ap-btn ap-btn--sm" onClick={onRetry}>
            <ReloadIcon /> Try again
          </button>
        )}
        {onDismiss && (
          <button
            className="ap-icon-btn ap-icon-btn--sm"
            onClick={onDismiss}
            aria-label="Dismiss"
          >
            <Cross2Icon />
          </button>
        )}
      </div>
    );
  }

  return (
    <div
      className={`login-progress login-progress--pending ${compact ? 'compact' : ''}`}
      role="status"
      aria-live="polite"
    >
      <div className="login-progress-head">
        <span className="ap-spinner login-progress-spinner" aria-hidden />
        <div className="login-progress-titles">
          <span className="login-progress-title">{title}</span>
          <span className="login-progress-sub">{waitingText}</span>
        </div>
        {onCancel && (
          <button
            className="ap-icon-btn ap-icon-btn--sm"
            onClick={onCancel}
            aria-label="Cancel sign-in"
            title="Cancel"
          >
            <Cross2Icon />
          </button>
        )}
      </div>
      {(code || url) && (
        <div className="login-progress-actions">
          {code && (
            <button
              className="login-progress-code"
              onClick={copyCode}
              title="Copy code"
            >
              <span className="ap-mono">{code}</span>
              <CopyIcon />
              {copied && <span className="login-progress-copied">Copied</span>}
            </button>
          )}
          {url && (
            <button
              className="ap-btn ap-btn--sm"
              onClick={() => openExternal(url)}
            >
              <ExternalLinkIcon /> Open browser
            </button>
          )}
        </div>
      )}
    </div>
  );
};

export default LoginProgress;
