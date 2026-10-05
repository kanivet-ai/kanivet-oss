import React from 'react';
import * as RDialog from '@radix-ui/react-dialog';

interface DialogProps {
  isOpen: boolean;
  onClose: () => void;
  onConfirm: () => void;
  title: string;
  children: React.ReactNode;
  confirmText?: string;
  cancelText?: string;
  isLoading?: boolean;
  loadingText?: string;
  confirmDisabled?: boolean;
  variant?: 'default' | 'danger' | 'warning';
}

const Dialog: React.FC<DialogProps> = ({
  isOpen,
  onClose,
  onConfirm,
  title,
  children,
  confirmText = 'Confirm',
  cancelText = 'Cancel',
  isLoading = false,
  loadingText = 'Loading...',
  confirmDisabled = false,
  variant = 'default',
}) => {
  const confirmClass =
    variant === 'danger'
      ? 'ap-btn--danger'
      : variant === 'warning'
        ? 'ap-btn--warning'
        : 'ap-btn--primary';

  return (
    <RDialog.Root open={isOpen} onOpenChange={(o) => !o && onClose()}>
      <RDialog.Portal>
        <RDialog.Overlay className="ap-overlay ap-dialog-overlay">
          <RDialog.Content
            className="ap-sheet ap-dialog"
            style={{ maxWidth: 480 }}
          >
            <RDialog.Title className="ap-dialog-title">{title}</RDialog.Title>
            <RDialog.Description asChild>
              <div className="ap-dialog-description">{children}</div>
            </RDialog.Description>
            <div className="ap-dialog-actions">
              <button
                type="button"
                className="ap-btn"
                onClick={onClose}
                disabled={isLoading}
              >
                {cancelText}
              </button>
              <button
                type="button"
                className={`ap-btn ${confirmClass}`}
                onClick={onConfirm}
                disabled={isLoading || confirmDisabled}
              >
                {isLoading ? loadingText : confirmText}
              </button>
            </div>
          </RDialog.Content>
        </RDialog.Overlay>
      </RDialog.Portal>
    </RDialog.Root>
  );
};

export default Dialog;
