import { useStore } from '../store';
import { useShallow } from 'zustand/react/shallow';
import './ConnectionStatusIndicator.css';

const ConnectionStatusIndicator = () => {
  const { backendState, websocketState, getOverallState, reconnectCountdown } = useStore(useShallow((s) => ({ backendState: s.backendState, websocketState: s.websocketState, getOverallState: s.getOverallState, reconnectCountdown: s.reconnectCountdown })));
  const overallState = getOverallState();

  if (overallState === 'connected') return null;

  const getStatusInfo = () => {
    if (backendState === 'disconnected') {
      return { icon: '●', label: 'Backend offline', className: 'disconnected' };
    }
    if (websocketState === 'disconnected') {
      return { icon: '●', label: 'Disconnected', className: 'disconnected' };
    }
    if (overallState === 'reconnecting') {
      const countdownText = reconnectCountdown ? ` (${reconnectCountdown}s)` : '';
      return { icon: '↻', label: `Reconnecting${countdownText}`, className: 'reconnecting' };
    }
    if (overallState === 'connecting') {
      return { icon: '◐', label: 'Connecting', className: 'connecting' };
    }
    return null;
  };

  const info = getStatusInfo();
  if (!info) return null;

  return (
    <div className={`connection-status-indicator ${info.className}`} title={`Backend: ${backendState}, WebSocket: ${websocketState}`}>
      <span className="connection-status-icon">{info.icon}</span>
      <span className="connection-status-label">{info.label}</span>
    </div>
  );
};

export default ConnectionStatusIndicator;
