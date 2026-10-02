import { useEffect, useId } from 'react';
import { createPortal } from 'react-dom';
import { useStore } from '../store';
import ClusterMetricsSettings from './ClusterMetricsSettings';
import type { ConfigurableProvider } from './metrics/clusterMetricsForm';
import './MonitoringSettingsModal.css';

interface MonitoringSettingsModalProps {
  onClose: () => void;
  cluster?: string;
  /** Provider whose settings start open, e.g. Mimir when it needs a tenant. */
  initialExpanded?: ConfigurableProvider | null;
  /** Focus the Mimir tenant field once it can be edited. */
  focusTenant?: boolean;
}

const MonitoringSettingsModal = ({ onClose, cluster, initialExpanded, focusTenant }: MonitoringSettingsModalProps) => {
  const currentTab = useStore((s) => s.currentTab);
  const activeCluster = cluster || currentTab;
  const titleId = useId();

  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return;
      event.preventDefault();
      event.stopPropagation();
      onClose();
    };
    document.addEventListener('keydown', handleKeyDown, true);
    return () => document.removeEventListener('keydown', handleKeyDown, true);
  }, [onClose]);

  if (!activeCluster) return null;
  return createPortal(
    <div className="ap-overlay ms-overlay" onClick={onClose} role="presentation">
      <div className="ap-sheet ms-sheet" role="dialog" aria-modal="true" aria-labelledby={titleId} onClick={(e) => e.stopPropagation()}>
        <div className="ms-header">
          <h3 className="ap-sheet-title" id={titleId}>Metrics</h3>
          <button type="button" className="ap-icon-btn" onClick={onClose} aria-label="Close">×</button>
        </div>
        <ClusterMetricsSettings
          key={activeCluster}
          cluster={activeCluster}
          onSaved={onClose}
          onCancel={onClose}
          initialExpanded={initialExpanded}
          focusTenant={focusTenant}
        />
      </div>
    </div>,
    document.body,
  );
};

export default MonitoringSettingsModal;
