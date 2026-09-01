import React, { useState, useEffect, useRef } from 'react';
import { ChevronDownIcon, MagnifyingGlassIcon } from '@radix-ui/react-icons';
import api from '../services/api';
import './ResourceSelector.css';

interface Resource {
  group: string;
  version: string;
  kind: string;
  namespaced: boolean;
  name: string;
  verbs: string[];
}

interface ResourceSelectorProps {
  cluster: string;
  onResourceSelect: (template: string) => void;
}

const ResourceSelector: React.FC<ResourceSelectorProps> = ({ cluster, onResourceSelect }) => {
  const [resources, setResources] = useState<Resource[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [isOpen, setIsOpen] = useState(false);
  const [searchQuery, setSearchQuery] = useState('');
  const [selectedResource, setSelectedResource] = useState<Resource | null>(null);
  const [fetchingTemplate, setFetchingTemplate] = useState(false);
  const dropdownRef = useRef<HTMLDivElement>(null);
  const searchInputRef = useRef<HTMLInputElement>(null);

  // Fetch available resources when component mounts or cluster changes
  useEffect(() => {
    const fetchResources = async () => {
      setLoading(true);
      setError(null);
      try {
        // Get all API resources for the cluster
        const apiResourcesData = await api.request('/cluster/api-resources', { cluster }, false);
        
        const allResources: Resource[] = [];
        
        // Process API resources
        if (apiResourcesData && Array.isArray(apiResourcesData)) {
          apiResourcesData.forEach((res: any) => {
            // Skip if it doesn't support create
            if (!res.verbs || !res.verbs.includes('create')) {
              return;
            }
            
            // Parse group/version from apiVersion
            let group = '';
            let version = res.version || 'v1';
            
            if (res.apiVersion && res.apiVersion.includes('/')) {
              const parts = res.apiVersion.split('/');
              group = parts[0];
              version = parts[1];
            }
            
            // Extract kind from name (capitalize first letter)
            const resourceKind = res.kind || res.name.charAt(0).toUpperCase() + res.name.slice(1).replace(/s$/, '');
            
            allResources.push({
              group: res.group || group || '',
              version: res.version || version,
              kind: resourceKind,
              namespaced: res.namespaced !== false,
              name: res.name,
              verbs: res.verbs,
            });
          });
        }

        // Sort resources by kind
        allResources.sort((a, b) => a.kind.localeCompare(b.kind));
        
        setResources(allResources);
      } catch (err: any) {
        setError(err.message || 'Failed to fetch resources');
        console.error('Failed to fetch resources:', err);
      } finally {
        setLoading(false);
      }
    };

    if (cluster) {
      fetchResources();
    }
  }, [cluster]);

  // Handle click outside to close dropdown
  useEffect(() => {
    const handleClickOutside = (event: MouseEvent) => {
      if (dropdownRef.current && !dropdownRef.current.contains(event.target as Node)) {
        setIsOpen(false);
      }
    };

    document.addEventListener('mousedown', handleClickOutside);
    return () => document.removeEventListener('mousedown', handleClickOutside);
  }, []);

  // Focus search input when dropdown opens
  useEffect(() => {
    if (isOpen && searchInputRef.current) {
      searchInputRef.current.focus();
    }
  }, [isOpen]);

  const handleResourceSelect = async (resource: Resource) => {
    setIsOpen(false);
    setFetchingTemplate(true);
    
    try {
      const { template } = await api.getResourceSchema(
        cluster,
        resource.group,
        resource.version,
        resource.kind
      );
      
      setSelectedResource(resource);
      onResourceSelect(template);
    } catch (err: any) {
      console.error('Failed to fetch resource template:', err);
      // Fall back to basic template
      const basicTemplate = `apiVersion: ${resource.group ? `${resource.group}/` : ''}${resource.version}
kind: ${resource.kind}
metadata:
  name: <Fill here>
  ${resource.namespaced ? 'namespace: default' : ''}
spec:
  <Fill here>`;
      
      setSelectedResource(resource);
      onResourceSelect(basicTemplate);
    } finally {
      setFetchingTemplate(false);
    }
  };

  const filteredResources = resources.filter(resource => {
    const query = searchQuery.toLowerCase();
    return (
      resource.kind.toLowerCase().includes(query) ||
      resource.group.toLowerCase().includes(query) ||
      `${resource.group}/${resource.version}`.toLowerCase().includes(query)
    );
  });

  const getResourceDisplay = (resource: Resource) => {
    if (resource.group) {
      // Shorten long group names
      const groupParts = resource.group.split('.');
      let shortGroup = resource.group;
      if (groupParts.length > 2) {
        // Keep first part and TLD (e.g., "iam.aws.upbound.io" -> "iam...io")
        shortGroup = `${groupParts[0]}...${groupParts[groupParts.length - 1]}`;
      }
      return {
        main: resource.kind,
        sub: `${shortGroup}/${resource.version}`,
      };
    }
    return {
      main: resource.kind,
      sub: resource.version,
    };
  };

  if (loading) {
    return (
      <div className="resource-selector loading">
        <span>Loading resources...</span>
      </div>
    );
  }

  if (error) {
    return (
      <div className="resource-selector error">
        <span>Error: {error}</span>
      </div>
    );
  }

  return (
    <div className="resource-selector" ref={dropdownRef}>
      <button
        className="resource-selector-trigger"
        onClick={() => setIsOpen(!isOpen)}
        disabled={fetchingTemplate}
      >
        <span className="trigger-content">
          {fetchingTemplate ? (
            <span className="fetching-text">Loading template...</span>
          ) : selectedResource ? (
            <>
              <span className="selected-kind">{selectedResource.kind}</span>
              <span className="selected-api">
                {selectedResource.group ? `${selectedResource.group}/${selectedResource.version}` : selectedResource.version}
              </span>
            </>
          ) : (
            <span>Select resource type</span>
          )}
        </span>
        <ChevronDownIcon className={`chevron ${isOpen ? 'open' : ''}`} />
      </button>

      {isOpen && (
        <div className="resource-selector-dropdown">
          <div className="dropdown-search">
            <MagnifyingGlassIcon className="search-icon" />
            <input
              ref={searchInputRef}
              type="text"
              placeholder="Search resources..."
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              className="search-input"
            />
          </div>
          
          <div className="dropdown-list">
            {filteredResources.length === 0 ? (
              <div className="no-results">No matching resources found</div>
            ) : (
              filteredResources.map((resource) => {
                const display = getResourceDisplay(resource);
                return (
                  <button
                    key={`${resource.group}-${resource.version}-${resource.kind}`}
                    className="resource-item"
                    onClick={() => handleResourceSelect(resource)}
                    title={resource.group ? `${resource.kind} - ${resource.group}/${resource.version}` : `${resource.kind} - ${resource.version}`}
                  >
                    <span className="resource-kind">{display.main}</span>
                    <span className="resource-api">{display.sub}</span>
                  </button>
                );
              })
            )}
          </div>
        </div>
      )}
    </div>
  );
};

export default ResourceSelector;
