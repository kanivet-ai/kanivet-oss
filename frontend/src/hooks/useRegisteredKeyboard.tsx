import { useEffect, useState, useMemo } from 'react';
import useKeyboard from './useKeyboard';
import { shortcutsRegistry } from '../services/shortcutsRegistry';
import { ShortcutDefinition } from '../types/shortcuts';

type ShortcutMap = Record<string, {
  category: ShortcutDefinition['category'];
  description: string;
  handler: (e: KeyboardEvent) => void;
  context?: string;
}>;

export const useRegisteredKeyboard = (shortcuts: ShortcutMap, componentId: string) => {
  const [, setUpdateTrigger] = useState(0);

  useEffect(() => {
    Object.entries(shortcuts).forEach(([keys, config]) => {
      const id = `${componentId}.${keys}`;
      shortcutsRegistry.register({
        id,
        category: config.category,
        description: config.description,
        defaultKeys: keys,
        handler: config.handler,
        context: config.context || componentId,
      });
    });

    const unsubscribe = shortcutsRegistry.subscribe(() => {
      setUpdateTrigger(prev => prev + 1);
    });

    return () => {
      Object.keys(shortcuts).forEach((keys) => {
        const id = `${componentId}.${keys}`;
        shortcutsRegistry.unregister(id);
      });
      unsubscribe();
    };
  }, [componentId]);

  const keyMap = useMemo(() => {
    const map: Record<string, (e: KeyboardEvent) => void> = {};
    Object.entries(shortcuts).forEach(([defaultKeys, config]) => {
      const id = `${componentId}.${defaultKeys}`;
      const currentKeys = shortcutsRegistry.getCurrentKeys(id);
      map[currentKeys] = config.handler;
    });
    return map;
  }, [shortcuts, componentId]);

  useKeyboard(keyMap);
};

export default useRegisteredKeyboard;
