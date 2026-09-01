import React from 'react';
import './FinOpsLoadingSkeleton.css';

export const FinOpsLoadingSkeleton: React.FC = () => {
  return (
    <div className="finops-skeleton">
      <div className="skeleton-filters">
        <div className="skeleton-search" />
        <div className="skeleton-filter-btn" />
      </div>

      <div className="skeleton-summary">
        <div className="skeleton-cards">
          <div className="skeleton-card primary" />
          <div className="skeleton-card" />
          <div className="skeleton-card" />
        </div>
        <div className="skeleton-idle-card" />
        <div className="skeleton-efficiency-meter" />
      </div>

      <div className="skeleton-section">
        <div className="skeleton-section-header">
          <div className="skeleton-title" />
          <div className="skeleton-count" />
        </div>
        <div className="skeleton-table">
          <div className="skeleton-table-header" />
          {[1, 2, 3, 4, 5].map(i => (
            <div key={i} className="skeleton-table-row">
              <div className="skeleton-cell wide" />
              <div className="skeleton-cell" />
              <div className="skeleton-cell" />
              <div className="skeleton-cell" />
              <div className="skeleton-cell" />
            </div>
          ))}
        </div>
      </div>

      <div className="skeleton-section">
        <div className="skeleton-section-header">
          <div className="skeleton-title" />
          <div className="skeleton-count" />
        </div>
        <div className="skeleton-table">
          <div className="skeleton-table-header" />
          {[1, 2, 3].map(i => (
            <div key={i} className="skeleton-table-row">
              <div className="skeleton-cell wide" />
              <div className="skeleton-cell" />
              <div className="skeleton-cell" />
              <div className="skeleton-cell" />
            </div>
          ))}
        </div>
      </div>
    </div>
  );
};

