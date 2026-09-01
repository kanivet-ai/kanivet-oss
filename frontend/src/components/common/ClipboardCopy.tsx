import { useState } from 'react';
import { CheckIcon, ClipboardCopyIcon } from '@radix-ui/react-icons';

import './ClipboardCopy.css';

interface ClipboardCopyProps {
  text: string;
  alwaysShow?: boolean;
}

const ClipboardCopy = ({ text, alwaysShow }: ClipboardCopyProps) => {
  const [isCopied, setIsCopied] = useState(false);

  const handleCopy = () => {
    navigator.clipboard.writeText(text).then(() => {
      setIsCopied(true);
      setTimeout(() => setIsCopied(false), 2000);
    });
  };

  return (
    <button
      onClick={handleCopy}
      className={`clipboard-copy-btn${alwaysShow ? ' always-visible' : ''}`}
      title="Copy to clipboard"
    >
      {isCopied ? <CheckIcon className="check-icon" /> : <ClipboardCopyIcon />}
    </button>
  );
};

export default ClipboardCopy;
