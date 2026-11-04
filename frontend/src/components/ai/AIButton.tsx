import React from 'react';
import { SparkleIcon } from './AIIcons';
import { askAI, type AskSeed } from './aiContext';
import './AI.css';
import './AIChat.css';

interface Props {
  label: string;
  /** Built lazily on click so pages don't do work for every row. */
  seed: AskSeed | (() => AskSeed | null | undefined);
  title?: string;
  /** "sm" is the default small sparkle button; "action" matches inspector quick actions; "icon" is icon-only. */
  variant?: 'sm' | 'action' | 'icon';
  className?: string;
  disabled?: boolean;
}

/** The one AI button used at every entry point: a small sparkle button that
 * opens the chat with context Kanivet gathered. */
const AIButton: React.FC<Props> = ({ label, seed, title, variant = 'sm', className = '', disabled }) => {
  const onClick = (e: React.MouseEvent) => {
    e.stopPropagation();
    e.preventDefault();
    const s = typeof seed === 'function' ? seed() : seed;
    if (s) askAI(s);
  };
  if (variant === 'action') {
    return (
      <button type="button" className={`quick-action-btn ai-investigate-action ${className}`} onClick={onClick} title={title || label} aria-label={label} disabled={disabled}>
        <span className="ap-action-icon"><SparkleIcon /></span>
        <span className="ap-action-label">{label}</span>
      </button>
    );
  }
  if (variant === 'icon') {
    return (
      <button type="button" className={`ap-icon-btn ai-btn-icon ${className}`} onClick={onClick} title={title || label} aria-label={label} disabled={disabled}>
        <SparkleIcon size={13} />
      </button>
    );
  }
  return (
    <button type="button" className={`ap-btn ap-btn--sm ai-btn ${className}`} onClick={onClick} title={title || label} disabled={disabled}>
      <SparkleIcon size={12} />
      <span>{label}</span>
    </button>
  );
};

export default AIButton;
