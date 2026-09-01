import React, { useEffect } from 'react';
import './KeyboardShortcutsModal.css';
import { Cross2Icon } from '@radix-ui/react-icons';

interface Props {
  isOpen: boolean;
  onClose: () => void;
}

const Key: React.FC<{ children: React.ReactNode }> = ({ children }) => <span className="ks-key">{children}</span>;

const ShortcutRow: React.FC<{ keys: React.ReactNode; desc: string }> = ({ keys, desc }) => (
  <div className="ks-row">
    <div className="ks-keys">{keys}</div>
    <div className="ks-desc">{desc}</div>
  </div>
);

const KeyboardShortcutsModal: React.FC<Props> = ({ isOpen, onClose }) => {
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && isOpen) {
        e.preventDefault();
        onClose();
      }
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [isOpen, onClose]);

  if (!isOpen) return null;

  return (
    <div className="ks-overlay" onClick={onClose}>
      <div className="ks-modal" onClick={(e) => e.stopPropagation()}>
        <div className="ks-header">
          <div className="ks-title">Keyboard Shortcuts</div>
          <button className="ks-close" onClick={onClose} aria-label="Close" title="Close">
            <Cross2Icon width={14} height={14} />
          </button>
        </div>
        <div className="ks-body">
          <div className="ks-section">
            <div className="ks-section-title">Global</div>
            <ShortcutRow
              keys={<><Key>⌘</Key><Key>K</Key></>}
              desc="Command palette"
            />
            <ShortcutRow
              keys={<><Key>⌘</Key><Key>Shift</Key><Key>P</Key></>}
              desc="Command palette"
            />
            <ShortcutRow
              keys={<><Key>s</Key> <span className="ks-then">then</span> <Key>c</Key></>}
              desc="Switch cluster"
            />
            <ShortcutRow
              keys={<><Key>⌘</Key><Key>T</Key></>}
              desc="Cluster selector"
            />
            <ShortcutRow
              keys={<><Key>⌘</Key><Key>,</Key></>}
              desc="Theme settings"
            />
            <ShortcutRow
              keys={<><Key>⌘</Key><Key>1-9</Key></>}
              desc="Switch to tab 1-9"
            />
            <ShortcutRow
              keys={<><Key>Ctrl</Key><Key>`</Key></>}
              desc="Open terminal"
            />
          </div>
          <div className="ks-section">
            <div className="ks-section-title">Navigation</div>
            <ShortcutRow
              keys={<><Key>⌥</Key><Key>←</Key> / <Key>⌥</Key><Key>→</Key></>}
              desc="Back / Forward"
            />
            <ShortcutRow
              keys={<><Key>⌘</Key><Key>[</Key> / <Key>⌘</Key><Key>]</Key></>}
              desc="Back / Forward"
            />
            <ShortcutRow
              keys={<><Key>Ctrl</Key><Key>O</Key> / <Key>Ctrl</Key><Key>I</Key></>}
              desc="Back / Forward"
            />
            <ShortcutRow
              keys={<><Key>Tab</Key></>}
              desc="Cycle focus areas"
            />
            <ShortcutRow
              keys={<><Key>Shift</Key><Key>Tab</Key></>}
              desc="Cycle focus backward"
            />
          </div>
          <div className="ks-section">
            <div className="ks-section-title">Tree</div>
            <ShortcutRow
              keys={<><Key>/</Key> or <Key>⌘</Key><Key>F</Key></>}
              desc="Search"
            />
            <ShortcutRow
              keys={<><Key>j</Key> / <Key>k</Key></>}
              desc="Move down / up"
            />
            <ShortcutRow
              keys={<><Key>Ctrl</Key><Key>D</Key> / <Key>Ctrl</Key><Key>U</Key></>}
              desc="Jump 10 items"
            />
            <ShortcutRow
              keys={<><Key>h</Key></>}
              desc="Collapse / parent"
            />
            <ShortcutRow
              keys={<><Key>l</Key></>}
              desc="Expand / child"
            />
            <ShortcutRow
              keys={<><Key>Enter</Key></>}
              desc="Open resource"
            />
            <ShortcutRow
              keys={<><Key>⌥</Key><Key>Enter</Key></>}
              desc="Open in split"
            />
          </div>
          <div className="ks-section">
            <div className="ks-section-title">Resource List</div>
            <ShortcutRow
              keys={<><Key>/</Key></>}
              desc="Search"
            />
            <ShortcutRow
              keys={<><Key>j</Key> / <Key>k</Key></>}
              desc="Move down / up"
            />
            <ShortcutRow
              keys={<><Key>Ctrl</Key><Key>D</Key> / <Key>Ctrl</Key><Key>U</Key></>}
              desc="Jump 10 items"
            />
            <ShortcutRow
              keys={<><Key>h</Key></>}
              desc="Back to tree"
            />
            <ShortcutRow
              keys={<><Key>l</Key></>}
              desc="Open details"
            />
            <ShortcutRow
              keys={<><Key>Enter</Key></>}
              desc="Open details"
            />
            <ShortcutRow
              keys={<><Key>r</Key></>}
              desc="Reload list"
            />
            <ShortcutRow
              keys={<><Key>Esc</Key></>}
              desc="Clear / back to tree"
            />
          </div>
          <div className="ks-section">
            <div className="ks-section-title">Split Panes</div>
            <ShortcutRow
              keys={<><Key>⌘</Key><Key>\</Key></>}
              desc="Split vertical"
            />
            <ShortcutRow
              keys={<><Key>⌘</Key><Key>Shift</Key><Key>\</Key></>}
              desc="Split horizontal"
            />
          </div>
        </div>
      </div>
    </div>
  );
};

export default KeyboardShortcutsModal;

