import { useMemo, useState } from 'react';
import './NatsPayloadViewer.css';

type PayloadFormat = 'json' | 'text' | 'hex' | 'base64';

interface NatsPayloadViewerProps {
  /** Base64-encoded raw bytes - the one wire format every NATS message payload uses on this app's wire. */
  data: string;
  headers?: Record<string, string[]>;
  defaultFormat?: PayloadFormat;
}

const FORMATS: { id: PayloadFormat; label: string }[] = [
  { id: 'json', label: 'JSON' },
  { id: 'text', label: 'Text' },
  { id: 'hex', label: 'Hex' },
  { id: 'base64', label: 'Base64' },
];

const decodeBinary = (base64: string): string => {
  try {
    return atob(base64);
  } catch {
    return '';
  }
};

const toHexDump = (binary: string): string => {
  const bytes = Array.from(binary, (c) => c.charCodeAt(0));
  const lines: string[] = [];
  for (let offset = 0; offset < bytes.length; offset += 16) {
    const chunk = bytes.slice(offset, offset + 16);
    const hex = chunk.map((b) => b.toString(16).padStart(2, '0')).join(' ');
    const ascii = chunk
      .map((b) => (b >= 32 && b < 127 ? String.fromCharCode(b) : '.'))
      .join('');
    lines.push(
      `${offset.toString(16).padStart(8, '0')}  ${hex.padEnd(47, ' ')}  ${ascii}`,
    );
  }
  return lines.join('\n');
};

const tryPrettyJson = (binary: string): string | null => {
  try {
    return JSON.stringify(JSON.parse(binary), null, 2);
  } catch {
    return null;
  }
};

const NatsPayloadViewer = ({
  data,
  headers,
  defaultFormat = 'json',
}: NatsPayloadViewerProps) => {
  const binary = useMemo(() => decodeBinary(data), [data]);
  const prettyJson = useMemo(() => tryPrettyJson(binary), [binary]);
  const [format, setFormat] = useState<PayloadFormat>(
    defaultFormat === 'json' && !prettyJson ? 'text' : defaultFormat,
  );

  const headerEntries = Object.entries(headers ?? {});

  const content = (() => {
    switch (format) {
      case 'json':
        return prettyJson ?? binary;
      case 'hex':
        return toHexDump(binary);
      case 'base64':
        return data;
      case 'text':
      default:
        return binary;
    }
  })();

  return (
    <div className="nats-payload-viewer">
      {headerEntries.length > 0 && (
        <div className="nats-payload-headers">
          {headerEntries.map(([key, values]) => (
            <div key={key} className="nats-payload-header-row">
              <span className="nats-payload-header-key">{key}</span>
              <span className="nats-payload-header-value">
                {values.join(', ')}
              </span>
            </div>
          ))}
        </div>
      )}
      <div className="nats-payload-format-switch">
        {FORMATS.map((f) => (
          <button
            key={f.id}
            type="button"
            className={`nats-payload-format-btn${format === f.id ? ' active' : ''}`}
            onClick={() => setFormat(f.id)}
            disabled={f.id === 'json' && !prettyJson}
          >
            {f.label}
          </button>
        ))}
      </div>
      <pre className="nats-payload-content">{content || '(empty payload)'}</pre>
    </div>
  );
};

export default NatsPayloadViewer;
