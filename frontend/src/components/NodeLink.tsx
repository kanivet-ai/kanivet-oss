import React from 'react';
import NavigationLink from './common/NavigationLink';
import { useStore } from '../store';
import './NodeLink.css';

interface NodeLinkProps {
  nodeName: string;
}

const NodeLink: React.FC<NodeLinkProps> = ({ nodeName }) => {
  const { currentTab } = useStore();

  if (!nodeName || nodeName === '-') {
    return <span>-</span>;
  }

  const nodeResource = {
    name: 'nodes',
    group: '',
    version: 'v1',
    kind: 'nodes',
    namespaced: false,
  };

  return (
    <NavigationLink
      target={{
        resource: nodeResource,
        targetName: nodeName,
        nodeId: `${currentTab}//v1/nodes`,
      }}
      className="node-link"
      title={`Navigate to node ${nodeName}`}
    >
      {nodeName}
    </NavigationLink>
  );
};

export default NodeLink;
