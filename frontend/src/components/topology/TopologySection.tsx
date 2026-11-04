// Inspector section: the resource's immediate neighbourhood as a small,
// live, expandable graph, with a way into the full Topology tab.
import React, { useMemo } from 'react';
import PropertyGroup from '../detailView/shared/PropertyGroup';
import { getResourceIcon } from '../../utils/resourceIcons';
import TopologyView from './TopologyView';
import { openTopology, refFromItem } from './topologyNav';
import type { TopologyMode } from './useTopologyGraph';
import './Topology.css';

interface Props {
  resource: any;
  cluster: string;
}

const TopologySection: React.FC<Props> = ({ resource, cluster }) => {
  const ref = refFromItem(resource);
  const key = ref ? `${ref.kind}/${ref.namespace}/${ref.name}` : '';
  const mode: TopologyMode | null = useMemo(() => (ref ? { kind: 'focus', ref } : null), [key]); // eslint-disable-line react-hooks/exhaustive-deps
  if (!ref) return null;
  return (
    <>
      <PropertyGroup
        title="Topology"
        icon={getResourceIcon('topology')}
        persistKey="topology"
        actions={
          <button
            type="button"
            className="ap-btn ap-btn--ghost topo-section-open"
            onClick={(e) => { e.stopPropagation(); void openTopology(cluster, { focus: ref }); }}
          >
            Open graph
          </button>
        }
      >
        <div className="topo-section">
          <TopologyView key={key} cluster={cluster} mode={mode} compact />
        </div>
      </PropertyGroup>
      <div className="section-divider" />
    </>
  );
};

export default TopologySection;
