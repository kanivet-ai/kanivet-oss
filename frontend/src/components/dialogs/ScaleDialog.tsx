import React, { useState, useEffect } from 'react';
import Dialog from '../common/Dialog';

interface ScaleDialogProps {
  isOpen: boolean;
  item: any;
  currentReplicas: number;
  isScaling: boolean;
  onConfirm: (replicas: number) => void;
  onCancel: () => void;
}

const ScaleDialog: React.FC<ScaleDialogProps> = ({
  isOpen,
  item,
  currentReplicas,
  isScaling,
  onConfirm,
  onCancel,
}) => {
  const [replicas, setReplicas] = useState(currentReplicas);

  useEffect(() => {
    setReplicas(currentReplicas);
  }, [currentReplicas]);
  return (
    <Dialog
      isOpen={isOpen}
      title={`Scale ${item.name}`}
      onClose={onCancel}
      onConfirm={() => onConfirm(replicas)}
      confirmText={isScaling ? 'Scaling...' : 'Scale'}
      cancelText="Cancel"
      isLoading={isScaling}
      confirmDisabled={replicas === currentReplicas || replicas < 0}
    >
      <p>Current replicas: {currentReplicas}</p>
      <div className="form-group">
        <label htmlFor="replicas">New replicas:</label>
        <input
          id="replicas"
          type="number"
          value={replicas}
          onChange={(e) => setReplicas(parseInt(e.target.value) || 0)}
          min="0"
          max="100"
          autoFocus
          disabled={isScaling}
          className="input"
        />
      </div>
    </Dialog>
  );
};

export default ScaleDialog;
