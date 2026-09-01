import { useState, useRef, useEffect } from 'react';
import './NamespaceSelector.css';

interface NamespaceSelectorProps {
  namespaces: string[];
  selectedNamespaces: string[];
  onChange: (namespace: string) => void;
  placeholder?: string;
}

const NamespaceSelector = ({
  namespaces,
  selectedNamespaces,
  onChange,
  placeholder = 'All namespaces',
}: NamespaceSelectorProps) => {
  const [isOpen, setIsOpen] = useState(false);
  const [searchQuery, setSearchQuery] = useState('');
  const [highlightedIndex, setHighlightedIndex] = useState(-1);
  const dropdownRef = useRef<HTMLDivElement | null>(null);
  const searchInputRef = useRef<HTMLInputElement | null>(null);

  useEffect(() => {
    if (isOpen && searchInputRef.current) {
      searchInputRef.current.focus();
    }
  }, [isOpen]);

  useEffect(() => {
    const handleClickOutside = (e: MouseEvent) => {
      if (
        dropdownRef.current &&
        !dropdownRef.current.contains(e.target as Node)
      ) {
        setIsOpen(false);
        setSearchQuery('');
      }
    };

    document.addEventListener('mousedown', handleClickOutside);
    return () => document.removeEventListener('mousedown', handleClickOutside);
  }, []);

  const filteredNamespaces = namespaces.filter((ns) =>
    ns.toLowerCase().includes(searchQuery.toLowerCase()),
  );

  const handleKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    const totalOptions = filteredNamespaces.length + 1;
    const selectedIndex = selectedNamespaces.length === 0 ? 0 :
      filteredNamespaces.indexOf(selectedNamespaces[0]) + 1;
    const getNextIndex = (current: number, delta: number) => {
      let next = current;
      for (let i = 0; i < totalOptions; i++) {
        next = delta > 0
          ? (next < totalOptions - 1 ? next + 1 : 0)
          : (next > 0 ? next - 1 : totalOptions - 1);
        if (next !== selectedIndex) return next;
      }
      return current;
    };

    switch (e.key) {
      case 'ArrowDown':
        e.preventDefault();
        setHighlightedIndex((prev) => getNextIndex(prev, 1));
        break;
      case 'ArrowUp':
        e.preventDefault();
        setHighlightedIndex((prev) => getNextIndex(prev, -1));
        break;
      case 'Enter':
        e.preventDefault();
        if (highlightedIndex === 0) {
          onChange('all');
        } else if (
          highlightedIndex > 0 &&
          highlightedIndex <= filteredNamespaces.length
        ) {
          onChange(filteredNamespaces[highlightedIndex - 1]);
        }
        setIsOpen(false);
        setSearchQuery('');
        setHighlightedIndex(-1);
        break;
      case 'Escape':
        setIsOpen(false);
        setSearchQuery('');
        setHighlightedIndex(-1);
        e.currentTarget.blur();
        break;
    }
    e.stopPropagation();
  };

  const displayValue =
    selectedNamespaces.length === 0 ? placeholder : selectedNamespaces[0];

  return (
    <div className="namespace-selector" ref={dropdownRef}>
      <label>Namespaces:</label>
      <div className="ns-dropdown">
        <div className="ns-trigger-wrapper">
          <input
            ref={searchInputRef}
            type="text"
            className="ns-trigger-input"
            placeholder={displayValue}
            value={searchQuery}
            onChange={(e) => {
              setSearchQuery(e.target.value);
              setHighlightedIndex(-1);
            }}
            onFocus={() => {
              setIsOpen(true);
              setHighlightedIndex(-1);
            }}
            onClick={() => setIsOpen(true)}
            onKeyDown={handleKeyDown}
          />
          <span className="caret">▾</span>
        </div>
        {isOpen && (
          <div className="ns-menu">
            <div
              className={`ns-option all ${
                selectedNamespaces.length === 0 ? 'selected' : ''
              } ${highlightedIndex === 0 ? 'highlighted' : ''}`}
              onClick={() => {
                onChange('all');
                setIsOpen(false);
                setSearchQuery('');
              }}
              onMouseEnter={() => setHighlightedIndex(0)}
            >
              <span className="ns-option-text">{placeholder}</span>
              {selectedNamespaces.length === 0 && (
                <span className="ns-option-check">✓</span>
              )}
            </div>
            <div className="ns-divider"></div>
            <div className="ns-options-scroll">
              {filteredNamespaces.map((ns, index) => (
                <div
                  key={ns}
                  className={`ns-option ${
                    selectedNamespaces.length === 1 &&
                    selectedNamespaces[0] === ns
                      ? 'selected'
                      : ''
                  } ${highlightedIndex === index + 1 ? 'highlighted' : ''}`}
                  onClick={() => {
                    onChange(ns);
                    setIsOpen(false);
                    setSearchQuery('');
                  }}
                  onMouseEnter={() => setHighlightedIndex(index + 1)}
                >
                  <span className="ns-option-text">{ns}</span>
                  {selectedNamespaces.length === 1 &&
                    selectedNamespaces[0] === ns && (
                      <span className="ns-option-check">✓</span>
                    )}
                </div>
              ))}
            </div>
          </div>
        )}
      </div>
    </div>
  );
};

export default NamespaceSelector;
