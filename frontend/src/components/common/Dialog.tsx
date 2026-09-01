import React from 'react';
import { Dialog as RDialog, Flex, Button } from '@radix-ui/themes';

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
  const confirmColor = variant === 'danger' ? 'red' : variant === 'warning' ? 'amber' : undefined;

  return (
    <RDialog.Root open={isOpen} onOpenChange={(o) => !o && onClose()}>
      <RDialog.Content size="2" maxWidth="480px">
        <RDialog.Title size="4" weight="medium" mb="2" style={{ fontFamily: 'var(--font-display)', letterSpacing: '-0.01em' }}>
          {title}
        </RDialog.Title>
        <RDialog.Description size="2" color="gray" mb="4">
          <div>{children}</div>
        </RDialog.Description>
        <Flex gap="3" mt="4" justify="end">
          <Button variant="soft" color="gray" onClick={onClose} disabled={isLoading}>
            {cancelText}
          </Button>
          <Button color={confirmColor} onClick={onConfirm} disabled={isLoading || confirmDisabled}>
            {isLoading ? loadingText : confirmText}
          </Button>
        </Flex>
      </RDialog.Content>
    </RDialog.Root>
  );
};

export default Dialog;
