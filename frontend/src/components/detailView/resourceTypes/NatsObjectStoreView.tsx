import { useCallback, useEffect, useState } from 'react';
import api from '../../../services/api';
import { NatsObjectInfo } from '../../../types/nats';
import { formatBytes } from '../../../utils/detailViewFormatters';
import './NatsBucketBrowser.css';

export interface NatsObjectStoreItem {
  cluster: string;
}

interface NatsObjectStoreViewProps {
  cluster: string;
  item: NatsObjectStoreItem;
}

const saveBlob = (blob: Blob, filename: string) => {
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(url);
};

const NatsObjectStoreView = ({ cluster }: NatsObjectStoreViewProps) => {
  const [buckets, setBuckets] = useState<string[]>([]);
  const [bucketsLoading, setBucketsLoading] = useState(true);
  const [bucketsError, setBucketsError] = useState<string | null>(null);

  const [selectedBucket, setSelectedBucket] = useState<string | null>(null);
  const [objects, setObjects] = useState<NatsObjectInfo[]>([]);
  const [objectsLoading, setObjectsLoading] = useState(false);
  const [objectsError, setObjectsError] = useState<string | null>(null);
  const [downloadingName, setDownloadingName] = useState<string | null>(null);

  useEffect(() => {
    setBucketsLoading(true);
    setBucketsError(null);
    api
      .listNatsObjectStores(cluster)
      .then(setBuckets)
      .catch((err) =>
        setBucketsError(err.message || 'Failed to load object stores'),
      )
      .finally(() => setBucketsLoading(false));
  }, [cluster]);

  const selectBucket = useCallback(
    (bucket: string) => {
      setSelectedBucket(bucket);
      setObjectsLoading(true);
      setObjectsError(null);
      api
        .listNatsObjects(cluster, bucket)
        .then(setObjects)
        .catch((err) =>
          setObjectsError(err.message || 'Failed to load objects'),
        )
        .finally(() => setObjectsLoading(false));
    },
    [cluster],
  );

  const download = useCallback(
    async (bucket: string, name: string) => {
      setDownloadingName(name);
      try {
        const blob = await api.downloadNatsObject(cluster, bucket, name);
        saveBlob(blob, name);
      } catch {
        // downloadNatsObject already logs; nothing further to report inline here
      } finally {
        setDownloadingName(null);
      }
    },
    [cluster],
  );

  return (
    <div className="nats-bucket-browser">
      <div className="nats-bucket-browser-title">Object Stores</div>

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
            No Object Store buckets in this cluster.
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
            Objects in {selectedBucket}
          </div>
          {objectsLoading && (
            <div className="nats-bucket-browser-status">Loading…</div>
          )}
          {objectsError && (
            <div className="nats-bucket-browser-status error">
              {objectsError}
            </div>
          )}
          {!objectsLoading && !objectsError && objects.length === 0 && (
            <div className="nats-bucket-browser-status">No objects.</div>
          )}
          {objects.length > 0 && (
            <div className="nats-object-table">
              <div className="nats-object-row nats-object-head">
                <span>Name</span>
                <span>Size</span>
                <span>Modified</span>
                <span>Digest</span>
                <span />
              </div>
              {objects.map((obj) => (
                <div key={obj.nuid} className="nats-object-row">
                  <span className="nats-object-name">{obj.name}</span>
                  <span>{formatBytes(obj.size)}</span>
                  <span>{new Date(obj.mtime).toLocaleString()}</span>
                  <span className="nats-object-digest" title={obj.digest}>
                    {obj.digest ? obj.digest.slice(0, 16) + '…' : '-'}
                  </span>
                  <button
                    type="button"
                    disabled={downloadingName === obj.name}
                    onClick={() => download(selectedBucket, obj.name)}
                  >
                    {downloadingName === obj.name ? 'Downloading…' : 'Download'}
                  </button>
                </div>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  );
};

export default NatsObjectStoreView;
