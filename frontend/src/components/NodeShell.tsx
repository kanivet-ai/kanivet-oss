import React from 'react';
import Shell from './common/Shell';

interface NodeShellProps {
  cluster: string;
  nodeName: string;
}

const NodeShell: React.FC<NodeShellProps> = ({ cluster, nodeName }) => {
  return <Shell cluster={cluster} nodeName={nodeName} />;
};

export default React.memo(NodeShell);
