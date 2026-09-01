import React, { useState } from 'react';
import { createPortal } from 'react-dom';
import './StatusIndicator.css';

interface StatusItem {
  name?: string;
  ready?: boolean;
  phase?: string;
  reason?: string;
  terminating?: boolean;
  restarts?: number;
  restartCount?: number;
  state?: any;
}

interface StatusIndicatorProps {
  items: StatusItem[];
  initItems?: StatusItem[];
  type: 'pod' | 'container';
  showCount?: boolean;
}

const StatusIndicator: React.FC<StatusIndicatorProps> = ({
  items,
  initItems,
  type,
  showCount = true,
}) => {
  const [hoveredItem, setHoveredItem] = useState<{ item: StatusItem; isInit: boolean } | null>(null);
  const [mousePos, setMousePos] = useState({ x: 0, y: 0 });
  const hasInitItems = initItems && Array.isArray(initItems) && initItems.length > 0;
  const hasItems = items && Array.isArray(items) && items.length > 0;

  if (!hasItems && !hasInitItems) {
    return <span>-</span>;
  }

  const getStatusClass = (item: StatusItem) => {
    if (type === 'pod') {
      if (item.terminating) return 'terminating';
      if (item.ready) return 'running';
      if (item.phase === 'Pending') return 'waiting';
      if (item.phase === 'Failed') return 'failed';
      if (item.phase === 'Unknown') return 'unknown';
      if (item.phase === 'Running' && !item.ready) return 'not-ready';
      return 'unknown';
    } else {
      if (item.state?.terminated) {
        if (item.state.terminated.reason === 'Completed') return 'completed';
        return 'terminated';
      }
      if (item.ready) return 'running';
      if (item.state?.running) return 'running';
      if (item.state?.waiting) return 'waiting';
      return 'unknown';
    }
  };

  const getStatusText = (item: StatusItem) => {
    const name = item.name || 'unknown';
    let status = 'Unknown';

    if (type === 'pod') {
      if (item.terminating) {
        status = 'Terminating';
      } else if (item.ready) {
        status = 'Ready';
      } else if (item.phase === 'Pending') {
        status = item.reason || 'Pending';
      } else if (item.phase === 'Failed') {
        status = item.reason || 'Failed';
      } else if (item.phase === 'Running' && !item.ready) {
        status = 'Not Ready';
      }
    } else {
      if (item.state?.terminated) {
        status = item.state.terminated.reason || 'Terminated';
      } else if (item.ready) {
        status = 'Running';
      } else if (item.state?.running) {
        status = 'Running (not ready)';
      } else if (item.state?.waiting) {
        status = item.state.waiting.reason || 'Waiting';
      }
    }

    const restarts =
      (item.restarts || item.restartCount || 0) > 0
        ? ` (${item.restarts || item.restartCount} restarts)`
        : '';
    return `${name}: ${status}${restarts}`;
  };

  const handleMouseEnter = (item: StatusItem, isInit: boolean, e: React.MouseEvent) => {
    const rect = e.currentTarget.getBoundingClientRect();
    setMousePos({ x: rect.left + rect.width / 2, y: rect.top });
    setHoveredItem({ item, isInit });
  };

  const handleMouseLeave = () => {
    setHoveredItem(null);
  };

  const readyCount = (items || []).filter((p) => p.ready && !p.terminating).length;
  const totalCount = (items || []).length;

  const getTooltipText = () => {
    if (!hoveredItem) return '';
    const prefix = hoveredItem.isInit ? '[init] ' : '';
    return prefix + getStatusText(hoveredItem.item);
  };

  return (
    <>
      <div className={`status-indicator-summary ${type}-status-summary`}>
        {showCount && type === 'pod' && (
          <span className="status-count">
            {readyCount}/{totalCount}
          </span>
        )}
        <div className="status-indicator-wrapper">
          {hasInitItems && initItems!.map((item, index) => (
            <div
              key={`init-${index}`}
              className={`status-indicator ${type}-status-indicator ${getStatusClass(item)}`}
              onMouseEnter={(e) => handleMouseEnter(item, true, e)}
              onMouseLeave={handleMouseLeave}
            />
          ))}
          {hasInitItems && hasItems && <div className="status-indicator-separator" />}
          {hasItems && items.map((item, index) => (
            <div
              key={`container-${index}`}
              className={`status-indicator ${type}-status-indicator ${getStatusClass(item)}`}
              onMouseEnter={(e) => handleMouseEnter(item, false, e)}
              onMouseLeave={handleMouseLeave}
            />
          ))}
        </div>
      </div>
      {hoveredItem && document.body && createPortal(
        <div
          className="status-indicator-tooltip"
          style={{
            position: 'fixed',
            left: mousePos.x,
            top: mousePos.y - 35,
            transform: 'translateX(-50%)',
          }}
        >
          {getTooltipText()}
        </div>,
        document.body
      )}
    </>
  );
};

export default StatusIndicator;
