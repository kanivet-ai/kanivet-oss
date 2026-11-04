import { useLayoutEffect, useState, type CSSProperties, type ReactNode, type RefObject } from 'react';
import { createPortal } from 'react-dom';
import './AnchoredPopover.css';

interface AnchoredPopoverProps {
  /** The element the popover hangs from (an input or a button). */
  anchor: RefObject<HTMLElement | null>;
  open: boolean;
  children: ReactNode;
  id?: string;
  role?: string;
  className?: string;
  /** Tallest the popover may get; it shrinks to the room left on screen. */
  maxHeight?: number;
  /** Narrowest the popover may get; it is at least as wide as the anchor. */
  minWidth?: number;
  'aria-label'?: string;
}

const GAP = 4;
const MARGIN = 8;

/**
 * A menu that renders in a portal on top of everything, placed under its
 * anchor (above when there is no room below). Cards with `overflow: hidden`
 * and scroll areas can no longer clip a suggestion list.
 */
export default function AnchoredPopover({
  anchor,
  open,
  children,
  id,
  role = 'listbox',
  className,
  maxHeight = 320,
  minWidth = 0,
  ...rest
}: AnchoredPopoverProps) {
  const [style, setStyle] = useState<CSSProperties | null>(null);

  useLayoutEffect(() => {
    if (!open) return;
    const place = () => {
      const el = anchor.current;
      if (!el) return;
      const r = el.getBoundingClientRect();
      const below = window.innerHeight - r.bottom - GAP - MARGIN;
      const above = r.top - GAP - MARGIN;
      const up = below < Math.min(maxHeight, 160) && above > below;
      const width = Math.max(r.width, minWidth);
      const left = Math.max(MARGIN, Math.min(r.left, window.innerWidth - width - MARGIN));
      setStyle({
        position: 'fixed',
        left,
        width,
        maxHeight: Math.max(80, Math.min(maxHeight, up ? above : below)),
        ...(up ? { bottom: window.innerHeight - r.top + GAP } : { top: r.bottom + GAP }),
      });
    };
    place();
    window.addEventListener('resize', place);
    // Capture: any scrolling ancestor moves the anchor.
    window.addEventListener('scroll', place, true);
    return () => {
      window.removeEventListener('resize', place);
      window.removeEventListener('scroll', place, true);
    };
  }, [open, anchor, maxHeight, minWidth]);

  if (!open || !style) return null;
  return createPortal(
    <div
      {...rest}
      id={id}
      role={role}
      className={`ap-menu ap-popover${className ? ` ${className}` : ''}`}
      style={style}
      // Clicking inside must not blur the input before the click lands.
      onMouseDown={(e) => e.preventDefault()}
    >
      {children}
    </div>,
    document.body,
  );
}
