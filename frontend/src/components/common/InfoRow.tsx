import React from 'react';
import SmartValue from './SmartValue';
import ClipboardCopy from './ClipboardCopy';
import './InfoRow.css';

interface InfoRowProps {
  label: string;
  value?: any;
  children?: React.ReactNode;
  copyText?: string;
  format?: 'auto' | 'json' | 'yaml' | 'resources' | 'labels' | 'annotations' | 'taints' | 'addresses' | 'secret';
  icon?: React.ReactNode;
  alwaysShowCopy?: boolean;
}

const InfoRow: React.FC<InfoRowProps> = ({
  label,
  value,
  children,
  copyText,
  format,
  icon,
  alwaysShowCopy,
}) => {
  const isKeyValueArray = Array.isArray(value) && value.length > 0 && value.every(item => 
    typeof item === 'object' && 
    item !== null &&
    (('key' in item && 'value' in item) || ('name' in item && 'value' in item)) &&
    Object.keys(item).length === 2
  );

  const isComplexValue = value !== undefined && 
    typeof value === 'object' && 
    value !== null && 
    !(value instanceof Date) &&
    !isKeyValueArray &&
    (Array.isArray(value) || 
     (!Array.isArray(value) && Object.keys(value).length > 0));

  if (isComplexValue || isKeyValueArray || format === 'labels' || format === 'annotations') {
    return (
      <div className="info-row-complex">
        <div className="info-label-complex">
          {icon && <span className="info-label-icon">{icon}</span>}
          <span>{label}</span>
        </div>
        <div className="info-value-complex">
          <SmartValue value={value} copyText={copyText} format={format} alwaysShowCopy={alwaysShowCopy} />
        </div>
      </div>
    );
  }

  return (
    <div className="info-row">
      <div className="info-label">
        {icon && <span className="info-label-icon">{icon}</span>}
        <span>{label}</span>
      </div>
      <div className="info-value">
        {value !== undefined ? (
          <SmartValue value={value} copyText={copyText} format={format} alwaysShowCopy={alwaysShowCopy} />
        ) : children ? (
          <span className="info-value-text">
            {copyText && <ClipboardCopy text={copyText} alwaysShow={alwaysShowCopy} />}
            {children}
          </span>
        ) : null}
      </div>
    </div>
  );
};

export default InfoRow;

