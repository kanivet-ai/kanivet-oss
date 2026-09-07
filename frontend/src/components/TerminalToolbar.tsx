import { useState, useRef, useEffect } from 'react';
import {
  PlusIcon,
  Cross2Icon,
  ChevronDownIcon,
  Pencil1Icon,
  MagnifyingGlassIcon,
  ViewHorizontalIcon,
  ViewVerticalIcon,
} from '@radix-ui/react-icons';
import * as DropdownMenu from '@radix-ui/react-dropdown-menu';
import { useStore, BottomTab } from '../store';
import { useShallow } from 'zustand/react/shallow';
import './TerminalToolbar.css';

interface TerminalToolbarProps {
  currentTabId: string;
  onSearchToggle?: () => void;
  isSearchOpen?: boolean;
  onSplitHorizontal?: () => void;
  onSplitVertical?: () => void;
}

const TerminalToolbar = ({
  currentTabId,
  onSearchToggle,
  isSearchOpen = false,
  onSplitHorizontal,
  onSplitVertical,
}: TerminalToolbarProps) => {
  const {
    bottomTabs,
    openBottomTab,
    closeBottomTab,
    setActiveBottomTab,
    renameBottomTab,
  } = useStore(useShallow((s) => ({ bottomTabs: s.bottomTabs, openBottomTab: s.openBottomTab, closeBottomTab: s.closeBottomTab, setActiveBottomTab: s.setActiveBottomTab, renameBottomTab: s.renameBottomTab })));
  const [isEditing, setIsEditing] = useState(false);
  const [editValue, setEditValue] = useState('');
  const inputRef = useRef<HTMLInputElement>(null);

  const terminalTabs = bottomTabs.filter(
    (tab: BottomTab) =>
      tab.type === 'shell' && tab.resource.kind === 'Terminal',
  );

  const currentTab = terminalTabs.find(
    (tab: BottomTab) => tab.id === currentTabId,
  );

  useEffect(() => {
    if (isEditing && inputRef.current) {
      inputRef.current.focus();
      inputRef.current.select();
    }
  }, [isEditing]);

  const handleNewTerminal = () => {
    openBottomTab(
      'shell',
      { kind: 'Terminal', metadata: { name: 'Terminal' } },
      'local',
    );
  };

  const handleCloseTerminal = () => {
    closeBottomTab(currentTabId);
  };

  const handleStartEdit = () => {
    setEditValue(currentTab?.customTitle || currentTab?.title || 'Terminal');
    setIsEditing(true);
  };

  const handleSaveEdit = () => {
    if (editValue.trim()) {
      renameBottomTab(currentTabId, editValue.trim());
    }
    setIsEditing(false);
  };

  const handleCancelEdit = () => {
    setIsEditing(false);
    setEditValue('');
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter') {
      handleSaveEdit();
    } else if (e.key === 'Escape') {
      handleCancelEdit();
    }
  };

  return (
    <div className="terminal-toolbar">
      <div className="terminal-toolbar-left">
        {isEditing ? (
          <input
            ref={inputRef}
            type="text"
            className="terminal-name-input"
            value={editValue}
            onChange={(e) => setEditValue(e.target.value)}
            onKeyDown={handleKeyDown}
            onBlur={handleSaveEdit}
          />
        ) : (
          <>
            <DropdownMenu.Root>
              <DropdownMenu.Trigger className="terminal-selector">
                <span className="terminal-selector-title">
                  {currentTab?.customTitle || currentTab?.title || 'Terminal'}
                </span>
                <ChevronDownIcon />
              </DropdownMenu.Trigger>
              <DropdownMenu.Portal>
                <DropdownMenu.Content
                  className="terminal-dropdown-content"
                  sideOffset={5}
                >
                  {terminalTabs.map((tab: BottomTab) => (
                    <DropdownMenu.Item
                      key={tab.id}
                      className={`terminal-dropdown-item ${
                        tab.id === currentTabId ? 'active' : ''
                      }`}
                      onSelect={() => setActiveBottomTab(tab.id)}
                    >
                      <span className="terminal-dropdown-item-title">
                        {tab.customTitle || tab.title}
                      </span>
                      {tab.id === currentTabId && (
                        <span className="terminal-dropdown-item-indicator">
                          ✓
                        </span>
                      )}
                    </DropdownMenu.Item>
                  ))}
                  {terminalTabs.length === 0 && (
                    <DropdownMenu.Item
                      className="terminal-dropdown-item disabled"
                      disabled
                    >
                      No terminals open
                    </DropdownMenu.Item>
                  )}
                </DropdownMenu.Content>
              </DropdownMenu.Portal>
            </DropdownMenu.Root>

            <button
              className="terminal-action-button terminal-rename-button"
              onClick={handleStartEdit}
              title="Rename Terminal"
              aria-label="Rename Terminal"
            >
              <Pencil1Icon />
            </button>

            <button
              className={`terminal-action-button ${
                isSearchOpen ? 'active' : ''
              }`}
              onClick={onSearchToggle}
              title="Search in Terminal (Ctrl+F)"
              aria-label="Search in Terminal"
            >
              <MagnifyingGlassIcon />
            </button>
          </>
        )}

        <span className="terminal-count">
          {terminalTabs.length} terminal{terminalTabs.length !== 1 ? 's' : ''}
        </span>
      </div>

      <div className="terminal-toolbar-right">
        {onSplitHorizontal && (
          <button
            className="terminal-action-button"
            onClick={onSplitHorizontal}
            title="Split Horizontally (Top/Bottom)"
            aria-label="Split Horizontally"
          >
            <ViewHorizontalIcon />
          </button>
        )}

        {onSplitVertical && (
          <button
            className="terminal-action-button"
            onClick={onSplitVertical}
            title="Split Vertically (Left/Right)"
            aria-label="Split Vertically"
          >
            <ViewVerticalIcon />
          </button>
        )}

        <div className="terminal-toolbar-divider" />

        <button
          className="terminal-action-button"
          onClick={handleNewTerminal}
          title="New Terminal"
          aria-label="New Terminal"
        >
          <PlusIcon />
        </button>

        <button
          className="terminal-action-button"
          onClick={handleCloseTerminal}
          title="Close Terminal"
          aria-label="Close Terminal"
        >
          <Cross2Icon />
        </button>
      </div>
    </div>
  );
};

export default TerminalToolbar;
