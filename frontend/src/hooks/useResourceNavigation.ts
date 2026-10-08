import { useStore } from '../store';
import { useShallow } from 'zustand/react/shallow';
import {
  kindToResource,
  kindToResourceDef,
  getResourceCategory,
  parseApiVersion,
} from '../utils/resourceUtils';

interface ResourceInfo {
  cluster: string;
  apiVersion: string;
  kind: string;
  name: string;
  namespace?: string;
}

const useResourceNavigation = (cluster: string) => {
  const { openDetailTab, loadDetails } = useStore(useShallow((s) => ({ openDetailTab: s.openDetailTab, loadDetails: s.loadDetails })));

  const navigateToResource = async (info: ResourceInfo) => {
    const { group, version } = parseApiVersion(info.apiVersion);
    const resource = { name: kindToResource(info.kind), group, version, kind: info.kind, namespaced: !!info.namespace };
    const category = getResourceCategory(group, resource.name);
    const item = { name: info.name, namespace: info.namespace, kind: info.kind, apiVersion: info.apiVersion };
    const entry = { type: 'resource', path: `${category}-${group || 'core'}-${version}-${resource.name}`, resource, item };
    try {
      if (useStore.getState().currentTab !== info.cluster) await useStore.getState().openTab(info.cluster);
      await useStore.getState().restoreNavigationState(entry);
      await useStore.getState().recordNavigation(entry.type, entry.path, resource, item);
      return true;
    } catch (error) {
      console.error('Failed to navigate to resource:', error);
      return false;
    }
  };

  const navigateToLink = async (
    kind: string,
    name: string,
    namespace?: string,
    options?: { openInDetailTab?: boolean; isPinned?: boolean; apiVersion?: string },
  ) => {
    let apiVersion = options?.apiVersion;
    let resourceDef = kindToResourceDef(kind);

    if (!apiVersion && !resourceDef) {
      console.error(`Unknown kind: ${kind}`);
      return;
    }

    if (!apiVersion) {
      apiVersion = resourceDef!.group
        ? `${resourceDef!.group}/${resourceDef!.version}`
        : resourceDef!.version;
    }

    const isNamespaced = resourceDef ? resourceDef.namespaced : !!namespace;

    // Always navigate in the main view first
    const navigationSuccessful = await navigateToResource({
      cluster,
      apiVersion,
      kind,
      name,
      namespace: isNamespaced ? namespace : undefined,
    });

    // If openInDetailTab is true, also open in detail tab
    if (options?.openInDetailTab && navigationSuccessful) {
      const { group: apiGroup, version } = parseApiVersion(apiVersion);
      const resourceName = kindToResource(kind);

      const resource = {
        name: resourceName,
        group: apiGroup,
        version: version,
        kind: kind,
        namespaced: !!namespace,
      };

      const item = {
        name: name,
        namespace: namespace,
        uid: `${namespace || 'cluster'}-${name}`,
        kind: kind,
        apiVersion: apiVersion,
      };

      // Immediately open detail tab, then ensure details are loaded
      openDetailTab(resource, item, cluster, options.isPinned || false);
      await loadDetails(cluster, resource, item);
    }
  };

  return { navigateToResource, navigateToLink };
};

export default useResourceNavigation;
