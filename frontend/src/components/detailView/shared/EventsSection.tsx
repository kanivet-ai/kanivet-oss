import React from 'react';
import { ActivityLogIcon } from '@radix-ui/react-icons';
import PropertyGroup from './PropertyGroup';
import ClipboardCopy from '../../common/ClipboardCopy';
import { formatAge } from '../../../utils/formatters';
import './EventsSection.css';

interface EventsSectionProps {
  events: any[];
  title?: string;
}

const EventsSection: React.FC<EventsSectionProps> = ({ events, title = 'Events' }) => {
  if (!events || events.length === 0) return null;

  return (
    <PropertyGroup title={title} count={events.length} icon={<ActivityLogIcon />} defaultOpen>
      <div className="events-list">
        {events.map((event: any, index: number) => (
          <div key={index} className={`event-item event-${event.type?.toLowerCase()}`}>
            <div className="event-header">
              <span className={`event-type-badge event-type-${event.type?.toLowerCase()}`}>{event.type}</span>
              <span className="event-reason">{event.reason}</span>
              <span className="event-time">{formatAge(event.lastTimestamp || event.eventTime)}</span>
              <ClipboardCopy text={`${event.type} - ${event.reason}: ${event.message}`} />
            </div>
            <div className="event-message">{event.message}</div>
          </div>
        ))}
      </div>
    </PropertyGroup>
  );
};

export default EventsSection;
