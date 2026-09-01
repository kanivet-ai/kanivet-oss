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
      <p
        style={{
          marginTop: '10px',
          fontSize: '0.9em',
          color: 'var(--warning)',
        }}
      >
        Warning: This will allow resources to be deleted even if they have
        finalizers. Use with caution.
      </p>
    </Dialog>
  );
};

export default RemoveFinalizersDialog;
