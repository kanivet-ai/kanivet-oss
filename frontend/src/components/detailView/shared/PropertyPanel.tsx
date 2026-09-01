import React from 'react';
import * as ScrollArea from '@radix-ui/react-scroll-area';
import './PropertyPanel.css';

interface PropertyPanelProps {
  children: React.ReactNode;
  className?: string;
}

const PropertyPanel: React.FC<PropertyPanelProps> = ({ children, className = '' }) => {
  return (
    <ScrollArea.Root className={`property-panel ${className}`}>
      <ScrollArea.Viewport className="property-panel-viewport">
        <div className="property-panel-content">{children}</div>
      </ScrollArea.Viewport>
      <ScrollArea.Scrollbar className="property-panel-scrollbar" orientation="vertical">
        <ScrollArea.Thumb className="property-panel-thumb" />
      </ScrollArea.Scrollbar>
    </ScrollArea.Root>
  );
};

export default PropertyPanel;
