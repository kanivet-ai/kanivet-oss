import { useState, useRef, useEffect } from 'react';
import {
  Cross2Icon,
  ChevronUpIcon,
  ChevronDownIcon,
  LetterCaseCapitalizeIcon,
  TextAlignCenterIcon,
  CodeIcon,
} from '@radix-ui/react-icons';
import './TerminalSearch.css';

interface TerminalSearchProps {
  onSearch: (
    term: string,
    direction: 'next' | 'previous',
    options?: SearchOptions,
  ) => void;
  onClose: () => void;
  resultCount?: number;
  currentResult?: number;
}

export interface SearchOptions {
  caseSensitive: boolean;
  wholeWord: boolean;
  regex: boolean;
}

const TerminalSearch = ({
  onSearch,
  onClose,
  resultCount = 0,
  currentResult = 0,
}: TerminalSearchProps) => {
  const [searchTerm, setSearchTerm] = useState('');
  const [caseSensitive, setCaseSensitive] = useState(false);
  const [wholeWord, setWholeWord] = useState(false);
  const [useRegex, setUseRegex] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    // Focus input when component mounts
    if (inputRef.current) {
      inputRef.current.focus();
      inputRef.current.select();
    }
  }, []);

  useEffect(() => {
    // Trigger search whenever term or options change
    if (searchTerm) {
      onSearch(searchTerm, 'next', {
        caseSensitive,
        wholeWord,
        regex: useRegex,
      });
    }
  }, [searchTerm, caseSensitive, wholeWord, useRegex, onSearch]);

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Escape') {
      onClose();
    } else if (e.key === 'Enter') {
      const options = { caseSensitive, wholeWord, regex: useRegex };
      if (e.shiftKey) {
        onSearch(searchTerm, 'previous', options);
      } else {
        onSearch(searchTerm, 'next', options);
      }
    }
  };

  return (
    <div className="terminal-search-bar">
      <input
        ref={inputRef}
        type="text"
        className="terminal-search-input"
        placeholder="Find..."
        value={searchTerm}
        onChange={(e) => setSearchTerm(e.target.value)}
        onKeyDown={handleKeyDown}
      />

      <div className="terminal-search-info">
        {searchTerm && resultCount > 0 && (
          <span className="terminal-search-count">
            {currentResult}/{resultCount}
          </span>
        )}
        {searchTerm && resultCount === 0 && (
          <span className="terminal-search-no-results">No results</span>
        )}
      </div>

      <div className="terminal-search-options">
        <button
          className={`terminal-search-option ${caseSensitive ? 'active' : ''}`}
          onClick={() => setCaseSensitive(!caseSensitive)}
          title="Match Case - When enabled, 'main' will not match 'Main'"
          aria-label="Match Case"
        >
          <LetterCaseCapitalizeIcon />
        </button>

        <button
          className={`terminal-search-option ${wholeWord ? 'active' : ''}`}
          onClick={() => setWholeWord(!wholeWord)}
          title="Match Whole Word - When enabled, 'main' will not match 'mainly'"
          aria-label="Match Whole Word"
        >
          <TextAlignCenterIcon />
        </button>

        <button
          className={`terminal-search-option ${useRegex ? 'active' : ''}`}
          onClick={() => setUseRegex(!useRegex)}
          title="Use Regular Expression - Enable regex patterns like 'ma.*' to match 'main', 'make', etc."
          aria-label="Use Regular Expression"
        >
          <CodeIcon />
        </button>
      </div>

      <div className="terminal-search-actions">
        <button
          className="terminal-search-button"
          onClick={() =>
            onSearch(searchTerm, 'previous', {
              caseSensitive,
              wholeWord,
              regex: useRegex,
            })
          }
          disabled={!searchTerm || resultCount === 0}
          title="Previous match (Shift+Enter)"
          aria-label="Previous match"
        >
          <ChevronUpIcon />
        </button>

        <button
          className="terminal-search-button"
          onClick={() =>
            onSearch(searchTerm, 'next', {
              caseSensitive,
              wholeWord,
              regex: useRegex,
            })
          }
          disabled={!searchTerm || resultCount === 0}
          title="Next match (Enter)"
          aria-label="Next match"
        >
          <ChevronDownIcon />
        </button>

        <button
          className="terminal-search-button"
          onClick={onClose}
          title="Close search (Escape)"
          aria-label="Close search"
        >
          <Cross2Icon />
        </button>
      </div>
    </div>
  );
};

export default TerminalSearch;
