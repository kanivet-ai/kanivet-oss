import React from 'react';
import { InfoCircledIcon } from '@radix-ui/react-icons';
import PropertyRow from '../../common/PropertyRow';
import PropertyGroup from './PropertyGroup';
import TimestampValue from '../../common/TimestampValue';
import SmartValue from '../../common/SmartValue';
import './MetadataSection.css';

interface MetadataSectionProps {
  metadata: any;
  status?: any;
  handleResourceClick?: (kind: string, name: string, namespace?: string, e?: React.MouseEvent, apiVersion?: string) => void;
  showNamespace?: boolean;
  showLabels?: boolean;
  showAnnotations?: boolean;
  children?: React.ReactNode;
  defaultOpen?: boolean;
}

const MetadataSection: React.FC<MetadataSectionProps> = ({
  metadata = {},
  handleResourceClick,
  showNamespace = true,
  showLabels = true,
  showAnnotations = true,
  children,
  defaultOpen = true,
}) => {
  const labels = metadata.labels || {};
  const annotations = Object.fromEntries(
    Object.entries(metadata.annotations || {}).filter(
      ([key]) => key !== 'kubectl.kubernetes.io/last-applied-configuration'
    )
  );
  const labelCount = Object.keys(labels).length;
  const annotationCount = Object.keys(annotations).length;
  const ownerRef = metadata.ownerReferences?.[0];
  const truncatedUid = metadata.uid ? `${metadata.uid.slice(0, 8)}...` : '';

  return (
    <PropertyGroup title="Metadata" icon={<InfoCircledIcon />} defaultOpen={defaultOpen}>
      {showNamespace && metadata.namespace && (
        <PropertyRow
          label="Namespace"
          copyText={metadata.namespace}
          value={
            handleResourceClick ? (
              <button
                className="link-button"
                onClick={(e) => handleResourceClick('Namespace', metadata.namespace, undefined, e)}
              >
                {metadata.namespace}
              </button>
            ) : (
              metadata.namespace
            )
          }
        />
      )}
      <PropertyRow
        label="Created"
        copyText={metadata.creationTimestamp}
        value={<TimestampValue timestamp={metadata.creationTimestamp} suffix="ago" />}
      />
      {ownerRef && (
        <PropertyRow
          label="Owner"
          copyText={`${ownerRef.kind}/${ownerRef.name}`}
          value={
            handleResourceClick ? (
              <button
                className="link-button"
                onClick={(e) => handleResourceClick(ownerRef.kind, ownerRef.name, metadata.namespace, e, ownerRef.apiVersion)}
              >
                {ownerRef.kind}/{ownerRef.name}
              </button>
            ) : (
              `${ownerRef.kind}/${ownerRef.name}`
            )
          }
        />
      )}
      <PropertyRow label="UID" value={truncatedUid} copyText={metadata.uid} mono />
      {showLabels && labelCount > 0 && (
        <PropertyGroup title="Labels" count={labelCount} defaultOpen={false}>
          <SmartValue value={labels} format="labels" />
        </PropertyGroup>
      )}
      {showAnnotations && annotationCount > 0 && (
        <PropertyGroup title="Annotations" count={annotationCount} defaultOpen={false}>
          <SmartValue value={annotations} format="annotations" />
        </PropertyGroup>
      )}
      {children}
    </PropertyGroup>
  );
};

export default MetadataSection;
