import React, { useState } from 'react';
import { LockClosedIcon, MagnifyingGlassIcon, LayersIcon, ChevronDownIcon } from '@radix-ui/react-icons';
import PropertyRow from '../../common/PropertyRow';
import PropertyGroup from '../shared/PropertyGroup';
import MetadataSection from '../shared/MetadataSection';
import EventsSection from '../shared/EventsSection';
import { DetailViewProps } from '../../../types/detailView';
import './RoleDetailView.css';

const VERB_CLASSES: Record<string, string> = {
  get: 'read', list: 'read', watch: 'read',
  create: 'write', update: 'write', patch: 'write',
  delete: 'delete', deletecollection: 'delete',
  '*': 'all'
};

const RoleDetailView: React.FC<DetailViewProps> = ({ resource, handleResourceClick }) => {
  const { metadata = {}, aggregationRule } = resource;
  const rules = resource.rules || [];
  const [searchTerm, setSearchTerm] = useState('');
  const [showGrouped, setShowGrouped] = useState(true);

  const isClusterRole = resource.kind === 'ClusterRole';

  const groupRulesByResource = () => {
    const grouped: Record<string, Set<string>> = {};
    rules.forEach((rule: any) => {
      const apiGroups = rule.apiGroups || [''];
      const resources = rule.resources || [];
      const verbs = rule.verbs || [];
      const nonResourceURLs = rule.nonResourceURLs || [];

      resources.forEach((res: string) => {
        apiGroups.forEach((group: string) => {
          const key = group ? `${res}.${group}` : res;
          if (!grouped[key]) grouped[key] = new Set();
          verbs.forEach((v: string) => grouped[key].add(v));
        });
      });

      nonResourceURLs.forEach((url: string) => {
        const key = `URL: ${url}`;
        if (!grouped[key]) grouped[key] = new Set();
        verbs.forEach((v: string) => grouped[key].add(v));
      });
    });
    return grouped;
  };

  const filterMatches = (text: string) => !searchTerm || text.toLowerCase().includes(searchTerm.toLowerCase());
  const grouped = groupRulesByResource();
  const filteredKeys = Object.keys(grouped).filter(filterMatches).sort();

  return (
    <>
      <PropertyGroup title="Role Info" icon={<LockClosedIcon />} defaultOpen>
        <PropertyRow label="Scope" value={isClusterRole ? 'Cluster' : 'Namespace'} />
        <PropertyRow label="Rules" value={rules.length} />
        {aggregationRule && (
          <>
            <PropertyRow label="Type" value={<span className="role-type-badge">Aggregated</span>} />
            <PropertyRow label="Selectors" value={aggregationRule.clusterRoleSelectors?.length || 0} />
          </>
        )}
      </PropertyGroup>
      <div className="section-divider" />

      <MetadataSection metadata={metadata} handleResourceClick={handleResourceClick} showNamespace={!isClusterRole} />
      <div className="section-divider" />

      {aggregationRule?.clusterRoleSelectors && (
        <>
          <PropertyGroup title="Aggregation" icon={<LayersIcon />} defaultOpen={false}>
            <div className="aggregation-selectors">
              {aggregationRule.clusterRoleSelectors.map((sel: any, i: number) => (
                <div key={i} className="aggregation-selector">
                  {sel.matchLabels && Object.entries(sel.matchLabels).map(([k, v]) => (
                    <span key={k} className="selector-label">{k}={v as string}</span>
                  ))}
                </div>
              ))}
            </div>
          </PropertyGroup>
          <div className="section-divider" />
        </>
      )}

      {rules.length > 0 && (
        <>
          <PropertyGroup
            title="Permissions"
            count={filteredKeys.length}
            defaultOpen
            actions={
              <div className="role-controls">
                <button
                  className={`role-view-toggle${showGrouped ? ' active' : ''}`}
                  onClick={() => setShowGrouped(!showGrouped)}
                  title={showGrouped ? 'Show as rules' : 'Group by resource'}
                >
                  {showGrouped ? <LayersIcon /> : <ChevronDownIcon />}
                </button>
              </div>
            }
          >
            <div className="role-search">
              <MagnifyingGlassIcon />
              <input
                type="text"
                placeholder="Filter permissions..."
                value={searchTerm}
                onChange={(e) => setSearchTerm(e.target.value)}
              />
            </div>

            {showGrouped ? (
              <div className="permissions-list">
                {filteredKeys.length === 0 && searchTerm ? (
                  <div className="no-results">No matching permissions</div>
                ) : (
                  filteredKeys.map(key => {
                    const verbs = Array.from(grouped[key]).sort();
                    return (
                      <div key={key} className="permission-item">
                        <span className="permission-resource">{key}</span>
                        <div className="permission-verbs">
                          {verbs.map((verb, i) => (
                            <span key={i} className={`verb-badge verb-${VERB_CLASSES[verb] || 'other'}`}>
                              {verb === '*' ? 'all' : verb}
                            </span>
                          ))}
                        </div>
                      </div>
                    );
                  })
                )}
              </div>
            ) : (
              <div className="rules-list">
                {rules.map((rule: any, i: number) => {
                  const resources = rule.resources || [];
                  const verbs = rule.verbs || [];
                  const apiGroups = rule.apiGroups || [''];
                  if (searchTerm && !resources.some((r: string) => filterMatches(r)) && !verbs.some((v: string) => filterMatches(v))) return null;
                  return (
                    <div key={i} className="rule-item">
                      <div className="rule-header">
                        <span className="rule-number">{i + 1}</span>
                        <span className="rule-resources">{resources.join(', ') || 'URLs'}</span>
                        <div className="rule-verbs">
                          {verbs.map((verb: string, j: number) => (
                            <span key={j} className={`verb-badge verb-${VERB_CLASSES[verb] || 'other'}`}>
                              {verb === '*' ? 'all' : verb}
                            </span>
                          ))}
                        </div>
                      </div>
                      {apiGroups[0] && <div className="rule-api-group">API: {apiGroups.join(', ')}</div>}
                    </div>
                  );
                })}
              </div>
            )}
          </PropertyGroup>
          <div className="section-divider" />
        </>
      )}

      <EventsSection events={resource.events} />
    </>
  );
};

export default RoleDetailView;
