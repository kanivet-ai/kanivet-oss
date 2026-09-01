import React from 'react';
import * as Collapsible from '@radix-ui/react-collapsible';
import { ChevronRightIcon } from '@radix-ui/react-icons';
import './DetailViewShared.css';

interface CollapsibleSectionProps {
  title: string;
  count?: number;
  defaultOpen?: boolean;
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  children: React.ReactNode;
  className?: string;
}

const CollapsibleSection: React.FC<CollapsibleSectionProps> = ({
  title,
  count,
  defaultOpen = true,
  open,
  onOpenChange,
  children,
  className = '',
}) => {
  const isControlled = open !== undefined;

  return (
    <Collapsible.Root
      className={`info-section collapsible-section ${className}`}
      defaultOpen={isControlled ? undefined : defaultOpen}
      open={isControlled ? open : undefined}
      onOpenChange={onOpenChange}
    >
      <Collapsible.Trigger asChild>
        <button className="collapsible-trigger">
          <span className="expand-icon">
            <ChevronRightIcon />
          </span>
          {title}
          {count !== undefined && <span className="section-count">{count}</span>}
        </button>
      </Collapsible.Trigger>
      <Collapsible.Content className="collapsible-content">
        <div className="info-section-content">{children}</div>
      </Collapsible.Content>
    </Collapsible.Root>
  );
};

export default CollapsibleSection;
