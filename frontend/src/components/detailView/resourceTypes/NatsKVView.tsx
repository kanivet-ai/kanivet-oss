import { useCallback, useEffect, useState } from 'react';
import api from '../../../services/api';
import { NatsKVEntry } from '../../../types/nats';
import NatsPayloadViewer from '../../common/NatsPayloadViewer';
import './NatsBucketBrowser.css';

export interface NatsKVItem {
  cluster: string;
}

interface NatsKVViewProps {
  cluster: string;
  item: NatsKVItem;
}

const NatsKVView = ({ cluster }: NatsKVViewProps) => {
  const [buckets, setBuckets] = useState<string[]>([]);
  const [bucketsLoading, setBucketsLoading] = useState(true);
  const [bucketsError, setBucketsError] = useState<string | null>(null);

  const [selectedBucket, setSelectedBucket] = useState<string | null>(null);
  const [keys, setKeys] = useState<string[]>([]);
  const [keysLoading, setKeysLoading] = useState(false);
  const [keysError, setKeysError] = useState<string | null>(null);

  const [selectedKey, setSelectedKey] = useState<string | null>(null);
  const [entry, setEntry] = useState<NatsKVEntry | null>(null);
  const [history, setHistory] = useState<NatsKVEntry[]>([]);
  const [entryLoading, setEntryLoading] = useState(false);
  const [entryError, setEntryError] = useState<string | null>(null);

  useEffect(() => {
    setBucketsLoading(true);
    setBucketsError(null);
    api
      .listNatsKVBuckets(cluster)
      .then(setBuckets)
      .catch((err) =>
        setBucketsError(err.message || 'Failed to load KV buckets'),
      )
      .finally(() => setBucketsLoading(false));
  }, [cluster]);

  const selectBucket = useCallback(
    (bucket: string) => {
      setSelectedBucket(bucket);
      setSelectedKey(null);
      setEntry(null);
      setHistory([]);
      setKeysLoading(true);
      setKeysError(null);
      api
        .listNatsKVKeys(cluster, bucket)
        .then(setKeys)
        .catch((err) => setKeysError(err.message || 'Failed to load keys'))
        .finally(() => setKeysLoading(false));
    },
    [cluster],
  );

  const selectKey = useCallback(
    (bucket: string, key: string) => {
      setSelectedKey(key);
      setEntryLoading(true);
      setEntryError(null);
      Promise.all([
        api.getNatsKVEntry(cluster, bucket, key),
        api.getNatsKVHistory(cluster, bucket, key),
      ])
        .then(([e, h]) => {
          setEntry(e);
          setHistory(h);
        })
        .catch((err) => setEntryError(err.message || 'Failed to load entry'))
        .finally(() => setEntryLoading(false));
    },
    [cluster],
  );

  return (
    <div className="nats-bucket-browser">
      <div className="nats-bucket-browser-title">KV Buckets</div>

      <div className="nats-bucket-browser-section">
        <div className="nats-bucket-browser-section-title">Buckets</div>
        {bucketsLoading && (
          <div className="nats-bucket-browser-status">Loading…</div>
        )}
        {bucketsError && (
          <div className="nats-bucket-browser-status error">{bucketsError}</div>
        )}
        {!bucketsLoading && !bucketsError && buckets.length === 0 && (
          <div className="nats-bucket-browser-status">
            No KV buckets in this cluster.
          </div>
        )}
        <div className="nats-bucket-browser-chips">
          {buckets.map((b) => (
            <button
              key={b}
              type="button"
              className={`nats-bucket-browser-chip${selectedBucket === b ? ' active' : ''}`}
              onClick={() => selectBucket(b)}
            >
              {b}
            </button>
          ))}
        </div>
      </div>

      {selectedBucket && (
        <div className="nats-bucket-browser-section">
          <div className="nats-bucket-browser-section-title">
            Keys in {selectedBucket}
          </div>
          {keysLoading && (
            <div className="nats-bucket-browser-status">Loading…</div>
          )}
          {keysError && (
            <div className="nats-bucket-browser-status error">{keysError}</div>
          )}
          {!keysLoading && !keysError && keys.length === 0 && (
            <div className="nats-bucket-browser-status">No keys.</div>
          )}
          <div className="nats-bucket-browser-chips">
            {keys.map((k) => (
              <button
                key={k}
                type="button"
                className={`nats-bucket-browser-chip${selectedKey === k ? ' active' : ''}`}
                onClick={() => selectKey(selectedBucket, k)}
              >
                {k}
              </button>
            ))}
          </div>
        </div>
      )}

      {selectedKey && (
        <div className="nats-bucket-browser-section">
          <div className="nats-bucket-browser-section-title">{selectedKey}</div>
          {entryLoading && (
            <div className="nats-bucket-browser-status">Loading…</div>
          )}
          {entryError && (
            <div className="nats-bucket-browser-status error">{entryError}</div>
          )}
          {entry && !entryLoading && (
            <>
              <div className="nats-bucket-browser-meta">
                <span>revision {entry.revision}</span>
                <span>{entry.operation}</span>
                <span>{new Date(entry.created).toLocaleString()}</span>
              </div>
              <NatsPayloadViewer data={entry.value} />
            </>
          )}
          {history.length > 1 && (
            <div className="nats-bucket-browser-history">
              <div className="nats-bucket-browser-section-title">
                History ({history.length})
              </div>
              {history
                .slice()
                .reverse()
                .map((h) => (
                  <div
                    key={h.revision}
                    className="nats-bucket-browser-history-row"
                  >
                    <span>rev {h.revision}</span>
                    <span>{h.operation}</span>
                    <span>{new Date(h.created).toLocaleString()}</span>
                  </div>
                ))}
            </div>
          )}
        </div>
      )}
    </div>
  );
};

export default NatsKVView;
