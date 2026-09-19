import { Text } from '@radix-ui/themes';
import Dialog from '../common/Dialog';

interface RemoveFinalizersDialogProps {
  isOpen: boolean;
  resourceCount: number;
  isRemoving: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}

const RemoveFinalizersDialog = ({
  isOpen,
  resourceCount,
  isRemoving,
  onConfirm,
  onCancel,
}: RemoveFinalizersDialogProps) => {
  return (
    <Dialog
      isOpen={isOpen}
      onClose={onCancel}
      onConfirm={onConfirm}
      title="Confirm Remove Finalizers"
      confirmText="Remove Finalizers"
      isLoading={isRemoving}
      loadingText="Removing finalizers..."
      variant="warning"
    >
      <p>
        Are you sure you want to remove finalizers from {resourceCount} resource
        {resourceCount !== 1 ? 's' : ''}?
      </p>
      {/* Same amber the sheet's warning confirm button uses (common/Dialog variant="warning") */}
      <Text as="p" size="1" weight="medium" color="amber" mt="2">
        Warning: This will allow resources to be deleted even if they have
        finalizers. Use with caution.
      </Text>
    </Dialog>
  );
};

export default RemoveFinalizersDialog;
