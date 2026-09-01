import React, { useEffect, useState, useRef, useMemo } from 'react';
import { Cross2Icon, ResetIcon, Pencil1Icon, CheckIcon, MagnifyingGlassIcon } from '@radix-ui/react-icons';
import { shortcutsRegistry } from '../services/shortcutsRegistry';
import { ShortcutDefinition } from '../types/shortcuts';
import './KeyboardShortcutsEditor.css';

interface Props {
  isOpen: boolean;
  onClose: () => void;
}

const CATEGORY_LABELS: Record<string, string> = {
  global: 'Global',
  navigation: 'Navigation',
  tree: 'Tree',
  list: 'Resource List',
  details: 'Details',
  panes: 'Split Panes',
};

const isMac = navigator.platform.toUpperCase().indexOf('MAC') >= 0;
const isWindows = navigator.platform.toUpperCase().indexOf('WIN') >= 0;

const KeyboardShortcutsEditor: React.FC<Props> = ({ isOpen, onClose }) => {
  const [shortcuts, setShortcuts] = useState<ShortcutDefinition[]>([]);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [editValue, setEditValue] = useState('');
  const [conflict, setConflict] = useState<ShortcutDefinition[]>([]);
  const [searchQuery, setSearchQuery] = useState('');
  const inputRef = useRef<HTMLInputElement>(null);
  const searchInputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && isOpen && !editingId) {
        e.preventDefault();
        onClose();
      }
      if ((e.metaKey || e.ctrlKey) && e.key === 'f' && isOpen && !editingId) {
        e.preventDefault();
        searchInputRef.current?.focus();
      }
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [isOpen, onClose, editingId]);

  useEffect(() => {
    if (isOpen) {
      setShortcuts(shortcutsRegistry.getAllShortcuts());
      const unsubscribe = shortcutsRegistry.subscribe(() => {
        setShortcuts(shortcutsRegistry.getAllShortcuts());
      });
      return () => { unsubscribe(); };
    }
  }, [isOpen]);

  useEffect(() => {
    if (editingId && inputRef.current) {
      inputRef.current.focus();
    }
  }, [editingId]);

  const handleStartEdit = (shortcut: ShortcutDefinition) => {
    setEditingId(shortcut.id);
    setEditValue(shortcut.currentKeys || shortcut.defaultKeys);
    setConflict([]);
  };

  const handleKeyCapture = (e: React.KeyboardEvent) => {
    e.preventDefault();
    e.stopPropagation();

    if (e.key === 'Escape') {
      setEditingId(null);
      setEditValue('');
      setConflict([]);
      return;
    }

    if (e.key === 'Enter' && editingId) {
      handleSave();
      return;
    }

    if (e.key === 'Backspace' || e.key === 'Delete') {
      setEditValue('');
      return;
    }

    const parts = [];
    if (e.ctrlKey) parts.push('ctrl');
    if (e.altKey) parts.push('alt');
    if (e.metaKey) parts.push('cmd');
    if (e.shiftKey && e.key !== 'Shift') parts.push('shift');

    let key = e.key.toLowerCase();
    const keyMap: Record<string, string> = {
      ' ': 'space',
      arrowup: 'up',
      arrowdown: 'down',
      arrowleft: 'left',
      arrowright: 'right',
    };
    key = keyMap[key] || key;

    if (!['control', 'alt', 'meta', 'shift'].includes(key)) {
      parts.push(key);
      const newBinding = parts.join('+');
      setEditValue(newBinding);

      const conflicts = shortcutsRegistry.findConflicts(newBinding, editingId || undefined);
      setConflict(conflicts);
    }
  };

  const handleSave = () => {
    if (!editingId || !editValue) return;
    if (conflict.length > 0) {
      const confirmOverride = window.confirm(
        `This shortcut conflicts with:\n${conflict.map(c => `${c.description} (${c.context})`).join('\n')}\n\nDo you want to proceed anyway?`
      );
      if (!confirmOverride) return;
    }
    shortcutsRegistry.updateShortcut(editingId, editValue);
    setEditingId(null);
    setEditValue('');
    setConflict([]);
  };

  const handleCancel = () => {
    setEditingId(null);
    setEditValue('');
    setConflict([]);
  };

  const handleReset = (id: string) => {
    shortcutsRegistry.resetShortcut(id);
  };

  const handleResetAll = () => {
    if (window.confirm('Reset all shortcuts to defaults?')) {
      shortcutsRegistry.resetAllShortcuts();
    }
  };

  const filteredShortcuts = useMemo(() => {
    if (!searchQuery) return shortcuts;
    const query = searchQuery.toLowerCase();
    return shortcuts.filter(s => 
      s.description.toLowerCase().includes(query) ||
      s.currentKeys?.toLowerCase().includes(query) ||
      s.defaultKeys.toLowerCase().includes(query) ||
      CATEGORY_LABELS[s.category]?.toLowerCase().includes(query)
    );
  }, [shortcuts, searchQuery]);

  const shortcutsByCategory = filteredShortcuts.reduce((acc, shortcut) => {
    if (!acc[shortcut.category]) {
      acc[shortcut.category] = [];
    }
    acc[shortcut.category].push(shortcut);
    return acc;
  }, {} as Record<string, ShortcutDefinition[]>);

  const formatKeys = (keys: string) => {
    return keys.split('+').map(k => {
      const keyLabels: Record<string, string> = {
        cmd: isMac ? '⌘' : (isWindows ? 'Win' : 'Super'),
        meta: isMac ? '⌘' : (isWindows ? 'Win' : 'Super'),
        ctrl: isMac ? '⌃' : 'Ctrl',
        alt: isMac ? '⌥' : 'Alt',
        shift: isMac ? '⇧' : 'Shift',
        enter: isMac ? '⏎' : 'Enter',
        escape: 'Esc',
        space: 'Space',
        left: '←',
        right: '→',
        up: '↑',
        down: '↓',
        backspace: isMac ? '⌫' : 'Backspace',
        delete: isMac ? '⌦' : 'Del',
        tab: isMac ? '⇥' : 'Tab',
      };
      return keyLabels[k] || k.toUpperCase();
    });
  };

  if (!isOpen) return null;

  return (
    <div className="kse-overlay" onClick={handleCancel}>
      <div className="kse-modal" onClick={(e) => e.stopPropagation()}>
        <div className="kse-header">
          <div className="kse-title">Keyboard Shortcuts</div>
          <div className="kse-header-actions">
            <button className="kse-reset-all" onClick={handleResetAll} title="Reset all to defaults">
              <ResetIcon width={14} height={14} />
              Reset All
            </button>
            <button className="kse-close" onClick={onClose} aria-label="Close" title="Close">
              <Cross2Icon width={14} height={14} />
            </button>
          </div>
        </div>
        <div className="kse-search-container">
          <MagnifyingGlassIcon className="kse-search-icon" width={14} height={14} />
          <input
            ref={searchInputRef}
            type="text"
            className="kse-search-input"
            placeholder={`Search shortcuts... (${isMac ? '⌘' : 'Ctrl'}+F)`}
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
          />
          {searchQuery && (
            <button 
              className="kse-search-clear" 
              onClick={() => setSearchQuery('')}
              aria-label="Clear search"
            >
              <Cross2Icon width={12} height={12} />
            </button>
          )}
        </div>
        <div className="kse-body">
          {Object.entries(shortcutsByCategory).map(([category, categoryShortcuts]) => (
            <div key={category} className="kse-section">
              <div className="kse-section-title">{CATEGORY_LABELS[category] || category}</div>
              {categoryShortcuts.map((shortcut) => (
                <div key={shortcut.id} className="kse-row">
                  <div className="kse-description">{shortcut.description}</div>
                  <div className="kse-binding">
                    {editingId === shortcut.id ? (
                      <div className="kse-edit-container">
                        <input
                          ref={inputRef}
                          type="text"
                          className={`kse-edit-input ${conflict.length > 0 ? 'kse-conflict' : ''}`}
                          value={editValue.split('+').map(k => formatKeys(k)[0]).join(' + ')}
                          onKeyDown={handleKeyCapture}
                          readOnly
                          placeholder="Press keys..."
                        />
                        {conflict.length > 0 && (
                          <div className="kse-conflict-hint">Conflicts detected!</div>
                        )}
                        <div className="kse-edit-actions">
                          <button className="kse-save-btn" onClick={handleSave} title="Save (Enter)">
                            <CheckIcon width={12} height={12} />
                          </button>
                          <button className="kse-cancel-btn" onClick={handleCancel} title="Cancel (Esc)">
                            <Cross2Icon width={12} height={12} />
                          </button>
                        </div>
                      </div>
                    ) : (
                      <>
                        <div className="kse-keys">
                          {formatKeys(shortcut.currentKeys || shortcut.defaultKeys).map((key, i) => (
                            <span key={i} className="kse-key">{key}</span>
                          ))}
                        </div>
                        <div className="kse-actions">
                          <button
                            className="kse-edit-btn"
                            onClick={() => handleStartEdit(shortcut)}
                            title="Edit shortcut"
                          >
                            <Pencil1Icon width={12} height={12} />
                          </button>
                          {shortcut.currentKeys !== shortcut.defaultKeys && (
                            <button
                              className="kse-reset-btn"
                              onClick={() => handleReset(shortcut.id)}
                              title="Reset to default"
                            >
                              <ResetIcon width={12} height={12} />
                            </button>
                          )}
                        </div>
                      </>
                    )}
                  </div>
                </div>
              ))}
            </div>
          ))}
          {Object.keys(shortcutsByCategory).length === 0 && (
            <div className="kse-no-results">
              No shortcuts found matching "{searchQuery}"
            </div>
          )}
        </div>
      </div>
    </div>
  );
};

export default KeyboardShortcutsEditor;
