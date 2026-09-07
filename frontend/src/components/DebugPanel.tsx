import { useEffect, useState, useRef, useCallback } from 'react';
import './DebugPanel.css';

const isDev = process.env.NODE_ENV !== 'production' || (window as any).electron?.isDev;

interface PerformanceStats {
  renderTime: number;
  nodeCount: number;
  expandedNodes: number;
  apiCalls: number;
  cacheHits: number;
  cacheMisses: number;
  avgResponseTime: number;
  memoryUsage: number;
  wsConnections: number;
  wsEventsTotal: number;
  wsEventsPerSec: number;
  lastUpdate: string;
}

interface DebugPanelProps {
  treeData: any[];
  isExpanded?: boolean;
  onToggle?: () => void;
}

const countNodes = (nodes: any[]): { total: number; expanded: number } => {
  let total = 0;
  let expanded = 0;

  const traverse = (nodeList: any[]) => {
    nodeList.forEach((node) => {
      total++;
      if (node.expanded) {
        expanded++;
        if (node.children) {
          traverse(node.children);
        }
      }
    });
  };

  traverse(nodes);
  return { total, expanded };
};

const DebugPanel = ({
  treeData,
  isExpanded = false,
  onToggle,
}: DebugPanelProps) => {
  const [height, setHeight] = useState(() => {
    const saved = localStorage.getItem('debugPanelHeight');
    return saved ? parseInt(saved, 10) : 200;
  });
  const [isResizing, setIsResizing] = useState(false);
  const panelRef = useRef<HTMLDivElement>(null);

  const [stats, setStats] = useState<PerformanceStats>({
    renderTime: 0,
    nodeCount: 0,
    expandedNodes: 0,
    apiCalls: 0,
    cacheHits: 0,
    cacheMisses: 0,
    avgResponseTime: 0,
    memoryUsage: 0,
    wsConnections: 0,
    wsEventsTotal: 0,
    wsEventsPerSec: 0,
    lastUpdate: new Date().toLocaleTimeString(),
  });

  const renderStartTime = useRef<number>(0);

  const handleMouseDown = useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
    setIsResizing(true);
  }, []);

  useEffect(() => {
    const handleMouseMove = (e: MouseEvent) => {
      if (!isResizing || !panelRef.current) return;

      const panelBottom = panelRef.current.getBoundingClientRect().bottom;
      const newHeight = panelBottom - e.clientY;
      const clampedHeight = Math.min(Math.max(newHeight, 100), 400);
      setHeight(clampedHeight);
    };

    const handleMouseUp = () => {
      if (isResizing) {
        setIsResizing(false);
        localStorage.setItem('debugPanelHeight', height.toString());
        document.body.style.cursor = '';
      }
    };

    if (isResizing) {
      document.body.style.cursor = 'ns-resize';
      document.addEventListener('mousemove', handleMouseMove);
      document.addEventListener('mouseup', handleMouseUp);
    }

    return () => {
      document.removeEventListener('mousemove', handleMouseMove);
      document.removeEventListener('mouseup', handleMouseUp);
      document.body.style.cursor = '';
    };
  }, [isResizing, height]);

  useEffect(() => {
    renderStartTime.current = performance.now();
  });

  useEffect(() => {
    if (!isDev) return;
    const renderEndTime = performance.now();
    const renderTime = renderEndTime - renderStartTime.current;

    const { total, expanded } = countNodes(treeData);

    const memoryUsage = (performance as any).memory
      ? Math.round((performance as any).memory.usedJSHeapSize / 1048576)
      : 0;

    setStats((prev) => ({
      ...prev,
      renderTime: Math.round(renderTime * 100) / 100,
      nodeCount: total,
      expandedNodes: expanded,
      memoryUsage,
      lastUpdate: new Date().toLocaleTimeString(),
    }));
  }, [treeData]);

  useEffect(() => {
    if (!isDev) return;
    const updateNetworkStats = () => {
      const apiPerformance = (window as any).__apiPerformance || [];
      const avgResponseTime =
        apiPerformance.length > 0
          ? apiPerformance.reduce((a: number, b: number) => a + b, 0) /
            apiPerformance.length
          : 0;

      const cacheHits = (window as any).__cacheHits || 0;
      const cacheMisses = (window as any).__cacheMisses || 0;
      const apiCalls = apiPerformance.length;

      setStats((prev) => ({
        ...prev,
        apiCalls,
        cacheHits,
        cacheMisses,
        avgResponseTime: Math.round(avgResponseTime * 100) / 100,
      }));
    };

    const interval = setInterval(updateNetworkStats, 1000);
    return () => clearInterval(interval);
  }, []);

  useEffect(() => {
    if (!isDev) return;
    const updateRealtimeStats = () => {
      const wsCount = (window as any).__activeWebSockets || 0;

      // Calculate WebSocket events metrics
      let wsEventsTotal = 0;
      let wsEventsPerSec = 0;

      if ((window as any).__wsEvents) {
        const wsEvents = (window as any).__wsEvents;
        wsEventsTotal = wsEvents.total || 0;

        // Calculate events per second (average over last 5 seconds)
        const now = Math.floor(Date.now() / 1000);
        let totalEvents = 0;
        let seconds = 0;
        for (let i = 0; i < 5; i++) {
          const second = now - i;
          if (wsEvents.perSecond[second]) {
            totalEvents += wsEvents.perSecond[second];
            seconds++;
          }
        }
        wsEventsPerSec = seconds > 0 ? Math.round(totalEvents / seconds) : 0;
      }

      setStats((prev) => ({
        ...prev,
        wsConnections: wsCount,
        wsEventsTotal,
        wsEventsPerSec,
      }));
    };

    const interval = setInterval(updateRealtimeStats, 1000);
    return () => clearInterval(interval);
  }, []);

  if (!isDev) return null;

  return (
    <div className="debug-panel" ref={panelRef}>
      {isExpanded && (
        <>
          <div
            className={`debug-resize-handle ${isResizing ? 'resizing' : ''}`}
            onMouseDown={handleMouseDown}
          />
          <div className="debug-content" style={{ height: `${height}px` }}>
            <div className="debug-section">
              <div className="debug-section-title">Performance</div>
              <div className="debug-stat">
                <span className="debug-label">Render:</span>
                <span className="debug-value">{stats.renderTime}ms</span>
              </div>
              <div className="debug-stat">
                <span className="debug-label">Avg API:</span>
                <span className="debug-value">{stats.avgResponseTime}ms</span>
              </div>
              <div className="debug-stat">
                <span className="debug-label">Memory:</span>
                <span className="debug-value">{stats.memoryUsage}MB</span>
              </div>
            </div>

            <div className="debug-section">
              <div className="debug-section-title">Tree Stats</div>
              <div className="debug-stat">
                <span className="debug-label">Nodes:</span>
                <span className="debug-value">{stats.nodeCount}</span>
              </div>
              <div className="debug-stat">
                <span className="debug-label">Expanded:</span>
                <span className="debug-value">{stats.expandedNodes}</span>
              </div>
            </div>

            <div className="debug-section">
              <div className="debug-section-title">Network</div>
              <div className="debug-stat">
                <span className="debug-label">API Calls:</span>
                <span className="debug-value">{stats.apiCalls}</span>
              </div>
              <div className="debug-stat">
                <span className="debug-label">Cache Hit:</span>
                <span className="debug-value">
                  {stats.cacheHits}/{stats.cacheHits + stats.cacheMisses}
                </span>
              </div>
              <div className="debug-stat">
                <span className="debug-label">WebSockets:</span>
                <span className="debug-value">{stats.wsConnections}</span>
              </div>
              <div className="debug-stat">
                <span className="debug-label">WS Events:</span>
                <span className="debug-value">{stats.wsEventsTotal}</span>
              </div>
              <div className="debug-stat">
                <span className="debug-label">Events/sec:</span>
                <span className="debug-value">{stats.wsEventsPerSec}</span>
              </div>
            </div>

            <div className="debug-footer">
              <span className="debug-timestamp">
                Updated: {stats.lastUpdate}
              </span>
            </div>
          </div>
        </>
      )}
      <div className="debug-header" onClick={onToggle}>
        <span className="debug-title">Debug Info</span>
        <span className="debug-toggle">{isExpanded ? '▼' : '▲'}</span>
      </div>
    </div>
  );
};

export default DebugPanel;
