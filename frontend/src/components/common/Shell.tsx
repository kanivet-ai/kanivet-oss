import React, { useMemo, useState, useEffect } from 'react';
import Terminal from './Terminal';
import { getBackendOrigin } from '../../services/api/types';

interface ShellProps {
  cluster: string;
  podName?: string;
  nodeName?: string;
  namespace?: string;
  container?: string;
}

const Shell: React.FC<ShellProps> = ({
  cluster,
  podName,
  nodeName,
  namespace,
  container,
}) => {
  const isPod = !!podName;
  const [sessionSecret, setSessionSecret] = useState<string | null>(null);

  useEffect(() => {
    Promise.resolve(
      (window as any).electronAPI?.security?.getSessionSecret?.() || null,
    ).then(setSessionSecret);
  }, []);

  const wsUrl = useMemo(() => {
    if (!sessionSecret) return '';
    const params = new URLSearchParams({
      cluster,
      session_secret: sessionSecret,
      ...(isPod
        ? {
            namespace: namespace!,
            pod: podName,
            ...(container ? { container } : {}),
          }
        : {
            node: nodeName!,
          }),
    });

    const endpoint = isPod ? '/api/v1/ws/exec' : '/api/v1/ws/node-exec';
    return `${getBackendOrigin().replace('http://', 'ws://')}${endpoint}?${params}`;
  }, [cluster, podName, nodeName, namespace, container, isPod, sessionSecret]);

  const headerInfo = useMemo(
    () =>
      isPod
        ? `${namespace}/${podName}${container ? `/${container}` : ''}`
        : `Node: ${nodeName}`,
    [isPod, namespace, podName, container, nodeName],
  );

  const connectMessage = isPod
    ? 'Connecting to pod...'
    : 'Connecting to node...';

  if (!sessionSecret || !wsUrl) {
    return <div className="terminal-loading">Securing connection...</div>;
  }

  return (
    <Terminal
      wsUrl={wsUrl}
      connectMessage={connectMessage}
      headerInfo={headerInfo}
    />
  );
};

export default React.memo(Shell);
