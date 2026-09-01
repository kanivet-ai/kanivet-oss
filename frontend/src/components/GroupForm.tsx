import React from 'react';
import { CheckIcon, CloseIcon } from './icons';

interface GroupFormProps {
  value: string;
  onChange: (value: string) => void;
  onSave: () => void;
  onCancel: () => void;
}

export const GroupForm: React.FC<GroupFormProps> = ({
  value,
  onChange,
  onSave,
  onCancel,
}) => {
  const handleKeyPress = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter') onSave();
    if (e.key === 'Escape') onCancel();
  };

  return (
    <div className="new-group-form">
      <input
        type="text"
        placeholder="Group name"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        onKeyPress={handleKeyPress}
        autoFocus
      />
      <div className="form-actions">
        <button onClick={onSave} className="save-btn" title="Save group">
          <CheckIcon />
        </button>
        <button onClick={onCancel} className="cancel-btn" title="Cancel">
          <CloseIcon />
        </button>
      </div>
    </div>
  );
};
