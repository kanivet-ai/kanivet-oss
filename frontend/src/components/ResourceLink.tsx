import React from 'react';
import { useStore } from '../store';
import { useShallow } from 'zustand/react/shallow';
import {
  kindToResource,
  getResourceCategory,
  parseApiVersion,
} from '../utils/resourceUtils';
import './ResourceLink.css';

interface ResourceLinkProps {
  cluster: string;
  apiVersion: string;
  kind: string;
  name: string;
  namespace?: string;
  children?: React.ReactNode;
  className?: string;
  openInDetailTab?: boolean;
}

const ResourceLink: React.FC<ResourceLinkProps> = ({
  cluster,
  apiVersion,
  kind,
  name,
  namespace,
  children,
  className = '',
  openInDetailTab = false,
}) => {
  const {
    selectNode,
    loadListItems,
    selectItem,
    loadDetails,
    recordNavigation,
    setFocusArea,
    openDetailTab,
  } = useStore(useShallow((s) => ({ selectNode: s.selectNode, loadListItems: s.loadListItems, selectItem: s.selectItem, loadDetails: s.loadDetails, recordNavigation: s.recordNavigation, setFocusArea: s.setFocusArea, openDetailTab: s.openDetailTab })));
  const [clickTimer, setClickTimer] = React.useState<NodeJS.Timeout | null>(
    null,
  );

  React.useEffect(() => {
    return () => {
      if (clickTimer) {
        clearTimeout(clickTimer);
      }
    };
  }, [clickTimer]);

  const handleClick = async (e: React.MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();

    // Parse the apiVersion to get group and version
    const { group: apiGroup, version } = parseApiVersion(apiVersion);

    // Convert kind to plural resource name
    const resourceName = kindToResource(kind);

    // Create resource object matching the tree node structure
    const resource = {
      name: resourceName,
      group: apiGroup,
      version: version,
      kind: kind,
      namespaced: !!namespace,
    };

    // Determine which category this resource belongs to
    const categoryId = getResourceCategory(apiGroup, resourceName);

    // Create tree node for selection
    const treeNode = {
      id: `${categoryId}-${apiGroup || 'core'}-${version}-${resourceName}`,
      label: resourceName,
      type: 'resource' as const,
      data: resource,
    };

    if (openInDetailTab) {
      // For detail tab navigation, handle single/double click
      if (clickTimer) {
        // Double click - clear timer and open pinned tab
        clearTimeout(clickTimer);
        setClickTimer(null);

        // Create item object
        const item = {
          name: name,
          namespace: namespace,
          uid: `${namespace || 'cluster'}-${name}`,
          kind: kind,
          apiVersion: apiVersion,
        };

        // Immediately open pinned tab, then load details
        openDetailTab(resource, item, cluster, true);
        await loadDetails(cluster, resource, item);
      } else {
        // Single click - set timer to open preview tab
        const timer = setTimeout(async () => {
          setClickTimer(null);

          // Create item object
          const item = {
            name: name,
            namespace: namespace,
            uid: `${namespace || 'cluster'}-${name}`,
            kind: kind,
            apiVersion: apiVersion,
          };

          // Immediately open preview tab, then load details
          openDetailTab(resource, item, cluster, false);
          await loadDetails(cluster, resource, item);
        }, 200);
        setClickTimer(timer);
      }
    } else {
      // Default behavior - navigate in main view
      try {
        // Select the node in the tree
        selectNode(treeNode);

        // Load the list of items for this resource type
        const success = await loadListItems(cluster, resource);

        if (success) {
          // Create item object for selection
          const item = {
            name: name,
            namespace: namespace,
            uid: `${namespace || 'cluster'}-${name}`,
            kind: kind,
            apiVersion: apiVersion,
          };

          // Select the specific item in the list
          selectItem(item);

          // Load the details for the selected item
          await loadDetails(cluster, resource, item);

          // Record navigation
          await recordNavigation('resource', treeNode.id, resource, item);

          // Switch focus to list view
          setFocusArea('list');
        }
      } catch (error) {
        console.error('Failed to navigate to resource:', error);
      }
    }
  };

  return (
    <span
      className={`resource-link ${className}`}
      onClick={handleClick}
      title={`View ${kind}: ${name}${namespace ? ` in ${namespace}` : ''}`}
    >
      {children || name}
    </span>
  );
};

export default ResourceLink;
