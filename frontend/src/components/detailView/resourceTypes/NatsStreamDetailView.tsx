import { useCallback, useEffect, useState } from 'react';
import api from '../../../services/api';
import { NatsConsumerInfoFull, NatsRawMessage } from '../../../types/nats';
import { formatCompactNumber } from '../../../utils/detailViewFormatters';
import NatsPayloadViewer from '../../common/NatsPayloadViewer';
import './NatsStreamDetailView.css';

export interface NatsStreamDetailItem {
  cluster: string;
  accountName: string;
  streamName: string;
  consumers?: string[];
  /** Set when opened from the "Needs attention" panel - pre-selects this consumer and jumps to its first undelivered message. */
  focusConsumer?: string;
}

interface NatsStreamDetailViewProps {
  cluster: string;
  item: NatsStreamDetailItem;
}

const NatsStreamDetailView = ({ cluster, item }: NatsStreamDetailViewProps) => {
  const { streamName, consumers = [] } = item;

  const [seqInput, setSeqInput] = useState('1');
  const [message, setMessage] = useState<NatsRawMessage | null>(null);
  const [messageError, setMessageError] = useState<string | null>(null);
  const [messageLoading, setMessageLoading] = useState(false);

  const [subjectInput, setSubjectInput] = useState('>');

  const [selectedConsumer, setSelectedConsumer] = useState<string | null>(
    item.focusConsumer ?? null,
  );
  const [consumerInfo, setConsumerInfo] = useState<NatsConsumerInfoFull | null>(
    null,
  );
  const [consumerError, setConsumerError] = useState<string | null>(null);
  const [consumerLoading, setConsumerLoading] = useState(false);

  const loadMessageBySeq = useCallback(
    async (seq: number) => {
      setMessageLoading(true);
      setMessageError(null);
      try {
        const msg = await api.getNatsStreamMessage(cluster, streamName, seq);
        setMessage(msg);
      } catch (err: any) {
        setMessage(null);
        setMessageError(err.message || 'Failed to load message');
      } finally {
        setMessageLoading(false);
      }
    },
    [cluster, streamName],
  );

  const loadLatestBySubject = useCallback(
    async (subject: string) => {
      setMessageLoading(true);
      setMessageError(null);
      try {
        const msg = await api.getNatsStreamLastMessage(
          cluster,
          streamName,
          subject,
        );
        setMessage(msg);
        setSeqInput(String(msg.sequence));
      } catch (err: any) {
        setMessage(null);
        setMessageError(err.message || 'Failed to load message');
      } finally {
        setMessageLoading(false);
      }
    },
    [cluster, streamName],
  );

  const loadConsumer = useCallback(
    async (consumerName: string) => {
      setConsumerLoading(true);
      setConsumerError(null);
      try {
        const info = await api.getNatsConsumerInfo(
          cluster,
          streamName,
          consumerName,
        );
        setConsumerInfo(info);
        return info;
      } catch (err: any) {
        setConsumerInfo(null);
        setConsumerError(err.message || 'Failed to load consumer');
        return null;
      } finally {
        setConsumerLoading(false);
      }
    },
    [cluster, streamName],
  );

  useEffect(() => {
    if (!item.focusConsumer) return;
    setSelectedConsumer(item.focusConsumer);
    loadConsumer(item.focusConsumer).then((info) => {
      if (info) {
        const nextSeq = info.ack_floor.stream_seq + 1;
        setSeqInput(String(nextSeq));
        loadMessageBySeq(nextSeq);
      }
    });
    // Only on first mount for this focused consumer - the user drives everything after.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [item.focusConsumer]);

  const handleSelectConsumer = (name: string) => {
    setSelectedConsumer(name);
    loadConsumer(name);
  };

  return (
    <div className="nats-stream-detail">
      <div className="nats-stream-detail-header">
        <div className="nats-stream-detail-title">{streamName}</div>
        <div className="nats-stream-detail-sub">account {item.accountName}</div>
      </div>

      <div className="nats-stream-detail-section">
        <div className="nats-stream-detail-section-title">Message Browser</div>
        <div className="nats-stream-detail-controls">
          <label>
            Sequence
            <input
              type="number"
              min={1}
              value={seqInput}
              onChange={(e) => setSeqInput(e.target.value)}
            />
          </label>
          <button
            type="button"
            onClick={() => loadMessageBySeq(Number(seqInput))}
            disabled={messageLoading}
          >
            Load
          </button>
          <label>
            Subject
            <input
              type="text"
              value={subjectInput}
              onChange={(e) => setSubjectInput(e.target.value)}
              placeholder=">"
            />
          </label>
          <button
            type="button"
            onClick={() => loadLatestBySubject(subjectInput)}
            disabled={messageLoading}
          >
            Load newest
          </button>
        </div>
        {messageLoading && (
          <div className="nats-stream-detail-status">Loading…</div>
        )}
        {messageError && (
          <div className="nats-stream-detail-status error">{messageError}</div>
        )}
        {message && !messageLoading && (
          <div className="nats-stream-detail-message">
            <div className="nats-stream-detail-message-meta">
              <span>seq {message.sequence}</span>
              <span>{message.subject}</span>
              <span>{new Date(message.time).toLocaleString()}</span>
            </div>
            <NatsPayloadViewer data={message.data} headers={message.headers} />
          </div>
        )}
      </div>

      {consumers.length > 0 && (
        <div className="nats-stream-detail-section">
          <div className="nats-stream-detail-section-title">Consumers</div>
          <div className="nats-stream-detail-consumer-list">
            {consumers.map((name) => (
              <button
                key={name}
                type="button"
                className={`nats-stream-detail-consumer-chip${selectedConsumer === name ? ' active' : ''}`}
                onClick={() => handleSelectConsumer(name)}
              >
                {name}
              </button>
            ))}
          </div>
          {consumerLoading && (
            <div className="nats-stream-detail-status">Loading…</div>
          )}
          {consumerError && (
            <div className="nats-stream-detail-status error">
              {consumerError}
            </div>
          )}
          {consumerInfo && !consumerLoading && (
            <div className="nats-stream-detail-consumer-info">
              <div className="nats-consumer-info-grid">
                <span className="nats-consumer-info-label">Deliver policy</span>
                <span>{consumerInfo.config.deliver_policy ?? '-'}</span>
                <span className="nats-consumer-info-label">Filter subject</span>
                <span>{consumerInfo.config.filter_subject || '(none)'}</span>
                <span className="nats-consumer-info-label">
                  Delivered (stream seq)
                </span>
                <span>{consumerInfo.delivered.stream_seq}</span>
                <span className="nats-consumer-info-label">
                  Ack floor (stream seq)
                </span>
                <span>{consumerInfo.ack_floor.stream_seq}</span>
                <span className="nats-consumer-info-label">Pending</span>
                <span>{formatCompactNumber(consumerInfo.num_pending)}</span>
                <span className="nats-consumer-info-label">Ack pending</span>
                <span>{formatCompactNumber(consumerInfo.num_ack_pending)}</span>
                <span className="nats-consumer-info-label">Redelivered</span>
                <span>{formatCompactNumber(consumerInfo.num_redelivered)}</span>
              </div>
              <button
                type="button"
                onClick={() => {
                  const nextSeq = consumerInfo.ack_floor.stream_seq + 1;
                  setSeqInput(String(nextSeq));
                  loadMessageBySeq(nextSeq);
                }}
              >
                Inspect first undelivered message
              </button>
            </div>
          )}
        </div>
      )}
    </div>
  );
};

export default NatsStreamDetailView;
