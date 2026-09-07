import React from 'react';
import { GearIcon, Link2Icon, CheckCircledIcon } from '@radix-ui/react-icons';
import PropertyRow from '../../common/PropertyRow';
import PropertyGroup from '../shared/PropertyGroup';
import ConditionsView from '../shared/ConditionsView';
import MetadataSection from '../shared/MetadataSection';
import SpecificationSection from '../shared/SpecificationSection';
import EventsSection from '../shared/EventsSection';
import ClipboardCopy from '../../common/ClipboardCopy';
import { useStore } from '../../../store';
import { parseApiVersion, kindToResource } from '../../../utils/resourceUtils';
import { DetailViewPropsWithCluster } from '../../../types/detailView';
import './CustomResourceDetailView.css';

const CustomResourceDetailView: React.FC<DetailViewPropsWithCluster> = ({ resource, cluster, handleResourceClick }) => {
  const loadDetails = useStore((s) => s.loadDetails);
  const setState = useStore.setState;

  const { metadata = {}, spec = {}, status = {} } = resource;

  const handleResourceRefClick = async (ref: any, e: React.MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
    const { group: apiGroup, version } = parseApiVersion(ref.apiVersion);
    const resourceName = kindToResource(ref.kind);
    const resourceDef = { name: resourceName, group: apiGroup, version, kind: ref.kind, namespaced: !!ref.namespace || !!metadata.namespace };
    const item = { name: ref.name, namespace: ref.namespace || metadata.namespace, uid: `${ref.namespace || metadata.namespace || 'cluster'}-${ref.name}`, kind: ref.kind, apiVersion: ref.apiVersion };
    const tab = useStore.getState().activeTabs.find((t) => t.id === cluster);
    if (!tab) return;
    const title = item.namespace ? `${item.name} (${item.namespace})` : item.name;
    const existing = tab.state.detailTabs.find((dt: any) => (dt.item.metadata?.name || dt.item.name) === item.name && (dt.item.metadata?.namespace || dt.item.namespace) === item.namespace && dt.cluster === cluster && !dt.isPinned);
    if (!existing) {
      const newTab = { id: `detail-${cluster}-${Date.now()}-${Math.random()}`, title, resource: resourceDef, item, cluster, isPinned: false, location: 'detail' as const };
      setState((state) => ({ activeTabs: state.activeTabs.map((t) => t.id === cluster ? { ...t, state: { ...t.state, detailTabs: [...t.state.detailTabs, newTab], isDetailsPanelCollapsed: false } } : t) }));
    }
    await loadDetails(cluster, resourceDef, item);
  };

  const filteredSpec = spec ? Object.fromEntries(Object.entries(spec).filter(([k]) => k !== 'resourceRefs' && k !== 'parameters')) : {};

  return (
    <>
      <MetadataSection metadata={metadata} handleResourceClick={handleResourceClick}>
        {metadata.generation && <PropertyRow label="Generation" value={metadata.generation} muted />}
        {metadata.resourceVersion && <PropertyRow label="Version" value={metadata.resourceVersion} muted mono />}
      </MetadataSection>
      <div className="section-divider" />

      {spec?.parameters && Object.keys(spec.parameters).length > 0 && (
        <>
          <PropertyGroup title="Parameters" icon={<GearIcon />} count={Object.keys(spec.parameters).length} defaultOpen>
            {Object.entries(spec.parameters).map(([key, value]) => (
              <PropertyRow key={key} label={key} value={typeof value === 'object' ? JSON.stringify(value) : String(value)} copyText={typeof value === 'object' ? JSON.stringify(value) : String(value)} />
            ))}
          </PropertyGroup>
          <div className="section-divider" />
        </>
      )}

      {Object.keys(filteredSpec).length > 0 && (
        <>
          <SpecificationSection spec={filteredSpec} />
          <div className="section-divider" />
        </>
      )}

      {spec?.resourceRefs?.length > 0 && (
        <>
          <PropertyGroup title="References" icon={<Link2Icon />} count={spec.resourceRefs.length} defaultOpen={false}>
            <div className="cr-refs">
              {spec.resourceRefs.map((ref: any, i: number) => (
                <div key={i} className="cr-ref-item">
                  <span className="cr-ref-kind">{ref.kind}</span>
                  <button className="link-button" onClick={(e) => handleResourceRefClick(ref, e)}>{ref.name}</button>
                  <ClipboardCopy text={ref.name} />
                </div>
              ))}
            </div>
          </PropertyGroup>
          <div className="section-divider" />
        </>
      )}

      {status?.conditions && (
        <>
          <PropertyGroup title="Conditions" icon={<CheckCircledIcon />} count={status.conditions.length} defaultOpen>
            <ConditionsView conditions={status.conditions} />
          </PropertyGroup>
          <div className="section-divider" />
        </>
      )}

      <EventsSection events={resource.events} />
    </>
  );
};

export default CustomResourceDetailView;
