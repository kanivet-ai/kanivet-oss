import React from 'react';
import Dialog from '../common/Dialog';

interface DrainDialogProps {
  isOpen: boolean;
  item: any;
  drainOptions: {
    ignoreDaemonsets: boolean;
    deleteEmptyDirData: boolean;
    force: boolean;
    gracePeriod: number;
  };
  isDraining: boolean;
  onOptionChange: (key: string, value: boolean | number) => void;
  onConfirm: () => void;
  onCancel: () => void;
}

const DrainDialog: React.FC<DrainDialogProps> = ({
  isOpen,
  item,
  drainOptions,
  isDraining,
  onOptionChange,
  onConfirm,
  onCancel,
}) => {
  return (
    <Dialog
      isOpen={isOpen}
      title={`Drain Node: ${item.name}`}
      onClose={onCancel}
      onConfirm={onConfirm}
      confirmText={isDraining ? 'Draining...' : 'Drain Node'}
      cancelText="Cancel"
      isLoading={isDraining}
      variant="danger"
    >
      <p className="warning-message">
        This will evict all pods from the node and mark it as unschedulable.
      </p>
      <div className="drain-options">
        <label className="checkbox-label">
          <input
            type="checkbox"
            checked={drainOptions.ignoreDaemonsets}
            onChange={(e) =>
              onOptionChange('ignoreDaemonsets', e.target.checked)
            }
            disabled={isDraining}
          />
          Ignore DaemonSets
        </label>
        <label className="checkbox-label">
          <input
            type="checkbox"
            checked={drainOptions.deleteEmptyDirData}
            onChange={(e) =>
              onOptionChange('deleteEmptyDirData', e.target.checked)
            }
            disabled={isDraining}
          />
          Delete pods with emptyDir volumes
        </label>
        <label className="checkbox-label">
          <input
            type="checkbox"
            checked={drainOptions.force}
            onChange={(e) => onOptionChange('force', e.target.checked)}
            disabled={isDraining}
          />
          Force deletion of pods
        </label>
        <div className="form-group">
          <label htmlFor="grace-period">Grace period (seconds):</label>
          <input
            id="grace-period"
            type="number"
            value={drainOptions.gracePeriod}
            onChange={(e) =>
              onOptionChange('gracePeriod', parseInt(e.target.value) || 0)
            }
            min="0"
            max="3600"
            disabled={isDraining}
            className="input"
          />
        </div>
      </div>
    </Dialog>
  );
};

export default DrainDialog;
