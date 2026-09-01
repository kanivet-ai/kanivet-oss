import React, { useEffect, useState } from 'react';
import api from '../../../services/api';
import PropertyGroup from './PropertyGroup';

interface MetricsPropertyGroupProps {
  cluster: string;
  icon?: React.ReactNode;
  children: React.ReactNode;
}

const MetricsPropertyGroup: React.FC<MetricsPropertyGroupProps> = ({
  cluster,
  icon,
  children,
}) => {
  const collapsedStorageKey = `kanivet:metrics-section:${cluster}:collapsed-while-unavailable`;
  const readOpenState = () => {
    if (!api.isMetricsProviderUnavailableCached(cluster)) return true;
    try {
      return window.localStorage.getItem(collapsedStorageKey) !== 'true';
    } catch {
      return true;
    }
  };
  const [providerUnavailable, setProviderUnavailable] = useState(() => api.isMetricsProviderUnavailableCached(cluster));
  const [open, setOpen] = useState(readOpenState);

  useEffect(() => {
    const unavailable = api.isMetricsProviderUnavailableCached(cluster);
    setProviderUnavailable(unavailable);
    setOpen(unavailable ? readOpenState() : true);

    const handleAvailabilityChange = (event: Event) => {
      const { cluster: eventCluster, unavailable: nextUnavailable } = (event as CustomEvent).detail || {};
      if (eventCluster && eventCluster !== cluster) return;

      setProviderUnavailable(!!nextUnavailable);
      setOpen(nextUnavailable ? readOpenState() : true);
    };

    window.addEventListener('metrics-provider-availability', handleAvailabilityChange);
    return () => window.removeEventListener('metrics-provider-availability', handleAvailabilityChange);
  }, [cluster, collapsedStorageKey]);

  return (
    <PropertyGroup
      title="Metrics"
      icon={icon}
      open={open}
      onOpenChange={(nextOpen) => {
        if (!providerUnavailable) {
          setOpen(true);
          return;
        }
        setOpen(nextOpen);
        try {
          window.localStorage.setItem(collapsedStorageKey, String(!nextOpen));
        } catch {
          // Ignore storage failures; current state still applies for this view.
        }
      }}
    >
      {children}
    </PropertyGroup>
  );
};

export default MetricsPropertyGroup;
