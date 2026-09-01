import { useEffect, useRef, useState } from 'react';

interface UseResizableOptions {
  minWidth: number;
  maxWidth: number;
  defaultWidth: number;
  onResize?: (width: number) => void;
}

const useResizable = (options: UseResizableOptions) => {
  const { minWidth, maxWidth, defaultWidth, onResize } = options;
  const [width, setWidth] = useState(defaultWidth);
  const [isResizing, setIsResizing] = useState(false);
  const startXRef = useRef(0);
  const startWidthRef = useRef(0);
  const rafRef = useRef<number | null>(null);
  const pendingWidthRef = useRef<number | null>(null);

  useEffect(() => {
    // Clamp width whenever constraints change
    setWidth((prev) => Math.max(minWidth, Math.min(prev, maxWidth)));
  }, [minWidth, maxWidth]);

  useEffect(() => {
    const handleMouseMove = (e: MouseEvent) => {
      if (!isResizing) return;

      const diff = e.clientX - startXRef.current;
      const newWidth = Math.min(
        maxWidth,
        Math.max(minWidth, startWidthRef.current - diff),
      );
      pendingWidthRef.current = newWidth;
      if (rafRef.current == null) {
        rafRef.current = requestAnimationFrame(() => {
          rafRef.current = null;
          const w = pendingWidthRef.current;
          if (w != null) {
            setWidth(w);
            onResize?.(w);
            pendingWidthRef.current = null;
          }
        });
      }
    };

    const handleMouseUp = () => {
      setIsResizing(false);
      if (rafRef.current != null) cancelAnimationFrame(rafRef.current);
      rafRef.current = null;
    };

    if (isResizing) {
      document.addEventListener('mousemove', handleMouseMove);
      document.addEventListener('mouseup', handleMouseUp);
      document.body.style.cursor = 'col-resize';
      document.body.style.userSelect = 'none';
    }

    return () => {
      document.removeEventListener('mousemove', handleMouseMove);
      document.removeEventListener('mouseup', handleMouseUp);
      document.body.style.cursor = '';
      document.body.style.userSelect = '';
      if (rafRef.current != null) cancelAnimationFrame(rafRef.current);
      rafRef.current = null;
    };
  }, [isResizing, minWidth, maxWidth, onResize]);

  const startResize = (e: React.MouseEvent) => {
    startXRef.current = e.clientX;
    startWidthRef.current = width;
    setIsResizing(true);
  };

  return {
    width,
    isResizing,
    startResize,
    setWidth,
  };
};

export default useResizable;
