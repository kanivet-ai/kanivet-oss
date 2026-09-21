import Dialog from './common/Dialog';

const GITHUB_REPOSITORY_URL = 'https://github.com/kanivet-ai/kanivet-oss';

interface StarProjectDialogProps {
  isOpen: boolean;
  onClose: () => void;
}

const StarProjectDialog = ({ isOpen, onClose }: StarProjectDialogProps) => {
  const handleStarProject = () => {
    window.open(GITHUB_REPOSITORY_URL, '_blank', 'noopener,noreferrer');
    onClose();
  };

  return (
    <Dialog
      isOpen={isOpen}
      onClose={onClose}
      onConfirm={handleStarProject}
      title="Enjoying Kanivet?"
      confirmText="Star on GitHub"
      cancelText="Maybe later"
    >
      <div style={{ display: 'grid', gap: 8 }}>
        <p style={{ margin: 0 }}>
          If Kanivet helps you navigate and troubleshoot Kubernetes, please
          consider starring the project on GitHub.
        </p>
        <p style={{ margin: 0 }}>
          It helps others discover the project and supports the community.
        </p>
      </div>
    </Dialog>
  );
};

export default StarProjectDialog;
