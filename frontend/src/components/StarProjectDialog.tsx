import * as Checkbox from '@radix-ui/react-checkbox';
import { CheckIcon } from '@radix-ui/react-icons';
import * as RDialog from '@radix-ui/react-dialog';
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
      <RDialog.Portal>
        <RDialog.Overlay className="ap-overlay ap-dialog-overlay">
          <RDialog.Content
            className="ap-sheet ap-dialog"
            style={{ maxWidth: 400 }}
          >
            <div
              style={{
                alignItems: 'center',
                display: 'flex',
                gap: 12,
                marginBottom: 12,
              }}
            >
              <KanivetMark size={32} tile />
              <RDialog.Title
                className="ap-dialog-title"
                style={{
                  fontWeight: 700,
                  letterSpacing: 'var(--letter-spacing-tight)',
                  marginBottom: 0,
                }}
              >
                Enjoying Kanivet?
              </RDialog.Title>
            </div>
            <RDialog.Description>
              If Kanivet helps you navigate Kubernetes, a GitHub star helps more
              people discover it.
            </RDialog.Description>
            <label
              style={{
                alignItems: 'center',
                cursor: 'pointer',
                display: 'flex',
                fontSize: 13,
                gap: 8,
                marginTop: 12,
              }}
            >
              <Checkbox.Root
                checked={dontShowAgain}
                onCheckedChange={handleDontShowAgainChange}
                aria-label="Don’t show this again"
                style={{
                  alignItems: 'center',
                  background: 'var(--hover)',
                  border: '1px solid var(--hair)',
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
            <div
              style={{
                alignItems: 'center',
                display: 'flex',
                flexWrap: 'wrap',
                gap: 8,
                justifyContent: 'space-between',
                marginTop: 16,
              }}
            >
              <button
                type="button"
                className="ap-btn"
                onClick={handleReportBug}
              >
                Report a bug
              </button>
              <div
                style={{
                  display: 'flex',
                  flexWrap: 'wrap',
                  gap: 8,
                  marginLeft: 'auto',
                }}
              >
                <button type="button" className="ap-btn" onClick={onClose}>
                  Maybe later
                </button>
                <button
                  type="button"
                  className="ap-btn ap-btn--primary"
                  onClick={handleStarProject}
                >
                  Star on GitHub
                </button>
              </div>
            </div>
          </RDialog.Content>
        </RDialog.Overlay>
      </RDialog.Portal>
    </RDialog.Root>
  );
};

export default StarProjectDialog;
