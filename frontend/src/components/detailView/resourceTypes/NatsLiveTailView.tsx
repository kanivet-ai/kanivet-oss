import { useState } from 'react';
import useNatsLiveTail from '../../../hooks/useNatsLiveTail';
import NatsPayloadViewer from '../../common/NatsPayloadViewer';
import './NatsLiveTailView.css';

export interface NatsLiveTailItem {
  cluster: string;
  defaultSubject?: string;
}

interface NatsLiveTailViewProps {
  cluster: string;
  item: NatsLiveTailItem;
}

const NatsLiveTailView = ({ cluster, item }: NatsLiveTailViewProps) => {
  const [subjectInput, setSubjectInput] = useState(item.defaultSubject ?? '>');
  const [activeSubject, setActiveSubject] = useState<string | null>(null);
  const [selectedIndex, setSelectedIndex] = useState<number | null>(null);

  const { messages, connected, error, clear } = useNatsLiveTail({
    cluster,
    subject: activeSubject ?? '',
    enabled: activeSubject !== null,
  });

  const start = () => {
    setSelectedIndex(null);
    setActiveSubject(subjectInput.trim() || '>');
  };
  const stop = () => {
    setActiveSubject(null);
    setSelectedIndex(null);
  };

  const selected = selectedIndex !== null ? messages[selectedIndex] : undefined;

  return (
    <div className="nats-live-tail">
      <div className="nats-live-tail-controls">
        <label>
          Subject
          <input
            type="text"
            value={subjectInput}
            onChange={(e) => setSubjectInput(e.target.value)}
            placeholder=">"
            disabled={activeSubject !== null}
          />
        </label>
        {activeSubject === null ? (
          <button type="button" onClick={start}>
            Start
          </button>
        ) : (
          <button type="button" onClick={stop}>
            Stop
          </button>
        )}
        <button type="button" onClick={clear} disabled={messages.length === 0}>
          Clear
        </button>
        {activeSubject !== null && (
          <span
            className={`nats-live-tail-status ${connected ? 'connected' : 'disconnected'}`}
          >
            {connected ? 'live' : 'connecting…'}
          </span>
        )}
        {error && <span className="nats-live-tail-status error">{error}</span>}
      </div>

      <div className="nats-live-tail-body">
        <div className="nats-live-tail-list">
          {messages.length === 0 ? (
            <div className="nats-live-tail-empty">
              {activeSubject === null
                ? 'Enter a subject and press Start.'
                : 'Waiting for messages…'}
            </div>
          ) : (
            messages
              .slice()
              .reverse()
              .map((msg, i) => {
                const realIndex = messages.length - 1 - i;
                return (
                  <div
                    key={`${msg.receivedAt}-${realIndex}`}
                    className={`nats-live-tail-row${selectedIndex === realIndex ? ' active' : ''}`}
                    onClick={() => setSelectedIndex(realIndex)}
                  >
                    <span className="nats-live-tail-time">
                      {new Date(msg.receivedAt).toLocaleTimeString()}
                    </span>
                    <span className="nats-live-tail-subject">
                      {msg.subject}
                    </span>
                  </div>
                );
              })
          )}
        </div>
        {selected && (
          <div className="nats-live-tail-detail">
            <NatsPayloadViewer
              data={selected.data}
              headers={selected.headers}
            />
          </div>
        )}
      </div>
    </div>
  );
};

export default NatsLiveTailView;
