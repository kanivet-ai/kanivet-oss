import { useState, useRef, useEffect, useLayoutEffect } from 'react';
import ReactDOM from 'react-dom';
import './ContainerSelector.css';

interface Container {
  name: string;
  image?: string;
  ready?: boolean;
  isRunning?: boolean;
  status?: {
    state?: {
      running?: any;
      waiting?: any;
      terminated?: any;
    };
  };
}

interface ContainerSelectorProps {
  containers: Container[];
  initContainers?: Container[];
  selectedContainer?: string;
  onSelectContainer: (containerName: string) => void;
  disabled?: boolean;
  showButton?: boolean;
  buttonElement?: HTMLElement | null;
}

const ContainerSelector: React.FC<ContainerSelectorProps> = ({
  containers,
  initContainers = [],
  selectedContainer,
  onSelectContainer,
  disabled = false,
  showButton = true,
  buttonElement = null,
}) => {
  const [isOpen, setIsOpen] = useState(!showButton && !buttonElement);
  const wrapperRef = useRef<HTMLDivElement>(null);
  const dropdownRef = useRef<HTMLDivElement>(null);
  const buttonRef = useRef<HTMLButtonElement>(null);
  const [dropdownStyles, setDropdownStyles] = useState<React.CSSProperties>({});

  useEffect(() => {
    if (buttonElement) {
      setIsOpen(true);
    }
  }, [buttonElement]);

  useEffect(() => {
    const handleClickOutside = (event: MouseEvent) => {
      const target = event.target as Node;
      const isInsideDropdown = dropdownRef.current?.contains(target);
      const isInsideWrapper = wrapperRef.current?.contains(target);
      const isInsideButtonElement = buttonElement?.contains(target);
      if (!isInsideDropdown && !isInsideWrapper && !isInsideButtonElement) {
        setIsOpen(false);
      }
    };

    if (isOpen) {
      document.addEventListener('mousedown', handleClickOutside);
    }

    return () => {
      document.removeEventListener('mousedown', handleClickOutside);
    };
  }, [isOpen, buttonElement]);

  const calculateDropdownPosition = (triggerElement: HTMLElement | null) => {
    if (!triggerElement) return {};

    const rect = triggerElement.getBoundingClientRect();
    const viewportHeight = window.innerHeight;
    const viewportWidth = window.innerWidth;
    const dropdownMinWidth = 300;
    const dropdownMaxHeight = 400;
    const spacing = 4;
    const edgePadding = 8;

    const styles: React.CSSProperties = {
      position: 'fixed',
      minWidth: `${dropdownMinWidth}px`,
      maxHeight: `${dropdownMaxHeight}px`,
      zIndex: 10000,
      background: 'var(--bg-primary)',
      border: '1px solid var(--border)',
      borderRadius: '4px',
      overflow: 'auto',
    };

    const spaceBelow = viewportHeight - rect.bottom - edgePadding;
    const spaceAbove = rect.top - edgePadding;

    if (spaceBelow >= 200 || spaceBelow >= spaceAbove) {
      styles.top = `${rect.bottom + spacing}px`;
      styles.maxHeight = `${Math.min(dropdownMaxHeight, spaceBelow)}px`;
    } else {
      styles.bottom = `${viewportHeight - rect.top + spacing}px`;
      styles.maxHeight = `${Math.min(dropdownMaxHeight, spaceAbove)}px`;
    }

    const spaceRight = viewportWidth - rect.left - edgePadding;

    if (spaceRight >= dropdownMinWidth) {
      styles.left = `${rect.left}px`;
    } else {
      styles.right = `${edgePadding}px`;
    }

    return styles;
  };

  useLayoutEffect(() => {
    const triggerElement = buttonElement || buttonRef.current;
    if (!triggerElement || !isOpen) return;

    const updatePosition = () => {
      setDropdownStyles(calculateDropdownPosition(triggerElement));
    };

    updatePosition();

    window.addEventListener('scroll', updatePosition, true);
    window.addEventListener('resize', updatePosition);

    return () => {
      window.removeEventListener('scroll', updatePosition, true);
      window.removeEventListener('resize', updatePosition);
    };
  }, [isOpen, buttonElement]);

  const getContainerState = (container: Container) => {
    if (container.status?.state?.running) return 'Running';
    if (container.status?.state?.waiting) return 'Waiting';
    if (container.status?.state?.terminated) return 'Terminated';
    return 'Unknown';
  };

  const getContainerStateDetails = (container: Container) => {
    if (container.status?.state?.waiting)
      return container.status.state.waiting.reason || 'Unknown';
    if (container.status?.state?.terminated)
      return container.status.state.terminated.reason || 'Unknown';
    return null;
  };

  const getStatusBadgeClass = (state: string) => {
    switch (state) {
      case 'Running':
        return 'success';
      case 'Waiting':
        return 'warning';
      case 'Terminated':
        return 'danger';
      default:
        return 'neutral';
    }
  };

  const allContainers = [
    ...containers.map((c) => ({ ...c, type: 'container' })),
    ...initContainers.map((c) => ({ ...c, type: 'init-container' })),
  ];

  const displayName = selectedContainer || 'Select container...';

  const handleContainerSelect = (containerName: string, isRunning: boolean) => {
    if (!isRunning) return;
    onSelectContainer(containerName);
    setIsOpen(false);
  };

  // When buttonElement is provided, render dropdown as a portal
  if (buttonElement) {
    if (!isOpen) return null;

    return ReactDOM.createPortal(
      <div
        className="container-selector-dropdown"
        style={dropdownStyles}
        ref={dropdownRef}
      >
        <div className="container-selector-list">
          {allContainers.map((container) => {
            const isRunning = container.isRunning ?? true;
            const state = getContainerState(container);
            const stateDetails = getContainerStateDetails(container);
            const isSelected = container.name === selectedContainer;

            return (
              <div
                key={`${container.type}-${container.name}`}
                className={`container-selector-row ${
                  !isRunning ? 'disabled' : ''
                } ${isSelected ? 'selected' : ''}`}
                onClick={() => handleContainerSelect(container.name, isRunning)}
                title={
                  !isRunning
                    ? `Container is not running (${state}: ${
                        stateDetails || ''
                      })`
                    : ''
                }
              >
                <div className="container-info">
                  <div className="container-name-line">
                    <span className="container-name">{container.name}</span>
                    {container.type === 'init-container' && (
                      <span className="container-badge init">INIT</span>
                    )}
                    <span
                      className={`status-badge ${getStatusBadgeClass(state)}`}
                    >
                      {state}
                    </span>
                  </div>
                  {container.image && (
                    <span className="container-image">{container.image}</span>
                  )}
                  {stateDetails && !isRunning && (
                    <span className="container-state-details">
                      {stateDetails}
                    </span>
                  )}
                </div>
                {isSelected && <span className="container-check">✓</span>}
              </div>
            );
          })}
        </div>
      </div>,
      document.body,
    );
  }

  const dropdownContent = (
    <div className="container-selector-list">
      {allContainers.map((container) => {
        const isRunning = container.isRunning ?? true;
        const state = getContainerState(container);
        const stateDetails = getContainerStateDetails(container);
        const isSelected = container.name === selectedContainer;

        return (
          <div
            key={`${container.type}-${container.name}`}
            className={`container-selector-row ${!isRunning ? 'disabled' : ''} ${isSelected ? 'selected' : ''}`}
            onClick={() => handleContainerSelect(container.name, isRunning)}
            title={!isRunning ? `Container is not running (${state}: ${stateDetails || ''})` : ''}
          >
            <div className="container-info">
              <div className="container-name-line">
                <span className="container-name">{container.name}</span>
                {container.type === 'init-container' && (
                  <span className="container-badge init">INIT</span>
                )}
                <span className={`status-badge ${getStatusBadgeClass(state)}`}>{state}</span>
              </div>
              {container.image && <span className="container-image">{container.image}</span>}
              {stateDetails && !isRunning && (
                <span className="container-state-details">{stateDetails}</span>
              )}
            </div>
            {isSelected && <span className="container-check">✓</span>}
          </div>
        );
      })}
    </div>
  );

  return (
    <div className="container-selector-wrapper" ref={wrapperRef}>
      {showButton && (
        <button
          ref={buttonRef}
          className="container-selector-button"
          onClick={() => setIsOpen(!isOpen)}
          disabled={disabled}
        >
          <span className="container-selector-text">{displayName}</span>
          <span className={`container-selector-caret ${isOpen ? 'open' : ''}`}>▼</span>
        </button>
      )}
      {isOpen && !buttonElement && ReactDOM.createPortal(
        <div className="container-selector-dropdown" style={dropdownStyles} ref={dropdownRef}>
          {dropdownContent}
        </div>,
        document.body
      )}
    </div>
  );
};

export default ContainerSelector;
