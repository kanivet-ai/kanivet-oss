import { ShortcutDefinition, CustomShortcuts } from '../types/shortcuts';

class ShortcutsRegistry {
  private shortcuts: Map<string, ShortcutDefinition> = new Map();
  private customBindings: CustomShortcuts = {};
  private listeners: Set<() => void> = new Set();
  private initialized = false;

  initialize() {
    if (this.initialized) return;
    this.loadCustomBindings();
    this.initialized = true;
  }

  register(shortcut: ShortcutDefinition) {
    this.shortcuts.set(shortcut.id, {
      ...shortcut,
      currentKeys: this.customBindings[shortcut.id] || shortcut.defaultKeys,
    });
    this.notifyListeners();
  }

  unregister(id: string) {
    this.shortcuts.delete(id);
    this.notifyListeners();
  }

  getShortcut(id: string): ShortcutDefinition | undefined {
    return this.shortcuts.get(id);
  }

  getAllShortcuts(): ShortcutDefinition[] {
    return Array.from(this.shortcuts.values());
  }

  getShortcutsByCategory(category: string): ShortcutDefinition[] {
    return Array.from(this.shortcuts.values()).filter(
      (s) => s.category === category,
    );
  }

  updateShortcut(id: string, newKeys: string) {
    const shortcut = this.shortcuts.get(id);
    if (!shortcut) return;

    this.customBindings[id] = newKeys;
    this.shortcuts.set(id, { ...shortcut, currentKeys: newKeys });
    this.saveCustomBindings();
    this.notifyListeners();
  }

  resetShortcut(id: string) {
    const shortcut = this.shortcuts.get(id);
    if (!shortcut) return;

    delete this.customBindings[id];
    this.shortcuts.set(id, { ...shortcut, currentKeys: shortcut.defaultKeys });
    this.saveCustomBindings();
    this.notifyListeners();
  }

  resetAllShortcuts() {
    this.customBindings = {};
    this.shortcuts.forEach((shortcut, id) => {
      this.shortcuts.set(id, { ...shortcut, currentKeys: shortcut.defaultKeys });
    });
    this.saveCustomBindings();
    this.notifyListeners();
  }

  getCurrentKeys(id: string): string {
    const shortcut = this.shortcuts.get(id);
    return shortcut?.currentKeys || shortcut?.defaultKeys || '';
  }

  findConflicts(keys: string, excludeId?: string): ShortcutDefinition[] {
    return Array.from(this.shortcuts.values()).filter(
      (s) => s.id !== excludeId && s.currentKeys === keys,
    );
  }

  subscribe(listener: () => void) {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  private notifyListeners() {
    this.listeners.forEach((listener) => listener());
  }

  private loadCustomBindings() {
    try {
      const stored = localStorage.getItem('kanivet.customShortcuts');
      if (stored) {
        this.customBindings = JSON.parse(stored);
      }
    } catch (e) {
      console.error('Failed to load custom shortcuts:', e);
    }
  }

  private saveCustomBindings() {
    try {
      localStorage.setItem(
        'kanivet.customShortcuts',
        JSON.stringify(this.customBindings),
      );
    } catch (e) {
      console.error('Failed to save custom shortcuts:', e);
    }
  }
}

export const shortcutsRegistry = new ShortcutsRegistry();
shortcutsRegistry.initialize();
