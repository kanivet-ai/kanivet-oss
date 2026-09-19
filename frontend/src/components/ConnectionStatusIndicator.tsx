import { useStore } from '../store';
import { useShallow } from 'zustand/react/shallow';
import './ConnectionStatusIndicator.css';

const ConnectionStatusIndicator = () => {
  const { backendState, websocketState, getOverallState, reconnectCountdown } = useStore(useShallow((s) => ({ backendState: s.backendState, websocketState: s.websocketState, getOverallState: s.getOverallState, reconnectCountdown: s.reconnectCountdown })));
  const overallState = getOverallState();

  if (overallState === 'connected') return null;

  const getStatusInfo = () => {
    if (backendState === 'disconnected') {
      return { label: 'Backend offline', className: 'disconnected' };
    }
    if (websocketState === 'disconnected') {
      return { label: 'Disconnected', className: 'disconnected' };
    }
    if (overallState === 'reconnecting') {
      const countdownText = reconnectCountdown ? ` (${reconnectCountdown}s)` : '';
      return { label: `Reconnecting${countdownText}`, className: 'reconnecting' };
    }
    if (overallState === 'connecting') {
      return { label: 'Connecting', className: 'connecting' };
    }
    return null;
  };

  const info = getStatusInfo();
  if (!info) return null;

  return (
    <div className={`connection-status-indicator ap-badge ${info.className}`} title={`Backend: ${backendState}, WebSocket: ${websocketState}`}>
      {/* The dot / spinner ring is drawn in CSS */}
      <span className="connection-status-icon" aria-hidden="true" />
      <span className="connection-status-label">{info.label}</span>
    </div>
  );
};

export default ConnectionStatusIndicator;
