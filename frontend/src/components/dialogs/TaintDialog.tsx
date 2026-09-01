import { useState } from 'react';
import Dialog from '../common/Dialog';

interface TaintDialogProps {
  item: any;
  onConfirm: (
    key: string,
    value: string,
    effect: 'NoSchedule' | 'PreferNoSchedule' | 'NoExecute',
  ) => void;
  onClose: () => void;
  isLoading: boolean;
}

const TaintDialog = ({
  item,
  onConfirm,
  onClose,
  isLoading,
}: TaintDialogProps) => {
  const [taintKey, setTaintKey] = useState('');
  const [taintValue, setTaintValue] = useState('');
  const [taintEffect, setTaintEffect] = useState<
    'NoSchedule' | 'PreferNoSchedule' | 'NoExecute'
  >('NoSchedule');

  return (
    <Dialog
      isOpen={true}
      title={`Taint Node: ${item.name}`}
      onClose={onClose}
      onConfirm={() => onConfirm(taintKey, taintValue, taintEffect)}
      confirmText={isLoading ? 'Applying...' : 'Apply Taint'}
      cancelText="Cancel"
      isLoading={isLoading}
      confirmDisabled={!taintKey}
      variant="warning"
    >
      <div className="dialog-options">
        <div className="form-group">
          <label htmlFor="taint-key">Key:</label>
          <input
            id="taint-key"
            type="text"
            value={taintKey}
            onChange={(e) => setTaintKey(e.target.value)}
            placeholder="e.g., dedicated"
            autoFocus
            className="input"
          />
        </div>
        <div className="form-group">
          <label htmlFor="taint-value">Value (optional):</label>
          <input
            id="taint-value"
            type="text"
            value={taintValue}
            onChange={(e) => setTaintValue(e.target.value)}
            placeholder="e.g., gpu"
            className="input"
          />
        </div>
        <div className="form-group">
          <label htmlFor="taint-effect">Effect:</label>
          <select
            id="taint-effect"
            value={taintEffect}
            onChange={(e) => setTaintEffect(e.target.value as any)}
          >
            <option value="NoSchedule">NoSchedule</option>
            <option value="PreferNoSchedule">PreferNoSchedule</option>
            <option value="NoExecute">NoExecute</option>
          </select>
        </div>
      </div>
    </Dialog>
  );
};

export default TaintDialog;
