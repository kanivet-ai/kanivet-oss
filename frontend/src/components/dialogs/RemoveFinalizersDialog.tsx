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
      {/* Same colour the sheet's warning confirm button uses (common/Dialog variant="warning") */}
      <p
        style={{
          color: 'var(--warning)',
          fontSize: 12,
          fontWeight: 500,
          lineHeight: '16px',
          marginTop: 8,
        }}
      >
        Warning: This will allow resources to be deleted even if they have
        finalizers. Use with caution.
      </p>
    </Dialog>
  );
};

export default RemoveFinalizersDialog;
