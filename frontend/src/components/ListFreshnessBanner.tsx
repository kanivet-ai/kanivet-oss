import { useEffect, useState } from 'react';
import { freshnessBanner } from './listFreshness';
import './ListFreshnessBanner.css';

interface ListFreshnessBannerProps {
  rowCount: number;
  hasReceivedData: boolean;
  loadError: string | null | undefined;
  lastSyncAt?: number;
  onRetry?: () => void;
}

// Over the rows of a list that is not live: says the backend stopped keeping
// it current, or that the rows are from earlier while the list arrives.
const ListFreshnessBanner = ({ rowCount, hasReceivedData, loadError, lastSyncAt, onRetry }: ListFreshnessBannerProps) => {
  const [now, setNow] = useState(() => Date.now());
  const banner = freshnessBanner({ rowCount, hasReceivedData, loadError, lastSyncAt, now });
  const shown = !!banner;
  // The age it names keeps up while it is shown.
  useEffect(() => {
    if (!shown) return;
    setNow(Date.now());
    const timer = setInterval(() => setNow(Date.now()), 30_000);
    return () => clearInterval(timer);
  }, [shown, loadError, lastSyncAt]);
  if (!banner) return null;
  return (
    <div className={`list-freshness-banner list-freshness-banner--${banner.kind}`} role="status" aria-live="polite">
      <span className="list-freshness-dot" aria-hidden="true" />
      <span className="list-freshness-text" title={banner.text}>{banner.text}</span>
      {banner.kind === 'stale' && onRetry && (
        <button type="button" className="ap-btn ap-btn--sm ap-btn--ghost list-freshness-retry" onClick={onRetry}>
          Retry now
        </button>
      )}
    </div>
  );
};

export default ListFreshnessBanner;
