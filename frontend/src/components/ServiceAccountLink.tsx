import React from 'react';
import NavigationLink from './common/NavigationLink';
import { useStore } from '../store';
import { useShallow } from 'zustand/react/shallow';
import './ServiceAccountLink.css';

interface ServiceAccountLinkProps {
  serviceAccountName: string;
  namespace?: string;
}

const ServiceAccountLink: React.FC<ServiceAccountLinkProps> = ({
  serviceAccountName,
  namespace,
}) => {
  const { currentTab } = useStore(useShallow((s) => ({ currentTab: s.currentTab })));

  if (
    !serviceAccountName ||
    serviceAccountName === '-' ||
    serviceAccountName === 'default'
  ) {
    return (
      <span className="service-account-text">
        {serviceAccountName || 'default'}
      </span>
    );
  }

  const serviceAccountResource = {
    name: 'serviceaccounts',
    group: '',
    version: 'v1',
    kind: 'serviceaccounts',
    namespaced: true,
  };

  return (
    <NavigationLink
      target={{
        resource: serviceAccountResource,
        targetName: serviceAccountName,
        targetNamespace: namespace || 'default',
        nodeId: `${currentTab}//v1/serviceaccounts`,
      }}
      className="service-account-link"
      title={`Navigate to ServiceAccount ${serviceAccountName} in ${
        namespace || 'default'
      }`}
    >
      {serviceAccountName}
    </NavigationLink>
  );
};

export default ServiceAccountLink;
