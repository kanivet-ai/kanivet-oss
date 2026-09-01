import { useEffect, useRef, useState } from 'react';

interface UseResizableHeightOptions {
  minHeight: number;
  maxHeight: number;
  defaultHeight: number;
  onResize?: (height: number) => void;
}

const useResizableHeight = (options: UseResizableHeightOptions) => {
  const { minHeight, maxHeight, defaultHeight, onResize } = options;
  const [height, setHeight] = useState(defaultHeight);
  const [isResizing, setIsResizing] = useState(false);
  const startYRef = useRef(0);
  const startHeightRef = useRef(0);

  useEffect(() => {
    // Clamp height whenever constraints change
    setHeight((prev) => Math.max(minHeight, Math.min(prev, maxHeight)));
  }, [minHeight, maxHeight]);

  useEffect(() => {
    const handleMouseMove = (e: MouseEvent) => {
      if (!isResizing) return;

      // For bottom dock, dragging up increases height
      const diff = startYRef.current - e.clientY;
      const newHeight = Math.min(
        maxHeight,
        Math.max(minHeight, startHeightRef.current + diff),
      );
      setHeight(newHeight);
      onResize?.(newHeight);
    };

    const handleMouseUp = () => {
      setIsResizing(false);
      // Save height to localStorage
      try {
        localStorage.setItem('kanivet.bottomDockHeight', String(height));
      } catch {}
    };

    if (isResizing) {
      document.addEventListener('mousemove', handleMouseMove);
      document.addEventListener('mouseup', handleMouseUp);
      document.body.classList.add('resizing-height');
    }

    return () => {
      document.removeEventListener('mousemove', handleMouseMove);
      document.removeEventListener('mouseup', handleMouseUp);
      document.body.classList.remove('resizing-height');
    };
  }, [isResizing, minHeight, maxHeight, height, onResize]);

  const startResize = (e: React.MouseEvent) => {
    e.preventDefault();
    startYRef.current = e.clientY;
    startHeightRef.current = height;
    setIsResizing(true);
  };

  // Load saved height on mount
  useEffect(() => {
    try {
      const saved = localStorage.getItem('kanivet.bottomDockHeight');
      if (saved) {
        const savedHeight = parseInt(saved, 10);
        if (!isNaN(savedHeight)) {
          setHeight(Math.max(minHeight, Math.min(savedHeight, maxHeight)));
        }
      }
    } catch {}
  }, [minHeight, maxHeight]);

  return {
    height,
    isResizing,
    startResize,
    setHeight,
  };
};

export default useResizableHeight;
