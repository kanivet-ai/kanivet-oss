import React from 'react';
import ClipboardCopy from './ClipboardCopy';
import SmartValue from './SmartValue';
import './PropertyRow.css';

interface PropertyRowProps {
  label: string;
  value?: React.ReactNode;
  copyText?: string;
  icon?: React.ReactNode;
  muted?: boolean;
  mono?: boolean;
  onClick?: () => void;
  className?: string;
}

const isPrimitive = (value: any): boolean => {
  if (value === null || value === undefined) return true;
  const type = typeof value;
  return type === 'string' || type === 'number' || type === 'boolean';
};

const shouldUseSmartValue = (value: any): boolean => {
  if (React.isValidElement(value)) return false;
  if (isPrimitive(value)) return true;
  if (typeof value === 'object') return true;
  return false;
};

const PropertyRow: React.FC<PropertyRowProps> = ({
  label,
  value,
  copyText,
  icon,
  mono,
  onClick,
  className = '',
}) => {
  const isClickable = !!onClick;
  const rowClass = [
    'property-row',
    isClickable && 'property-row-clickable',
    className,
  ].filter(Boolean).join(' ');

  const renderValue = () => {
    if (value === undefined || value === null) return null;
    if (shouldUseSmartValue(value)) {
      return <SmartValue value={value} copyText={copyText} />;
    }
    return (
      <>
        <span className="property-value-text">{value}</span>
        {copyText && <ClipboardCopy text={copyText} />}
      </>
    );
  };

  return (
    <div className={rowClass} onClick={onClick}>
      <div className="property-label">
        {icon && <span className="property-label-icon">{icon}</span>}
        <span>{label}</span>
      </div>
      <div className={`property-value${mono ? ' mono' : ''}`}>
        {renderValue()}
      </div>
    </div>
  );
};

export default PropertyRow;
