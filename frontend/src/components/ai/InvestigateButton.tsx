import React from 'react';
import { useAIStore } from '../../store/aiStore';
import type { InvestigationTarget } from '../../services/api/ai';
import { SparkleIcon } from './AIIcons';
import './AI.css';

interface Props {
  target: InvestigationTarget;
  /** "action" renders as an inspector quick action; "inline" as a small button. */
  variant?: 'action' | 'inline';
}

/** Starts an AI investigation of a resource (or an incident). */
const InvestigateButton: React.FC<Props> = ({ target, variant = 'action' }) => {
  const investigate = useAIStore((s) => s.investigate);
  const onClick = (e: React.MouseEvent) => {
    e.stopPropagation();
    void investigate(target);
  };
  if (variant === 'inline') {
    return (
      <button type="button" className="ap-btn ap-btn--sm ai-investigate-inline" onClick={onClick} title="Investigate with an AI agent">
        <SparkleIcon size={12} />
        <span>Investigate</span>
      </button>
    );
  }
  return (
    <button type="button" className="quick-action-btn ai-investigate-action" onClick={onClick} title="Investigate with an AI agent" aria-label="Investigate">
      <span className="ap-action-icon"><SparkleIcon /></span>
      <span className="ap-action-label">Investigate</span>
    </button>
  );
};

export default InvestigateButton;
