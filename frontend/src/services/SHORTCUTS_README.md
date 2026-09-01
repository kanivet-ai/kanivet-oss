# Keyboard Shortcuts System

## Overview

The keyboard shortcuts system provides a centralized, customizable, and persistent way to manage all keyboard shortcuts in the application.

## Architecture

### Components

1. **ShortcutsRegistry** (`shortcutsRegistry.ts`)
   - Central registry for all shortcuts
   - Handles custom bindings
   - Persists to localStorage
   - Provides conflict detection
   - Notifies listeners of changes

2. **useRegisteredKeyboard Hook** (`hooks/useRegisteredKeyboard.tsx`)
   - Registers shortcuts with the registry
   - Subscribes to registry changes
   - Automatically unregisters on unmount
   - Maps current keys to handlers

3. **KeyboardShortcutsEditor** (`KeyboardShortcutsEditor.tsx`)
   - UI for viewing all shortcuts
   - Search bar to filter shortcuts (Cmd/Ctrl+F)
   - Platform-specific key symbols (⌘ on Mac, Ctrl/Win on Windows/Linux)
   - Edit mode for customizing shortcuts
   - Conflict detection and warnings
   - Reset individual or all shortcuts

## Usage

### Registering Shortcuts

```typescript
import { useRegisteredKeyboard } from '../hooks/useRegisteredKeyboard';

function MyComponent() {
  useRegisteredKeyboard({
    'meta+k': {
      category: 'global',
      description: 'Open command palette',
      handler: (e) => {
        e.preventDefault();
        openCommandPalette();
      },
    },
    'ctrl+d': {
      category: 'navigation',
      description: 'Jump down 10 items',
      handler: (e) => {
        e.preventDefault();
        jumpDown();
      },
    },
  }, 'componentId');

  return <div>...</div>;
}
```

### Categories

- `global`: Application-wide shortcuts
- `navigation`: Navigation and history
- `tree`: Tree sidebar shortcuts
- `list`: Resource list shortcuts
- `details`: Details panel shortcuts
- `panes`: Split pane shortcuts

### Shortcut Format

Keys use the format: `modifier+modifier+key`

**Modifiers:**
- `meta`: Command (Mac) or Ctrl (Windows/Linux) - auto-expands to both
- `cmd`: Command key specifically
- `ctrl`: Control key
- `alt`: Alt/Option key
- `shift`: Shift key

**Examples:**
- `meta+k` - Expands to both Cmd+K and Ctrl+K
- `meta+shift+p` - Command/Ctrl + Shift + P
- `j` - Just the J key
- `s+c` - Sequence: S then C (within 700ms)

## Persistence

Custom shortcuts are saved to localStorage under the key `kanivet.customShortcuts`.

## Conflict Detection

The system automatically detects when a new shortcut binding conflicts with existing shortcuts and prompts the user before overriding.

## Reset Functionality

- Individual shortcuts can be reset to their default
- All shortcuts can be reset via "Reset All" button
