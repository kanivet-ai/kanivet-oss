import { forwardRef } from 'react';
import './SearchInput.css';

interface SearchInputProps {
  value: string;
  onChange: (value: string) => void;
  onClear?: () => void;
  placeholder?: string;
  showResultCount?: boolean;
  resultCount?: number;
  totalCount?: number;
}

const SearchInput = forwardRef<HTMLInputElement, SearchInputProps>(
  (
    {
      value,
      onChange,
      onClear,
      placeholder = 'Search...',
      showResultCount = false,
      resultCount = 0,
      totalCount = 0,
    },
    ref,
  ) => {
    const handleClear = () => {
      onChange('');
      onClear?.();
    };

    return (
      <div className="search-input-container">
        <input
          ref={ref}
          type="text"
          className="search-input"
          placeholder={placeholder}
          value={value}
          onChange={(e) => onChange(e.target.value)}
        />
        {value && (
          <button
            className="search-clear"
            onClick={handleClear}
            title="Clear search"
          >
            ✕
          </button>
        )}
        {showResultCount && value && (
          <span className="search-results-count">
            {resultCount} of {totalCount}
          </span>
        )}
      </div>
    );
  },
);

SearchInput.displayName = 'SearchInput';

export default SearchInput;
