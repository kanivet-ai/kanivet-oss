import React, { useState, useEffect } from 'react';
import Shell from './common/Shell';
import ContainerSelector from './ContainerSelector';
import { useStore } from '../store';
import './PodShell.css';

interface Container {
  name: string;
  image?: string;
}

interface ContainerStatus {
  name: string;
  ready?: boolean;
  started?: boolean;
  state?: {
    running?: any;
    waiting?: any;
    terminated?: any;
  };
}

interface PodShellProps {
  cluster: string;
  namespace: string;
  podName: string;
  containers?: Container[];
  initContainers?: Container[];
  containerStatuses?: ContainerStatus[];
  initContainerStatuses?: ContainerStatus[];
  tabId?: string;
  initialContainer?: string;
}

const PodShell: React.FC<PodShellProps> = ({
  cluster,
  namespace,
  podName,
  containers = [],
  initContainers = [],
  containerStatuses = [],
  initContainerStatuses = [],
  tabId,
  initialContainer,
}) => {
  const { updateBottomTabContainer } = useStore();
  const [selectedContainer, setSelectedContainer] = useState<
    string | undefined
  >(initialContainer);
  const [containerError, setContainerError] = useState<string | null>(null);
  const [isInitialized, setIsInitialized] = useState(false);

  // Update selected container when initialContainer prop changes
  useEffect(() => {
    if (initialContainer && initialContainer !== selectedContainer) {
      setSelectedContainer(initialContainer);
    }
  }, [initialContainer, selectedContainer]);

  useEffect(() => {
    if (isInitialized || initialContainer) return;

    const allContainers = [...containers, ...initContainers];

    if (allContainers.length === 0) {
      setContainerError('No containers available in this pod');
      setSelectedContainer(undefined);
    } else if (allContainers.length === 1) {
      const containerName = allContainers[0].name;
      setSelectedContainer(containerName);
      setContainerError(null);
      if (tabId) {
        updateBottomTabContainer(tabId, containerName);
      }
    } else {
      const firstContainer =
        containers.length > 0 ? containers[0] : initContainers[0];
      if (firstContainer) {
        setSelectedContainer(firstContainer.name);
        if (tabId) {
          updateBottomTabContainer(tabId, firstContainer.name);
        }
      }
      setContainerError(null);
    }

    setIsInitialized(true);
  }, [
    containers,
    initContainers,
    isInitialized,
    initialContainer,
    tabId,
    updateBottomTabContainer,
  ]);

  const handleContainerSelect = (containerName: string) => {
    setSelectedContainer(containerName);
    if (tabId) {
      updateBottomTabContainer(tabId, containerName);
    }
  };

  const getContainerStatus = (
    containerName: string,
    isInit: boolean = false,
  ) => {
    const statuses = isInit ? initContainerStatuses : containerStatuses;
    return statuses.find((s) => s.name === containerName);
  };

  const isContainerRunning = (
    containerName: string,
    isInit: boolean = false,
  ) => {
    const status = getContainerStatus(containerName, isInit);
    return status?.state?.running !== undefined;
  };

  const enrichedContainers = containers.map((c) => ({
    ...c,
    isRunning: isContainerRunning(c.name, false),
    status: getContainerStatus(c.name, false),
  }));

  const enrichedInitContainers = initContainers.map((c) => ({
    ...c,
    isRunning: isContainerRunning(c.name, true),
    status: getContainerStatus(c.name, true),
  }));

  if (containerError) {
    return (
      <div className="shell-container-error">
        <div className="shell-error-message">{containerError}</div>
      </div>
    );
  }

  const allContainersCount =
    enrichedContainers.length + enrichedInitContainers.length;
  const showContainerDropdown = allContainersCount > 1;

  return (
    <div className="pod-shell-container">
      {showContainerDropdown && (
        <div className="shell-header">
          <span className="shell-header-label">Container:</span>
          <ContainerSelector
            containers={enrichedContainers}
            initContainers={enrichedInitContainers}
            selectedContainer={selectedContainer}
            onSelectContainer={handleContainerSelect}
          />
        </div>
      )}
      {selectedContainer ? (
        <Shell
          key={`${cluster}-${namespace}-${podName}-${selectedContainer}`}
          cluster={cluster}
          namespace={namespace}
          podName={podName}
          container={selectedContainer}
        />
      ) : (
        <div className="shell-container-error">
          <div className="shell-error-message">
            No running containers available to attach shell
          </div>
        </div>
      )}
    </div>
  );
};

export default React.memo(PodShell);
