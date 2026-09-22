import { Button, Dialog as RDialog, Flex } from '@radix-ui/themes';
import KanivetMark from './icons/KanivetMark';

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

  const handleReportBug = () => {
    window.open(
      'https://github.com/kanivet-ai/kanivet-oss/issues/new?template=bug_report.md',
      '_blank',
      'noopener,noreferrer',
    );
  };

  return (
    <RDialog.Root open={isOpen} onOpenChange={(open) => !open && onClose()}>
      <RDialog.Content
        size="2"
        maxWidth="400px"
        style={{
          width: 'calc(100vw - 32px)',
          fontFamily: 'var(--font-sans)',
        }}
      >
        <Flex align="center" gap="3" mb="3">
          <KanivetMark size={32} tile />
          <RDialog.Title
            size="4"
            style={{
              fontFamily: 'var(--font-sans)',
              letterSpacing: 'var(--letter-spacing-tight)',
            }}
          >
            Enjoying Kanivet?
          </RDialog.Title>
        </Flex>
        <RDialog.Description>
          If Kanivet helps you navigate Kubernetes, a GitHub star helps more
          people discover it.
        </RDialog.Description>
        <Flex
          gap="2"
          mt="4"
          align="center"
          justify="between"
          wrap="wrap"
          style={{ rowGap: 8 }}
        >
          <Button variant="soft" color="gray" onClick={handleReportBug}>
            Report a bug
          </Button>
          <Flex gap="2" wrap="wrap" style={{ marginLeft: 'auto' }}>
            <Button variant="soft" color="gray" onClick={onClose}>
              Maybe later
            </Button>
            <Button onClick={handleStarProject}>Star on GitHub</Button>
          </Flex>
        </Flex>
      </RDialog.Content>
    </RDialog.Root>
  );
};

export default StarProjectDialog;
