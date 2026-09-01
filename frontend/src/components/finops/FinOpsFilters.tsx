import React, { useState, useRef, useEffect } from 'react';
import { createPortal } from 'react-dom';
import { MagnifyingGlassIcon, Cross2Icon, MixerHorizontalIcon } from '@radix-ui/react-icons';
import './FinOpsFilters.css';

export interface FinOpsFilterState {
  search: string;
  selectedNamespaces: string[];
  efficiencyMin: number;
  efficiencyMax: number;
  costMin: number;
  costMax: number;
  showNoRequests: boolean;
  showOverprovisioned: boolean;
  showOvercommitted: boolean;
}

interface FinOpsFiltersProps {
  filters: FinOpsFilterState;
  onFiltersChange: (filters: FinOpsFilterState) => void;
  stats: {
    totalNamespaces: number;
    totalNodes: number;
    filteredNamespaces: number;
    filteredNodes: number;
  };
  availableNamespaces: string[];
}

export const FinOpsFilters: React.FC<FinOpsFiltersProps> = ({ filters, onFiltersChange, stats, availableNamespaces }) => {
  const [isExpanded, setIsExpanded] = useState(false);
  const [showNsDropdown, setShowNsDropdown] = useState(false);
  const dropdownRef = useRef<HTMLDivElement>(null);
  const buttonRef = useRef<HTMLButtonElement>(null);
  const [dropdownStyle, setDropdownStyle] = useState<React.CSSProperties>({});

  useEffect(() => {
    const updatePosition = () => {
      if (showNsDropdown && buttonRef.current) {
        const rect = buttonRef.current.getBoundingClientRect();
        const style = {
          top: rect.bottom + 4,
          left: rect.left,
          minWidth: Math.max(rect.width, 250),
        };
        console.log('[Dropdown Position]', {
          buttonRect: { top: rect.top, bottom: rect.bottom, left: rect.left, width: rect.width },
          dropdownStyle: style,
          scrollY: window.scrollY,
        });
        setDropdownStyle(style);
      }
    };

    updatePosition();
    
    if (showNsDropdown) {
      window.addEventListener('resize', updatePosition);
      window.addEventListener('scroll', updatePosition, true);
      return () => {
        window.removeEventListener('resize', updatePosition);
        window.removeEventListener('scroll', updatePosition, true);
      };
    }
  }, [showNsDropdown]);

  useEffect(() => {
    const handleClickOutside = (event: MouseEvent) => {
      if (dropdownRef.current && !dropdownRef.current.contains(event.target as Node) &&
          buttonRef.current && !buttonRef.current.contains(event.target as Node)) {
        setShowNsDropdown(false);
      }
    };

    if (showNsDropdown) {
      document.addEventListener('mousedown', handleClickOutside);
      return () => document.removeEventListener('mousedown', handleClickOutside);
    }
  }, [showNsDropdown]);

  const handleSearchChange = (search: string) => {
    onFiltersChange({ ...filters, search });
  };

  const handleReset = () => {
    onFiltersChange({
      search: '',
      selectedNamespaces: [],
      efficiencyMin: 0,
      efficiencyMax: 200,
      costMin: 0,
      costMax: 999999,
      showNoRequests: false,
      showOverprovisioned: false,
      showOvercommitted: false,
    });
    setIsExpanded(false);
  };

  const toggleNamespace = (ns: string) => {
    const selected = [...filters.selectedNamespaces];
    const idx = selected.indexOf(ns);
    if (idx >= 0) {
      selected.splice(idx, 1);
    } else {
      selected.push(ns);
    }
    onFiltersChange({ ...filters, selectedNamespaces: selected });
  };

  const activeFilterCount = [
    filters.search !== '',
    filters.selectedNamespaces.length > 0,
    filters.efficiencyMin > 0 || filters.efficiencyMax < 200,
    filters.costMin > 0 || filters.costMax < 999999,
    filters.showNoRequests,
    filters.showOverprovisioned,
    filters.showOvercommitted,
  ].filter(Boolean).length;

  return (
    <div className="finops-filters">
      <div className="filters-header">
        <div className="search-box">
          <MagnifyingGlassIcon className="search-icon" />
          <input
            type="text"
            placeholder="Search namespaces, workloads, nodes..."
            value={filters.search}
            onChange={(e) => handleSearchChange(e.target.value)}
            className="search-input"
          />
          {filters.search && (
            <button
              className="search-clear"
              onClick={() => handleSearchChange('')}
              title="Clear search"
            >
              <Cross2Icon />
            </button>
          )}
        </div>

        <div className="filters-actions">
          {(stats.filteredNamespaces < stats.totalNamespaces || stats.filteredNodes < stats.totalNodes) && (
            <div className="filter-results">
              {stats.filteredNamespaces < stats.totalNamespaces && (
                <>
                  <span className="result-count">{stats.filteredNamespaces}</span>
                  <span className="result-label">of {stats.totalNamespaces} namespaces</span>
                </>
              )}
              {stats.filteredNamespaces < stats.totalNamespaces && stats.filteredNodes < stats.totalNodes && (
                <span className="result-separator">•</span>
              )}
              {stats.filteredNodes < stats.totalNodes && (
                <>
                  <span className="result-count">{stats.filteredNodes}</span>
                  <span className="result-label">of {stats.totalNodes} nodes</span>
                </>
              )}
            </div>
          )}
          
          <button
            className={`filter-toggle ${isExpanded ? 'active' : ''}`}
            onClick={() => setIsExpanded(!isExpanded)}
            title="Advanced filters"
          >
            <MixerHorizontalIcon />
            Filters
            {activeFilterCount > 0 && (
              <span className="filter-badge">{activeFilterCount}</span>
            )}
          </button>

          {activeFilterCount > 0 && (
            <button className="filter-reset" onClick={handleReset} title="Clear all filters">
              Reset
            </button>
          )}
        </div>
      </div>

      {isExpanded && (
        <div className="filters-expanded">
          <div className="filter-grid">
            <div className="filter-group filter-ns">
              <label className="filter-label">Namespaces</label>
              <div className="namespace-selector">
                <button 
                  ref={buttonRef}
                  className="namespace-dropdown-btn"
                  onClick={() => setShowNsDropdown(!showNsDropdown)}
                >
                  {filters.selectedNamespaces.length === 0 
                    ? 'All namespaces' 
                    : `${filters.selectedNamespaces.length} selected`}
                </button>
              </div>
            </div>

            <div className="filter-group">
              <label className="filter-label">Efficiency %</label>
              <div className="filter-range">
                <input
                  type="number"
                  min="0"
                  max="200"
                  value={filters.efficiencyMin || ''}
                  onChange={(e) => onFiltersChange({ ...filters, efficiencyMin: Number(e.target.value) || 0 })}
                  className="filter-input"
                  placeholder="Min"
                />
                <span className="range-separator">-</span>
                <input
                  type="number"
                  min="0"
                  max="200"
                  value={filters.efficiencyMax === 200 ? '' : filters.efficiencyMax}
                  onChange={(e) => onFiltersChange({ ...filters, efficiencyMax: Number(e.target.value) || 200 })}
                  className="filter-input"
                  placeholder="Max"
                />
              </div>
              <div className="filter-presets">
                <button className="preset-chip critical" onClick={() => onFiltersChange({ ...filters, efficiencyMin: 0, efficiencyMax: 30 })}>
                  &lt;30%
                </button>
                <button className="preset-chip overcommit" onClick={() => onFiltersChange({ ...filters, efficiencyMin: 85, efficiencyMax: 100 })}>
                  &gt;85%
                </button>
              </div>
            </div>

            <div className="filter-group">
              <label className="filter-label">Cost $/mo</label>
              <div className="filter-range">
                <input
                  type="number"
                  min="0"
                  value={filters.costMin || ''}
                  onChange={(e) => onFiltersChange({ ...filters, costMin: Number(e.target.value) || 0 })}
                  className="filter-input"
                  placeholder="Min"
                />
                <span className="range-separator">-</span>
                <input
                  type="number"
                  min="0"
                  value={filters.costMax === 999999 ? '' : filters.costMax}
                  onChange={(e) => onFiltersChange({ ...filters, costMax: Number(e.target.value) || 999999 })}
                  className="filter-input"
                  placeholder="Max"
                />
              </div>
              <div className="filter-presets">
                <button className="preset-chip" onClick={() => onFiltersChange({ ...filters, costMin: 100, costMax: 999999 })}>
                  &gt;$100
                </button>
                <button className="preset-chip" onClick={() => onFiltersChange({ ...filters, costMin: 0, costMax: 10 })}>
                  &lt;$10
                </button>
              </div>
            </div>

            <div className="filter-group">
              <label className="filter-label">Show Only</label>
              <div className="filter-checkboxes">
                <label className="filter-checkbox">
                  <input
                    type="checkbox"
                    checked={filters.showNoRequests}
                    onChange={(e) => onFiltersChange({ ...filters, showNoRequests: e.target.checked })}
                  />
                  <span>No resource requests</span>
                </label>
                <label className="filter-checkbox">
                  <input
                    type="checkbox"
                    checked={filters.showOverprovisioned}
                    onChange={(e) => onFiltersChange({ ...filters, showOverprovisioned: e.target.checked })}
                  />
                  <span>Underutilized (&lt;30%)</span>
                </label>
                <label className="filter-checkbox">
                  <input
                    type="checkbox"
                    checked={filters.showOvercommitted}
                    onChange={(e) => onFiltersChange({ ...filters, showOvercommitted: e.target.checked })}
                  />
                  <span>Overcommitted (&gt;85%)</span>
                </label>
              </div>
            </div>
          </div>

        </div>
      )}

      {showNsDropdown && createPortal(
        <div ref={dropdownRef} className="namespace-dropdown" style={dropdownStyle} onClick={(e) => e.stopPropagation()}>
          <div className="namespace-dropdown-header">
            <span className="ns-count">{availableNamespaces.length} namespaces</span>
            {filters.selectedNamespaces.length > 0 && (
              <button 
                className="ns-select-all"
                onClick={() => onFiltersChange({ ...filters, selectedNamespaces: [] })}
              >
                Clear All
              </button>
            )}
          </div>
          <div className="namespace-list">
            {availableNamespaces.length === 0 ? (
              <div className="namespace-empty">No namespaces available</div>
            ) : (
              [...availableNamespaces].sort().map(ns => (
                <label key={ns} className="namespace-item" onClick={(e) => e.stopPropagation()}>
                  <input
                    type="checkbox"
                    checked={filters.selectedNamespaces.includes(ns)}
                    onChange={() => toggleNamespace(ns)}
                  />
                  <span>{ns}</span>
                </label>
              ))
            )}
          </div>
        </div>,
        document.body
      )}
    </div>
  );
};

