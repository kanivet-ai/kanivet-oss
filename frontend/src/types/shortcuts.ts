export interface ShortcutDefinition {
  id: string;
  category: 'global' | 'navigation' | 'tree' | 'list' | 'details' | 'panes';
  description: string;
  defaultKeys: string;
  currentKeys?: string;
  handler?: (e: KeyboardEvent) => void;
  context?: string;
}

export interface ShortcutCategory {
  id: string;
  label: string;
  shortcuts: ShortcutDefinition[];
}

export interface CustomShortcuts {
  [shortcutId: string]: string;
}
