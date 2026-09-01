import React, { useEffect, useState, useRef } from 'react';
import { ClusterSelector } from './ClusterSelector';
import { CloudDiscovery } from './CloudDiscovery';
import { QuickClusterSearch } from './QuickClusterSearch';
import './ClusterSelectorModal.css';

interface ClusterSelectorModalProps {
  isOpen: boolean;
  onClose: () => void;
  onSelectCluster: (cluster: string) => void;
}

type ModalTab = 'find' | 'groups' | 'cloud';

const persistedTab = { value: 'find' as ModalTab };

export const ClusterSelectorModal: React.FC<ClusterSelectorModalProps> = ({
  isOpen,
  onClose,
  onSelectCluster,
}) => {
  const [activeTab, setActiveTabState] = useState<ModalTab>(persistedTab.value);
  const [refreshKey, setRefreshKey] = useState(0);
  const [findKey, setFindKey] = useState(0);
  const wasOpen = useRef(false);

  const setActiveTab = (tab: ModalTab) => {
    persistedTab.value = tab;
    setActiveTabState(tab);
  };

  useEffect(() => {
    if (isOpen && !wasOpen.current) {
      wasOpen.current = true;
      setActiveTabState('find');
      setFindKey(k => k + 1);
    } else if (!isOpen && wasOpen.current) {
      wasOpen.current = false;
    }
  }, [isOpen]);

  useEffect(() => {
    if (isOpen) {
      document.body.style.overflow = 'hidden';
      const handleKeyDown = (e: KeyboardEvent) => {
        if (e.key === 'Escape' && activeTab !== 'find') {
          onClose();
        }
      };
      document.addEventListener('keydown', handleKeyDown);
      return () => {
        document.body.style.overflow = '';
        document.removeEventListener('keydown', handleKeyDown);
      };
    }
  }, [isOpen, activeTab, onClose]);

  if (!isOpen) return null;

  const handleOverlayClick = (e: React.MouseEvent) => {
    if (e.target === e.currentTarget) {
      onClose();
    }
  };

  const handleSelectCluster = (cluster: string) => {
    onSelectCluster(cluster);
    onClose();
  };

  const handleClusterImported = () => {
    setRefreshKey(k => k + 1);
    setActiveTab('groups');
  };

  return (
    <div className="cluster-selector-overlay" onClick={handleOverlayClick}>
      <div className={`cluster-selector-modal ${activeTab === 'find' ? 'find-mode' : ''}`}>
        <div className="cluster-selector-modal-header">
          <div className="cluster-selector-modal-tabs">
            <button
              className={`cluster-selector-modal-tab ${activeTab === 'find' ? 'active' : ''}`}
              onClick={() => setActiveTab('find')}
            >
              Quick Find
            </button>
            <button
              className={`cluster-selector-modal-tab ${activeTab === 'groups' ? 'active' : ''}`}
              onClick={() => setActiveTab('groups')}
            >
              Manage Groups
            </button>
            <button
              className={`cluster-selector-modal-tab ${activeTab === 'cloud' ? 'active' : ''}`}
              onClick={() => setActiveTab('cloud')}
            >
              Cloud Discovery
            </button>
          </div>
          <button
            className="cluster-selector-modal-close"
            onClick={onClose}
            aria-label="Close"
          >
            <svg width="24" height="24" viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg">
              <path d="M18 6L6 18M6 6l12 12" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
            </svg>
          </button>
        </div>
        <div className="cluster-selector-modal-body">
          {activeTab === 'find' && (
            <QuickClusterSearch key={findKey} onSelectCluster={handleSelectCluster} onClose={onClose} />
          )}
          {activeTab === 'groups' && (
            <ClusterSelector key={refreshKey} onSelectCluster={handleSelectCluster} isMainView={true} />
          )}
          {activeTab === 'cloud' && (
            <CloudDiscovery onClusterImported={handleClusterImported} />
          )}
        </div>
      </div>
    </div>
  );
};
