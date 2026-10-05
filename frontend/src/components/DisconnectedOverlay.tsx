import { useStore } from '../store';
import { useShallow } from 'zustand/react/shallow';
import './DisconnectedOverlay.css';

interface DisconnectedOverlayProps {
  cluster?: string;
  lastUpdate?: number;
}

const DisconnectedOverlay = (_props: DisconnectedOverlayProps) => {
  const { backendState, websocketState, getOverallState } = useStore(useShallow((s) => ({ backendState: s.backendState, websocketState: s.websocketState, getOverallState: s.getOverallState })));
  const overallState = getOverallState();

  // A down socket is shown whatever the cluster reports: its error is stale
  // while updates cannot arrive.
  if (overallState === 'connected') return null;

  const getOverlayContent = () => {
    if (backendState === 'disconnected') {
      return {
        title: 'Backend Offline',
        message: 'Unable to connect to Kanivet backend. Displayed data may be stale.',
        icon: '⚠',
        showSpinner: false,
      };
    }
    if (websocketState === 'disconnected') {
      return {
        title: 'Connection Lost',
        message: 'Real-time updates paused. Reconnecting...',
        icon: '↻',
        showSpinner: true,
      };
    }
    if (websocketState === 'reconnecting' || websocketState === 'connecting') {
      return {
        title: 'Reconnecting',
        message: 'Restoring connection to server...',
        icon: '↻',
        showSpinner: true,
      };
    }
    return null;
  };

  const content = getOverlayContent();
  if (!content) return null;

  return (
    <div className="disconnected-overlay">
      <div className="disconnected-overlay-content">
        <div className={`disconnected-overlay-icon ${content.showSpinner ? 'spinning' : ''}`}>
          {content.icon}
        </div>
        <div className="disconnected-overlay-title">{content.title}</div>
        <div className="disconnected-overlay-message">{content.message}</div>
      </div>
    </div>
  );
};

export default DisconnectedOverlay;
