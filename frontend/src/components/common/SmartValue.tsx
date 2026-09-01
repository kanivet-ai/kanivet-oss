import React, { useState } from 'react';
import { ChevronRightIcon } from '@radix-ui/react-icons';
import ClipboardCopy from './ClipboardCopy';
import ExpandIcon from '../icons/ExpandIcon';
import { Tooltip } from './Tooltip';
import './SmartValue.css';

interface SmartValueProps {
  value: any;
  copyText?: string;
  format?: 'auto' | 'json' | 'yaml' | 'resources' | 'labels' | 'annotations' | 'taints' | 'addresses' | 'secret';
  secretKey?: string;
  isRevealed?: boolean;
  onRevealChange?: (revealed: boolean) => void;
  isExpanded?: boolean;
  onExpandChange?: (expanded: boolean) => void;
  alwaysShowCopy?: boolean;
  forceExpanded?: boolean;
}

const formatBytes = (bytes: string): string => {
  const match = bytes.match(/^(\d+)([KMGT]i?)?$/);
  if (!match) return bytes;

  const num = parseInt(match[1]);
  const unit = match[2] || 'B';

  // Normalize unit (K -> Ki, M -> Mi, etc)
  const normalizedUnit = unit.length === 1 ? `${unit}i` : unit;

  const units = ['B', 'Ki', 'Mi', 'Gi', 'Ti'];
  const index = units.indexOf(normalizedUnit);

  if (index === -1) return bytes;

  // Convert to appropriate unit for display
  let value = num;
  let targetIndex = index;

  while (value >= 1024 && targetIndex < units.length - 1) {
    value = value / 1024;
    targetIndex++;
  }

  // Format with appropriate precision
  const formatted =
    value < 10
      ? value.toFixed(2)
      : value < 100
        ? value.toFixed(1)
        : value.toFixed(0);
  return `${formatted} ${units[targetIndex]}`;
};

const formatCPU = (cpu: string): string => {
  if (cpu.endsWith('m')) {
    const millicores = parseInt(cpu.slice(0, -1));
    if (millicores >= 1000) {
      return `${(millicores / 1000).toFixed(1)} CPU`;
    }
    return `${millicores}m`;
  }
  return cpu;
};

const decodeSecret = (encodedValue: string): string => {
  try {
    return atob(encodedValue);
  } catch (e) {
    return encodedValue;
  }
};

const detectFormat = (value: string): string => {
  if (value.includes('-----BEGIN CERTIFICATE-----') || value.includes('-----BEGIN RSA PRIVATE KEY-----')) {
    return 'certificate';
  }
  try {
    JSON.parse(value);
    return 'json';
  } catch {}
  return 'text';
};

const getMeaningfulLabel = (obj: any, fallback: string): string => {
  if (typeof obj !== 'object' || obj === null) return fallback;
  if (obj.name) return obj.name;
  if (obj.key) return obj.key;
  if (obj.type) return obj.type;
  if (obj.kind) return obj.kind;
  if (obj.operator) return obj.operator;
  return fallback;
};

const generatePreview = (value: any, maxLength = 60): string => {
  if (Array.isArray(value)) {
    if (value.length === 0) return '[]';
    const items = value.slice(0, 3).map((item) => {
      if (typeof item === 'object' && item !== null) {
        const label = getMeaningfulLabel(item, '');
        return label || '{…}';
      }
      if (typeof item === 'string') return item.length > 18 ? item.slice(0, 18) + '…' : item;
      return String(item);
    });
    const more = value.length > 3 ? ` +${value.length - 3}` : '';
    const preview = items.join(', ') + more;
    return preview.length > maxLength ? preview.slice(0, maxLength - 1) + '…' : preview;
  }
  if (typeof value === 'object' && value !== null) {
    const keys = Object.keys(value);
    if (keys.length === 0) return '{}';
    const preview = keys.slice(0, 3).join(', ') + (keys.length > 3 ? ` +${keys.length - 3}` : '');
    return preview.length > maxLength ? preview.slice(0, maxLength - 1) + '…' : preview;
  }
  return String(value).slice(0, maxLength);
};

interface ExpandableComplexValueProps {
  label: string;
  value: any;
  forceExpanded?: boolean;
}

const ExpandableComplexValue: React.FC<ExpandableComplexValueProps> = ({ label, value, forceExpanded }) => {
  const [expanded, setExpanded] = useState(false);
  const isExpanded = forceExpanded || expanded;
  const preview = generatePreview(value);

  if (!isExpanded) {
    return (
      <div className="map-item complex-value-collapsed" onClick={() => setExpanded(true)}>
        <ChevronRightIcon className="expand-chevron" />
        <span className="map-key">{label}</span>
        <span className="complex-preview">{preview}</span>
      </div>
    );
  }

  return (
    <div className="complex-value-expanded">
      <div className="complex-value-header" onClick={() => setExpanded(forceExpanded ? true : false)}>
        <ChevronRightIcon className="expand-chevron open" />
        <span className="complex-key">{label}</span>
        <span className="complex-meta">{generateInlineMeta(value)}</span>
      </div>
      <div className="complex-value-content">
        <SmartValue value={value} />
      </div>
    </div>
  );
};

const generateInlineMeta = (value: any): string => {
  if (Array.isArray(value)) {
    if (value.length === 0) return '';
    const noun = value.length === 1 ? 'item' : 'items';
    return `${value.length} ${noun}`;
  }
  if (typeof value === 'object' && value !== null) {
    const keys = Object.keys(value);
    if (keys.length === 0) return '';
    return `${keys.length} field${keys.length === 1 ? '' : 's'}`;
  }
  return '';
};

const SmartValue: React.FC<SmartValueProps> = ({
  value,
  copyText,
  format = 'auto',
  secretKey,
  isRevealed: externalIsRevealed,
  onRevealChange,
  isExpanded: externalIsExpanded,
  onExpandChange,
  alwaysShowCopy,
  forceExpanded,
}) => {
  const [internalIsExpanded, setInternalIsExpanded] = useState(false);
  const [internalIsRevealed, setInternalIsRevealed] = useState(false);

  const isExpanded = externalIsExpanded !== undefined ? externalIsExpanded : internalIsExpanded;
  const isRevealed = externalIsRevealed !== undefined ? externalIsRevealed : internalIsRevealed;

  const handleExpandChange = (expanded: boolean) => {
    if (onExpandChange) {
      onExpandChange(expanded);
    } else {
      setInternalIsExpanded(expanded);
    }
  };

  const handleRevealChange = (revealed: boolean) => {
    if (onRevealChange) {
      onRevealChange(revealed);
    } else {
      setInternalIsRevealed(revealed);
    }
  };

  const renderValue = () => {
    // Handle null/undefined
    if (value === null) {
      return <span className="smart-value-null">null</span>;
    }
    if (value === undefined) {
      return <span className="smart-value-empty">-</span>;
    }

    // Handle secret format
    if (format === 'secret' && typeof value === 'string') {
      const decodedValue = decodeSecret(value);
      const lines = decodedValue.split('\n');
      const isMultiline = lines.length > 3 || decodedValue.length > 100;
      const detectedFormat = detectFormat(decodedValue);

      let displayValue = isRevealed ? decodedValue : '••••••••••••••••';
      if (isRevealed && !isExpanded && isMultiline) {
        displayValue = lines.slice(0, 3).join('\n');
        if (lines.length > 3) displayValue += '\n...';
      }

      const size = formatBytes(String(decodedValue.length));

      return (
        <div className={`smart-value-secret ${detectedFormat}`}>
          <div className="secret-header">
            <div className="secret-key-info">
              {isMultiline && isRevealed && (
                <button
                  className="expand-button"
                  onClick={() => handleExpandChange(!isExpanded)}
                  title={isExpanded ? 'Collapse' : 'Expand'}
                >
                  <ExpandIcon expanded={isExpanded} />
                </button>
              )}
              {secretKey && <span className="secret-key" title={secretKey}>{secretKey}</span>}
              {detectedFormat !== 'text' && isRevealed && (
                <span className={`format-badge format-${detectedFormat}`}>{detectedFormat.toUpperCase()}</span>
              )}
            </div>
            <div className="secret-meta">
              <span className="secret-size">{size}</span>
              {isRevealed && lines.length > 1 && (
                <span className="secret-lines">{lines.length} lines</span>
              )}
              <button
                className="reveal-button"
                onClick={() => handleRevealChange(!isRevealed)}
                title={isRevealed ? 'Hide value' : 'Show value'}
              >
                {isRevealed ? (
                  <svg width="16" height="16" viewBox="0 0 16 16" fill="currentColor">
                    <path d="M8 2C4.5 2 1.5 5 0.5 8c1 3 4 6 7.5 6s6.5-3 7.5-6c-1-3-4-6-7.5-6zm0 10c-2.2 0-4-1.8-4-4s1.8-4 4-4 4 1.8 4 4-1.8 4-4 4z"/>
                    <circle cx="8" cy="8" r="2"/>
                  </svg>
                ) : (
                  <svg width="16" height="16" viewBox="0 0 16 16" fill="currentColor">
                    <path d="M13.4 2.6l-11 11 1.4 1.4 11-11z"/>
                    <path d="M8 2C4.5 2 1.5 5 0.5 8c.5 1.5 1.5 2.8 2.8 3.7l1.5-1.5C3.7 9.3 3 8.2 3 7c0-2.2 1.8-4 4-4 1.2 0 2.3.5 3 1.3l1.5-1.5C10.5 2.3 9.3 2 8 2zm7.5 6c-.5-1.5-1.5-2.8-2.8-3.7l-1.5 1.5c1.1.9 1.8 2 1.8 3.2 0 2.2-1.8 4-4 4-1.2 0-2.3-.5-3-1.3l-1.5 1.5c1 .5 2.2.8 3.5.8 3.5 0 6.5-3 7.5-6z"/>
                  </svg>
                )}
              </button>
              {isRevealed && <ClipboardCopy text={decodedValue} />}
            </div>
          </div>
          {isRevealed ? (
            <div className={`secret-value ${isExpanded ? 'expanded' : ''} ${isMultiline && !isExpanded ? 'truncated' : ''}`}>
              <pre className={`language-${detectedFormat}`}>{displayValue}</pre>
            </div>
          ) : (
            <div className="secret-value masked" title="Click the eye icon to reveal and copy this secret">
              <pre>{displayValue}</pre>
            </div>
          )}
        </div>
      );
    }

    // Handle resources format (only when explicitly set, not auto-detected)
    if (format === 'resources') {
      return (
        <div className="smart-value-resources">
          {value.cpu && (
            <div className="resource-item">
              <span className="resource-label">CPU</span>
              <span className="resource-value">{formatCPU(value.cpu)}</span>
            </div>
          )}
          {value.memory && (
            <div className="resource-item">
              <span className="resource-label">Memory</span>
              <span className="resource-value">
                {formatBytes(value.memory)}
              </span>
            </div>
          )}
          {value.pods && (
            <div className="resource-item">
              <span className="resource-label">Pods</span>
              <span className="resource-value">{value.pods}</span>
            </div>
          )}
          {value['ephemeral-storage'] && (
            <div className="resource-item">
              <span className="resource-label">Storage</span>
              <span className="resource-value">{formatBytes(value['ephemeral-storage'])}</span>
            </div>
          )}
        </div>
      );
    }

    if (format === 'taints' && Array.isArray(value)) {
      if (value.length === 0) {
        return <span className="smart-value-empty" title="No value">—</span>;
      }
      return (
        <div className="smart-value-taints">
          {value.map((taint, index) => {
            const copyText = `${taint.key}${taint.value ? '=' + taint.value : ''}:${taint.effect}`;
            return (
              <div key={index} className="taint-item">
                <span className="taint-key">{taint.key}</span>
                {taint.value && (
                  <>
                    <span className="taint-equals">=</span>
                    <span className="taint-value">{taint.value}</span>
                  </>
                )}
                <span className={`taint-effect effect-${taint.effect.toLowerCase()}`}>
                  {taint.effect}
                </span>
                <ClipboardCopy text={copyText} />
              </div>
            );
          })}
        </div>
      );
    }

    if (format === 'addresses' && Array.isArray(value)) {
      if (value.length === 0) {
        return <span className="smart-value-empty" title="No value">—</span>;
      }
      return (
        <div className="smart-value-addresses">
          {value.map((addr, index) => (
            <div key={index} className="address-item">
              <span className="address-type">{addr.type}</span>
              <span className="address-value">{addr.address}</span>
              <ClipboardCopy text={addr.address} />
            </div>
          ))}
        </div>
      );
    }

    // Handle labels/annotations
    if (
      format === 'labels' ||
      format === 'annotations' ||
      (format === 'auto' && typeof value === 'object' && !Array.isArray(value))
    ) {
      const entries = Object.entries(value);
      if (entries.length === 0) {
        return <span className="smart-value-empty" title="No value">—</span>;
      }

      return (
        <div className="smart-value-map">
          {entries.map(([key, val]) => {
            const isEmptyArray = Array.isArray(val) && val.length === 0;
            if (val !== null && typeof val === 'object' && !isEmptyArray) {
              return <ExpandableComplexValue key={key} label={key} value={val} forceExpanded={forceExpanded} />;
            }
            // (top-level object spec entries inherit forceExpanded above; nested objects collapse by default)
            if (isEmptyArray) {
              return (
                <div key={key} className="map-item">
                  <span className="map-key">{key}</span>
                  <span className="map-value">
                    <SmartValue value={val} copyText="[]" />
                  </span>
                </div>
              );
            }
            const valueStr = String(val);
            const isResourceKey = key === 'cpu' || key === 'memory';
            return (
              <div key={key} className={`map-item ${isResourceKey ? 'map-item-resource' : ''}`}>
                <span className={`map-key ${isResourceKey ? 'map-key-resource' : ''}`}>{key}</span>
                <span className={`map-value ${isResourceKey ? 'map-value-resource' : ''}`}>
                  <SmartValue value={val} copyText={valueStr} />
                </span>
              </div>
            );
          })}
        </div>
      );
    }

    // Handle JSON
    if (
      format === 'json' ||
      (format === 'auto' &&
        typeof value === 'object' &&
        !Array.isArray(value) &&
        Object.keys(value).length > 0)
    ) {
      try {
        const formatted = JSON.stringify(value, null, 2);
        return (
          <pre className="smart-value-json" aria-label="json-block">
            <code>{formatted}</code>
          </pre>
        );
      } catch {}
    }

    // Handle arrays
    if (Array.isArray(value)) {
      if (value.length === 0)
        return <span className="smart-value-empty">[]</span>;

      // Check if this is an array of key-value objects (like tags)
      const isKeyValueArray = value.every(item => 
        typeof item === 'object' && 
        item !== null &&
        'key' in item && 
        'value' in item &&
        Object.keys(item).length === 2
      );

      if (isKeyValueArray) {
        return (
          <div className="smart-value-map">
            {value.map((item, index) => (
              <div key={index} className="map-item">
                <span className="map-key">{item.key}</span>
                <span className="map-value">
                  <SmartValue value={item.value} copyText={String(item.value)} />
                </span>
              </div>
            ))}
          </div>
        );
      }

      // Check if this is an array of name-value objects
      const isNameValueArray = value.every(item => 
        typeof item === 'object' && 
        item !== null &&
        'name' in item && 
        'value' in item &&
        Object.keys(item).length === 2
      );

      if (isNameValueArray) {
        return (
          <div className="smart-value-map">
            {value.map((item, index) => (
              <div key={index} className="map-item">
                <span className="map-key">{item.name}</span>
                <span className="map-value">
                  <SmartValue value={item.value} copyText={String(item.value)} />
                </span>
              </div>
            ))}
          </div>
        );
      }

      return (
        <div className="smart-value-map">
          {value.map((item, index) => {
            const isComplexItem = item !== null && typeof item === 'object';
            if (isComplexItem) {
              const label = getMeaningfulLabel(item, '');
              if (label) {
                return <ExpandableComplexValue key={index} label={label} value={item} />;
              }
              return (
                <div key={index} className="complex-value-expanded">
                  <div className="complex-value-content array-item-no-label">
                    <SmartValue value={item} />
                  </div>
                </div>
              );
            }
            return (
              <div key={index} className="map-item">
                <span className="map-value map-value-full-width">
                  <SmartValue value={item} copyText={String(item)} />
                </span>
              </div>
            );
          })}
        </div>
      );
    }

    // Handle boolean
    if (typeof value === 'boolean') {
      return (
        <span className={`smart-value-boolean ${value ? 'true' : 'false'}`}>
          {value ? 'true' : 'false'}
        </span>
      );
    }

    // Handle numbers
    if (typeof value === 'number') {
      const isPort = value >= 0 && value <= 65535 && Number.isInteger(value);
      return (
        <span className="smart-value-number">{isPort ? value : value.toLocaleString()}</span>
      );
    }

    // Handle URLs
    if (
      typeof value === 'string' &&
      (value.startsWith('http://') || value.startsWith('https://'))
    ) {
      return (
        <a
          href={value}
          target="_blank"
          rel="noopener noreferrer"
          className="smart-value-link"
        >
          {value}
        </a>
      );
    }

    // Handle JSON strings (including compact ones)
    if (
      typeof value === 'string' &&
      (value.startsWith('{') || value.startsWith('['))
    ) {
      try {
        const parsed = JSON.parse(value);
        const formatted = JSON.stringify(parsed, null, 2);
        return (
          <pre className="smart-value-json">
            <code>{formatted}</code>
          </pre>
        );
      } catch {
        // Not valid JSON, continue to multiline handling
      }
    }

    // Handle multiline strings
    if (typeof value === 'string' && value.includes('\n')) {
      const lines = value.split('\n');
      const maxLines = 4;
      const isLong = lines.length > maxLines;
      return (
        <div className={`smart-value-multiline-expandable ${isExpanded ? 'expanded' : ''}`}>
          <pre onClick={isLong ? () => handleExpandChange(!isExpanded) : undefined}>
            {isExpanded || !isLong ? value : lines.slice(0, maxLines).join('\n')}
          </pre>
          {!isExpanded && isLong && <div className="expand-fade" onClick={() => handleExpandChange(true)} />}
        </div>
      );
    }

    // Handle fieldRef/resourceRef-like shell expressions: $(...)
    if (typeof value === 'string' && /\$\([^)]+\)/.test(value)) {
      return <span className="smart-value-expression">{value}</span>;
    }

    // Handle status/phase values with semantic colors
    if (typeof value === 'string') {
      const successStatuses = ['Running', 'Succeeded', 'Active', 'Bound', 'Ready', 'Complete', 'Completed', 'Healthy', 'Available', 'True'];
      const warningStatuses = ['Pending', 'Waiting', 'ContainerCreating', 'Terminating', 'Unknown', 'Progressing', 'PodInitializing', 'ContainerStatusUnknown'];
      const errorStatuses = ['Failed', 'Error', 'CrashLoopBackOff', 'ImagePullBackOff', 'ErrImagePull', 'OOMKilled', 'Evicted', 'BackOff', 'InvalidImageName', 'CreateContainerError', 'RunContainerError', 'False'];

      if (successStatuses.includes(value)) {
        return <span className="smart-value-status status-success">{value}</span>;
      }
      if (warningStatuses.includes(value)) {
        return <span className="smart-value-status status-warning">{value}</span>;
      }
      if (errorStatuses.includes(value)) {
        return <span className="smart-value-status status-error">{value}</span>;
      }

      // Handle service types
      const serviceTypes: Record<string, string> = {
        'ClusterIP': 'cluster-ip',
        'NodePort': 'node-port',
        'LoadBalancer': 'load-balancer',
        'ExternalName': 'external-name'
      };
      if (serviceTypes[value]) {
        return <span className={`smart-value-service-type type-${serviceTypes[value]}`}>{value}</span>;
      }

      // Handle restart policies
      const restartPolicies: Record<string, string> = {
        'Always': 'always',
        'OnFailure': 'on-failure',
        'Never': 'never'
      };
      if (restartPolicies[value]) {
        return <span className={`smart-value-restart-policy policy-${restartPolicies[value]}`}>{value}</span>;
      }

      // Handle API version strings (v1, v1beta1, v1alpha1)
      if (/^v\d+(?:alpha\d+|beta\d+)?$/.test(value)) {
        const versionClass = value.includes('alpha') ? 'alpha' : value.includes('beta') ? 'beta' : 'stable';
        return <span className={`smart-value-version version-${versionClass}`}>{value}</span>;
      }
    }

    // Handle Docker image references (e.g., image:tag or registry/image:tag)
    if (
      typeof value === 'string' &&
      value.includes(':') &&
      /^[a-z0-9][\w\-\.\/]*:[a-z0-9][\w\-\.]*$/i.test(value) &&
      !value.startsWith('http') // Not URLs
    ) {
      const element = (
        <span
          className={`smart-value-docker-image ${isExpanded ? 'expanded' : ''}`}
          onClick={() => handleExpandChange(!isExpanded)}
        >
          {value}
        </span>
      );
      return isExpanded ? element : <Tooltip content={value} side="top">{element}</Tooltip>;
    }

    // Handle UUID/hash-like strings
    if (
      typeof value === 'string' &&
      (/^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/i.test(value) || // UUID
       /^[a-f0-9]{40,}$/i.test(value)) // Long hex strings (SHA1, SHA256, etc)
    ) {
      const element = (
        <span
          className={`smart-value-uuid ${isExpanded ? 'expanded' : ''}`}
          onClick={() => handleExpandChange(!isExpanded)}
        >
          {value}
        </span>
      );
      return isExpanded ? element : <Tooltip content={value} side="top">{element}</Tooltip>;
    }

    // Handle long strings (like Azure resource IDs) - threshold of 25 characters
    if (
      typeof value === 'string' &&
      value.length > 25 &&
      !value.includes('\n') && // Not multiline
      !value.startsWith('http') // Not URLs (handled separately)
    ) {
      const element = (
        <span
          className={`smart-value-long ${isExpanded ? 'expanded' : ''}`}
          onClick={() => handleExpandChange(!isExpanded)}
        >
          {value}
        </span>
      );
      return isExpanded ? element : <Tooltip content={value} side="top">{element}</Tooltip>;
    }

    // Handle base64 encoded strings
    if (
      typeof value === 'string' &&
      value.length > 20 &&
      /^[A-Za-z0-9+/]+=*$/.test(value)
    ) {
      return (
        <Tooltip content={`Base64 (${value.length} chars)`} side="top">
          <span className="smart-value-base64">
            {value.substring(0, 20)}...
          </span>
        </Tooltip>
      );
    }

    // Default string handling
    return <span className="smart-value-string">{String(value)}</span>;
  };

  const renderedValue = renderValue();

  // Don't wrap if it's already a complex component
  if (React.isValidElement(renderedValue)) {
    const className = (renderedValue.props as any)?.className || '';
    if (
      className.includes('smart-value-map') ||
      className.includes('smart-value-json') ||
      className.includes('smart-value-list')
    ) {
      return renderedValue;
    }
  }

  // For simple values, wrap with hoverable container
  if (copyText) {
    return (
      <span className="smart-value-wrapper">
        {renderedValue}
        <ClipboardCopy text={copyText} alwaysShow={alwaysShowCopy} />
      </span>
    );
  }

  return renderedValue;
};

export default SmartValue;
