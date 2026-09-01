import React from 'react';
import { BellIcon, Link2Icon } from '@radix-ui/react-icons';
import PropertyRow from '../../common/PropertyRow';
import PropertyGroup from '../shared/PropertyGroup';
import TimestampValue from '../../common/TimestampValue';
import ClipboardCopy from '../../common/ClipboardCopy';
import { DetailViewProps } from '../../../types/detailView';
import './EventDetailView.css';

const EventDetailView: React.FC<DetailViewProps> = ({ resource, handleResourceClick }) => {
  const { metadata = {}, involvedObject = {}, source = {} } = resource;

  return (
    <>
      <PropertyGroup title="Event" icon={<BellIcon />} defaultOpen>
        <PropertyRow
          label="Type"
          value={<span className={`event-type-badge event-type-${resource.type?.toLowerCase()}`}>{resource.type}</span>}
        />
        <PropertyRow label="Reason" value={resource.reason} copyText={resource.reason} />
        <PropertyRow label="Count" value={resource.count || 1} />
        <PropertyRow label="First Seen" value={<TimestampValue timestamp={resource.firstTimestamp} suffix="ago" />} />
        <PropertyRow label="Last Seen" value={<TimestampValue timestamp={resource.lastTimestamp || resource.eventTime} suffix="ago" />} />
        {source.component && <PropertyRow label="Source" value={`${source.component}${source.host ? ` on ${source.host}` : ''}`} muted />}
      </PropertyGroup>
      <div className="section-divider" />

      <PropertyGroup title="Message" defaultOpen>
        <div className="event-message-box">
          <pre>{resource.message}</pre>
          <ClipboardCopy text={resource.message} alwaysShow />
        </div>
      </PropertyGroup>
      <div className="section-divider" />

      <PropertyGroup title="Involved Object" icon={<Link2Icon />} defaultOpen>
        <PropertyRow
          label="Name"
          value={
            handleResourceClick ? (
              <button className="link-button" onClick={(e) => handleResourceClick(involvedObject.kind, involvedObject.name, involvedObject.namespace || metadata.namespace, e)}>
                {involvedObject.name}
              </button>
            ) : involvedObject.name
          }
          copyText={involvedObject.name}
        />
        <PropertyRow label="Kind" value={involvedObject.kind} />
        <PropertyRow
          label="Namespace"
          value={
            handleResourceClick ? (
              <button className="link-button" onClick={(e) => handleResourceClick('Namespace', involvedObject.namespace || metadata.namespace, undefined, e)}>
                {involvedObject.namespace || metadata.namespace}
              </button>
            ) : (involvedObject.namespace || metadata.namespace)
          }
        />
        {involvedObject.fieldPath && <PropertyRow label="Field Path" value={involvedObject.fieldPath} mono muted />}
        {involvedObject.apiVersion && <PropertyRow label="API Version" value={involvedObject.apiVersion} muted />}
      </PropertyGroup>
    </>
  );
};

export default EventDetailView;
