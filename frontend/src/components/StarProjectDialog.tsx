import * as Checkbox from '@radix-ui/react-checkbox';
import { CheckIcon } from '@radix-ui/react-icons';
import { Button, Dialog as RDialog, Flex } from '@radix-ui/themes';
import { useState } from 'react';
import KanivetMark from './icons/KanivetMark';
import {
  isStarProjectPromptDismissed,
  setStarProjectPromptDismissed,
} from '../utils/starProjectPromptPreference';

const GITHUB_REPOSITORY_URL = 'https://github.com/kanivet-ai/kanivet-oss';

interface StarProjectDialogProps {
  isOpen: boolean;
  onClose: () => void;
}

const StarProjectDialog = ({ isOpen, onClose }: StarProjectDialogProps) => {
  const [dontShowAgain, setDontShowAgain] = useState(
    isStarProjectPromptDismissed,
  );

  const handleDontShowAgainChange = (checked: boolean) => {
    setDontShowAgain(checked);
    setStarProjectPromptDismissed(checked);
  };

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
        <Flex asChild align="center" gap="2" mt="3">
          <label style={{ cursor: 'pointer', fontSize: 13 }}>
            <Checkbox.Root
              checked={dontShowAgain}
              onCheckedChange={handleDontShowAgainChange}
              aria-label="Don’t show this again"
              style={{
                alignItems: 'center',
                background: 'var(--gray-a3)',
                border: '1px solid var(--gray-a6)',
                borderRadius: 4,
                display: 'inline-flex',
                height: 16,
                justifyContent: 'center',
                width: 16,
              }}
            >
              <Checkbox.Indicator>
                <CheckIcon />
              </Checkbox.Indicator>
            </Checkbox.Root>
            Don’t show this again
          </label>
        </Flex>
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
