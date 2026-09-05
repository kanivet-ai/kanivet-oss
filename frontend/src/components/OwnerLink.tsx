import React from 'react';
import NavigationLink from './common/NavigationLink';
import { useStore } from '../store';
import { useShallow } from 'zustand/react/shallow';
import {
  pluralizeKind,
  getGroupForKind,
  getVersionForKind,
  isNamespacedKind,
  isNavigableKind,
} from '../utils/ownerUtils';
import './OwnerLink.css';

interface OwnerReference {
  kind: string;
  name: string;
  apiVersion?: string;
}

interface OwnerLinkProps {
  ownerReferences: OwnerReference[];
}

const OwnerLink: React.FC<OwnerLinkProps> = ({ ownerReferences }) => {
  const { currentTab } = useStore(useShallow((s) => ({ currentTab: s.currentTab })));

  if (
    !ownerReferences ||
    !Array.isArray(ownerReferences) ||
    ownerReferences.length === 0
  ) {
    return <span>-</span>;
  }

  const owner = ownerReferences[0];
  const isNavigable = isNavigableKind(owner.kind);

  if (!isNavigable) {
    return (
      <span className="owner-text">
        {owner.kind}/{owner.name}
      </span>
    );
  }

  const ownerResource = {
    name: pluralizeKind(owner.kind.toLowerCase()),
    group: getGroupForKind(owner.kind),
    version: getVersionForKind(owner.kind),
    kind: pluralizeKind(owner.kind.toLowerCase()),
    namespaced: isNamespacedKind(owner.kind),
  };

  return (
    <NavigationLink
      target={{
        resource: ownerResource,
        targetName: owner.name,
        targetNamespace: ownerResource.namespaced ? 'all' : undefined,
        nodeId: `${currentTab}/${ownerResource.group}/${ownerResource.version}/${ownerResource.name}`,
      }}
      className="owner-link"
      title={`Navigate to ${owner.kind}/${owner.name}`}
    >
      {owner.kind}/{owner.name}
    </NavigationLink>
  );
};

export default OwnerLink;
