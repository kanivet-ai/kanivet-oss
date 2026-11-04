import React, { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { DotsHorizontalIcon } from '@radix-ui/react-icons';

/**
 * The inspector header's "More" menu: secondary actions (Compare, Delete, …)
 * that would otherwise crowd the primary ones. Children stay mounted while
 * the menu is closed, so their dialogs and popovers survive it closing.
 * The button hides itself when every child renders nothing.
 */
const HeaderOverflow: React.FC<{ children: React.ReactNode }> = ({ children }) => {
  const [open, setOpen] = useState(false);
  const [empty, setEmpty] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);

  useLayoutEffect(() => {
    setEmpty((menuRef.current?.childElementCount ?? 0) === 0);
  });

  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') setOpen(false); };
    document.addEventListener('mousedown', onDown);
    document.addEventListener('keydown', onKey);
    return () => { document.removeEventListener('mousedown', onDown); document.removeEventListener('keydown', onKey); };
  }, [open]);

  return (
    <div className="header-overflow" ref={rootRef} style={empty ? { display: 'none' } : undefined}>
      <button
        type="button"
        className="quick-action-btn header-overflow-btn"
        onClick={() => setOpen((o) => !o)}
        title="More actions"
        aria-label="More actions"
        aria-haspopup="menu"
        aria-expanded={open}
      >
        <span className="ap-action-icon"><DotsHorizontalIcon /></span>
        <span className="ap-action-label">More</span>
      </button>
      <div
        ref={menuRef}
        className="header-overflow-menu"
        role="menu"
        hidden={!open}
        onClick={(e) => { if ((e.target as HTMLElement).closest('button')) setOpen(false); }}
      >
        {children}
      </div>
    </div>
  );
};

export default HeaderOverflow;
