import { useEffect, useCallback, useRef } from 'react';

type KeyMap = Record<string, (e: KeyboardEvent) => void>;

const useKeyboard = (keyMap: KeyMap) => {
  const lastKeyRef = useRef<string | null>(null);
  const lastTimeRef = useRef<number>(0);

  // Expand meta shortcuts to both cmd and ctrl and handle modifier order variations
  const expandedKeyMap = useRef<KeyMap>({});

  useEffect(() => {
    const expanded: KeyMap = {};

    // Helper to generate all modifier order permutations
    const generateModifierPermutations = (keyStr: string): string[] => {
      const parts = keyStr.split('+');
      const mainKey = parts[parts.length - 1]; // The actual key (e.g., 'p', 'k')
      const modifiers = parts.slice(0, -1); // All modifiers

      // If there's only one modifier or no modifiers, return as is
      if (modifiers.length <= 1) return [keyStr];

      // For shift combinations, create both orders
      if (modifiers.includes('shift') && modifiers.length === 2) {
        const otherMod = modifiers.find((m) => m !== 'shift');
        return [`${otherMod}+shift+${mainKey}`, `shift+${otherMod}+${mainKey}`];
      }

      return [keyStr];
    };

    Object.entries(keyMap).forEach(([key, handler]) => {
      if (key.includes('meta+')) {
        // Add both cmd and ctrl versions
        const cmdKey = key.replace('meta+', 'cmd+');
        const ctrlKey = key.replace('meta+', 'ctrl+');

        // Generate all permutations for each version
        generateModifierPermutations(cmdKey).forEach(
          (k) => (expanded[k] = handler),
        );
        generateModifierPermutations(ctrlKey).forEach(
          (k) => (expanded[k] = handler),
        );
      } else {
        // Generate permutations for non-meta keys too
        generateModifierPermutations(key).forEach(
          (k) => (expanded[k] = handler),
        );
      }
    });
    expandedKeyMap.current = expanded;
  }, [keyMap]);

  const handleKeyDown = useCallback((e: KeyboardEvent) => {
    const target = e.target as HTMLElement;
    const isInputField =
      target.tagName === 'INPUT' ||
      target.tagName === 'TEXTAREA' ||
      target.contentEditable === 'true';

    const key = getKeyString(e);

    // Allow global shortcuts even in input fields
    const globalShortcuts = [
      'cmd+k',
      'ctrl+k',
      'cmd+l',
      'ctrl+l',
      'cmd+shift+p',
      'ctrl+shift+p',
      'cmd+t',
      'ctrl+t',
      'escape',
      'alt+left',
      'alt+right',
      'cmd+[',
      'ctrl+[',
      'cmd+]',
      'ctrl+]',
      'ctrl+o',
      'ctrl+i',
    ];
    const isGlobalShortcut = globalShortcuts.includes(key);

    if (isInputField && !isGlobalShortcut) return;

    // First: support simple two-key sequences like 's+c' (no modifiers)
    const hasModifier = e.ctrlKey || e.altKey || e.metaKey || e.shiftKey;
    const now = Date.now();
    if (!hasModifier) {
      const last = lastKeyRef.current;
      const withinWindow = now - lastTimeRef.current < 700;
      if (last && withinWindow) {
        const seq = `${last}+${key}`;
        const seqHandler = (keyMap as any)[seq];
        if (seqHandler) {
          e.preventDefault();
          seqHandler(e);
          lastKeyRef.current = null;
          lastTimeRef.current = 0;
          return;
        }
      }
      // Update last key for potential next sequence (letters/numbers only)
      if (/^[a-z0-9]$/.test(key)) {
        lastKeyRef.current = key;
        lastTimeRef.current = now;
      } else {
        lastKeyRef.current = null;
        lastTimeRef.current = 0;
      }
    }

    // Then: normal single-key with modifiers
    const handler = expandedKeyMap.current[key];
    if (handler) {
      e.preventDefault();
      handler(e);
    }
  }, []);

  useEffect(() => {
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [handleKeyDown]);
};

const getKeyString = (e: KeyboardEvent): string => {
  const parts = [];
  // Order matters: ctrl, alt, cmd/meta, shift
  if (e.ctrlKey) parts.push('ctrl');
  if (e.altKey) parts.push('alt');
  if (e.metaKey) parts.push('cmd');
  if (e.shiftKey) parts.push('shift');

  let key = e.key.toLowerCase();
  const keyMap: Record<string, string> = {
    ' ': 'space',
    arrowup: 'up',
    arrowdown: 'down',
    arrowleft: 'left',
    arrowright: 'right',
  };

  key = keyMap[key] || key;

  // Handle special characters that might be uppercase when shift is pressed
  if (e.shiftKey && key.length === 1) {
    // For single characters, use the lowercase version even with shift
    key = key.toLowerCase();
  }

  parts.push(key);
  return parts.join('+');
};

export default useKeyboard;
