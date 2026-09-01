import * as TooltipPrimitive from '@radix-ui/react-tooltip';
import React from 'react';
import './Tooltip.css';

interface TooltipProps {
  children: React.ReactNode;
  content: React.ReactNode;
  side?: 'top' | 'right' | 'bottom' | 'left';
  align?: 'start' | 'center' | 'end';
  delayDuration?: number;
  skipDelayDuration?: number;
}

/**
 * Tooltip component using Radix UI
 * Standard delay: 300ms (industry standard for responsive feel)
 * Skip delay: 100ms (faster on subsequent hovers)
 */
export const Tooltip: React.FC<TooltipProps> = ({
  children,
  content,
  side = 'bottom',
  align = 'center',
  delayDuration = 300,
  skipDelayDuration = 100,
}) => {
  if (!content) {
    return <>{children}</>;
  }

  return (
    <TooltipPrimitive.Provider
      delayDuration={delayDuration}
      skipDelayDuration={skipDelayDuration}
    >
      <TooltipPrimitive.Root>
        <TooltipPrimitive.Trigger asChild>{children}</TooltipPrimitive.Trigger>
        <TooltipPrimitive.Portal>
          <TooltipPrimitive.Content
            className="tooltip-content"
            side={side}
            align={align}
            sideOffset={5}
          >
            {content}
            <TooltipPrimitive.Arrow className="tooltip-arrow" />
          </TooltipPrimitive.Content>
        </TooltipPrimitive.Portal>
      </TooltipPrimitive.Root>
    </TooltipPrimitive.Provider>
  );
};

export default Tooltip;
