import React from 'react';
import { Cross2Icon } from '@radix-ui/react-icons';
import { ClusterCostSummary, formatCost, formatBytes, formatMilliCores } from '../../types/finops';
import './EfficiencyExplainer.css';

interface EfficiencyExplainerProps {
  summary: ClusterCostSummary;
  onClose: () => void;
}

export const EfficiencyExplainer: React.FC<EfficiencyExplainerProps> = ({ summary, onClose }) => {
  const wastedCpu = summary.totalCpu - summary.requestedCpu;
  const wastedMemory = summary.totalMemory - summary.requestedMemory;
  const wasteMultiplier = summary.overallEfficiency > 0 ? (100 / summary.overallEfficiency) : 0;

  return (
    <div className="efficiency-explainer-overlay" onClick={onClose}>
      <div className="efficiency-explainer-modal" onClick={(e) => e.stopPropagation()}>
        <div className="explainer-header">
          <h2>Understanding Resource Efficiency</h2>
          <button className="close-button" onClick={onClose}>
            <Cross2Icon />
          </button>
        </div>

        <div className="explainer-content">
          <div className="explainer-section">
            <h3>What is Resource Efficiency?</h3>
            <p>
              Resource efficiency measures how much of your cluster's capacity is actually being used by your workloads.
              It compares the resources your pods <strong>request</strong> versus what your cluster <strong>provides</strong>.
            </p>
            <div className="info-callout">
              <strong>Note:</strong> This measures requests, not actual usage. Pods with 0% efficiency have no resource requests defined.
            </div>
            <div className="formula-card">
              <div className="formula-label">Formula</div>
              <code className="formula">Efficiency = (Requested / Allocated) × 100%</code>
              <div className="formula-breakdown">
                <div className="formula-item">
                  <span className="formula-key">Requested:</span>
                  <span className="formula-value">What pods ask for in resource requests</span>
                </div>
                <div className="formula-item">
                  <span className="formula-key">Allocated:</span>
                  <span className="formula-value">Total capacity your cluster provides</span>
                </div>
              </div>
            </div>
          </div>

          <div className="explainer-section">
            <h3>Your Cluster's Status</h3>
            <div className="cluster-stats">
              <div className="cluster-stat">
                <div className="stat-header">
                  <span className="stat-title">Overall Efficiency</span>
                  <span className="stat-big" style={{ 
                    color: summary.overallEfficiency >= 70 ? 'var(--efficiency-excellent)' :
                           summary.overallEfficiency >= 50 ? 'var(--efficiency-good)' :
                           summary.overallEfficiency >= 30 ? 'var(--efficiency-fair)' :
                           'var(--efficiency-critical)'
                  }}>
                    {summary.overallEfficiency.toFixed(0)}%
                  </span>
                </div>
                {summary.overallEfficiency < 70 && (
                  <p className="stat-warning">
                    You're paying for <strong>{wasteMultiplier.toFixed(1)}x</strong> more resources than you're using.
                  </p>
                )}
              </div>

              <div className="resource-comparison">
                <div className="comparison-row">
                  <div className="comparison-label">CPU</div>
                  <div className="comparison-bars">
                    <div className="comparison-bar">
                      <span className="bar-label">Allocated</span>
                      <div className="bar-track">
                        <div className="bar-fill total" style={{ width: '100%' }} />
                      </div>
                      <span className="bar-value">{formatMilliCores(summary.totalCpu)}</span>
                    </div>
                    <div className="comparison-bar">
                      <span className="bar-label">Requested</span>
                      <div className="bar-track">
                        <div className="bar-fill used" style={{ width: `${summary.cpuEfficiency}%` }} />
                      </div>
                      <span className="bar-value">{formatMilliCores(summary.requestedCpu)}</span>
                    </div>
                    <div className="comparison-bar waste">
                      <span className="bar-label">Wasted</span>
                      <div className="bar-track">
                        <div className="bar-fill waste-fill" style={{ width: `${100 - summary.cpuEfficiency}%` }} />
                      </div>
                      <span className="bar-value">{formatMilliCores(wastedCpu)}</span>
                    </div>
                  </div>
                  <div className="comparison-efficiency" style={{ color: summary.cpuEfficiency >= 50 ? 'var(--efficiency-good)' : 'var(--efficiency-critical)' }}>
                    {summary.cpuEfficiency.toFixed(0)}%
                  </div>
                </div>

                <div className="comparison-row">
                  <div className="comparison-label">Memory</div>
                  <div className="comparison-bars">
                    <div className="comparison-bar">
                      <span className="bar-label">Allocated</span>
                      <div className="bar-track">
                        <div className="bar-fill total" style={{ width: '100%' }} />
                      </div>
                      <span className="bar-value">{formatBytes(summary.totalMemory)}</span>
                    </div>
                    <div className="comparison-bar">
                      <span className="bar-label">Requested</span>
                      <div className="bar-track">
                        <div className="bar-fill used" style={{ width: `${summary.memoryEfficiency}%` }} />
                      </div>
                      <span className="bar-value">{formatBytes(summary.requestedMemory)}</span>
                    </div>
                    <div className="comparison-bar waste">
                      <span className="bar-label">Wasted</span>
                      <div className="bar-track">
                        <div className="bar-fill waste-fill" style={{ width: `${100 - summary.memoryEfficiency}%` }} />
                      </div>
                      <span className="bar-value">{formatBytes(wastedMemory)}</span>
                    </div>
                  </div>
                  <div className="comparison-efficiency" style={{ color: summary.memoryEfficiency >= 50 ? 'var(--efficiency-good)' : 'var(--efficiency-critical)' }}>
                    {summary.memoryEfficiency.toFixed(0)}%
                  </div>
                </div>
              </div>
            </div>
          </div>

          <div className="explainer-section">
            <h3>What Does Good Look Like?</h3>
            <div className="benchmark-scale">
              <div className="benchmark-item critical">
                <div className="benchmark-range">0-30%</div>
                <div className="benchmark-label">Critical</div>
                <div className="benchmark-desc">Massive waste - paying for 3x more than needed</div>
              </div>
              <div className="benchmark-item fair">
                <div className="benchmark-range">30-50%</div>
                <div className="benchmark-label">Fair</div>
                <div className="benchmark-desc">Underutilized - room for significant improvement</div>
              </div>
              <div className="benchmark-item good">
                <div className="benchmark-range">50-70%</div>
                <div className="benchmark-label">Good</div>
                <div className="benchmark-desc">Well optimized - some improvement possible</div>
              </div>
              <div className="benchmark-item excellent">
                <div className="benchmark-range">70-85%</div>
                <div className="benchmark-label">Excellent</div>
                <div className="benchmark-desc">Industry best practice - great balance</div>
              </div>
              <div className="benchmark-item risk">
                <div className="benchmark-range">85-100%+</div>
                <div className="benchmark-label">Overcommit</div>
                <div className="benchmark-desc">Requesting more than available - works but risky, no buffer for spikes</div>
              </div>
            </div>
          </div>

          {summary.overallEfficiency < 70 && (
            <div className="explainer-section recommendations">
              <h3>How to Improve</h3>
              <ul className="improvement-list">
                {summary.overallEfficiency < 50 && (
                  <>
                    <li>
                      <strong>Rightsize your pods:</strong> Review resource requests - they may be set too conservatively
                    </li>
                    <li>
                      <strong>Reduce node count:</strong> Consider consolidating workloads onto fewer nodes
                    </li>
                  </>
                )}
                <li>
                  <strong>Use Vertical Pod Autoscaler (VPA):</strong> Automatically adjust resource requests based on actual usage
                </li>
                <li>
                  <strong>Review idle namespaces:</strong> Look for dev/test environments that can be scaled down
                </li>
                <li>
                  <strong>Set appropriate resource requests:</strong> Ensure all pods have realistic requests defined
                </li>
              </ul>
              <div className="potential-savings">
                <div className="savings-label">Potential Monthly Savings</div>
                <div className="savings-value">{formatCost(summary.idleCost)}</div>
                <div className="savings-desc">By optimizing idle resources</div>
              </div>
            </div>
          )}
        </div>
      </div>
    </div>
  );
};

