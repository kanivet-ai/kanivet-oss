import Dialog from '../common/Dialog';

interface DeleteConfirmationDialogProps {
  isOpen: boolean;
  resourceCount: number;
  isDeleting: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}

const DeleteConfirmationDialog = ({
  isOpen,
  resourceCount,
  isDeleting,
  onConfirm,
  onCancel,
}: DeleteConfirmationDialogProps) => {
  return (
    <Dialog
      isOpen={isOpen}
      onClose={onCancel}
      onConfirm={onConfirm}
      title="Confirm Delete"
      confirmText="Delete"
      isLoading={isDeleting}
      loadingText="Deleting..."
      variant="danger"
    >
      <p>
        Are you sure you want to delete {resourceCount} resource
        {resourceCount !== 1 ? 's' : ''}?
      </p>
    </Dialog>
  );
};

export default DeleteConfirmationDialog;
