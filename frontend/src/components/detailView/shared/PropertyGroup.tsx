import React, { useCallback, useEffect, useState } from 'react';
import * as Collapsible from '@radix-ui/react-collapsible';
import { ChevronRightIcon } from '@radix-ui/react-icons';
import './PropertyGroup.css';

interface PropertyGroupProps {
  title: string;
  icon?: React.ReactNode;
  count?: number;
  defaultOpen?: boolean;
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  muted?: boolean;
  children: React.ReactNode;
  className?: string;
  actions?: React.ReactNode;
  persistKey?: string;
}

const PropertyGroup: React.FC<PropertyGroupProps> = ({
  title,
  icon,
  count,
  defaultOpen = true,
  open,
  onOpenChange,
  muted,
  children,
  className = '',
  actions,
  persistKey,
}) => {
  const isControlled = open !== undefined;
  const storageKey = persistKey ? `kanivet:property-group:${persistKey}:open` : null;
  const getInitialOpen = useCallback(() => {
    if (!storageKey || typeof window === 'undefined') return defaultOpen;
    try {
      const stored = window.localStorage.getItem(storageKey);
      return stored === null ? defaultOpen : stored === 'true';
    } catch {
      return defaultOpen;
    }
  }, [defaultOpen, storageKey]);
  const [persistedOpen, setPersistedOpen] = useState(() => {
    return getInitialOpen();
  });
  const isPersisted = !isControlled && !!storageKey;
  useEffect(() => {
    if (isPersisted) {
      setPersistedOpen(getInitialOpen());
    }
  }, [getInitialOpen, isPersisted]);
  const handleOpenChange = useCallback((nextOpen: boolean) => {
    if (isPersisted && storageKey) {
      setPersistedOpen(nextOpen);
      try {
        window.localStorage.setItem(storageKey, String(nextOpen));
      } catch {
        // Ignore storage failures; in-memory state still preserves this mount.
      }
    }
    onOpenChange?.(nextOpen);
  }, [isPersisted, onOpenChange, storageKey]);

  return (
    <Collapsible.Root
      className={`property-group${muted ? ' property-group-muted' : ''} ${className}`}
      defaultOpen={isControlled || isPersisted ? undefined : defaultOpen}
      open={isControlled ? open : isPersisted ? persistedOpen : undefined}
      onOpenChange={handleOpenChange}
    >
      <Collapsible.Trigger asChild>
        <button className="property-group-trigger">
          <span className="property-group-chevron">
            <ChevronRightIcon />
          </span>
          {icon && <span className="property-group-icon">{icon}</span>}
          <span className="property-group-title">{title}</span>
          {count !== undefined && <span className="property-group-count">{count}</span>}
          {actions && (
            <span className="property-group-actions" onClick={e => e.stopPropagation()}>
              {actions}
            </span>
          )}
        </button>
      </Collapsible.Trigger>
      <Collapsible.Content className="property-group-content">
        <div className="property-group-inner">{children}</div>
      </Collapsible.Content>
    </Collapsible.Root>
  );
};

export default PropertyGroup;
