import React from 'react';
import { LayersIcon } from '@radix-ui/react-icons';
import type { ImagePullContext } from '../../services/api/images';
import { openImageTab, templatePullContext } from './openImageTab';
import { shortImageRef } from './imageUtils';
import PropertyGroup from '../detailView/shared/PropertyGroup';
import './ImageInspector.css';

interface Props {
  image: string;
  cluster: string;
  pullContext: Omit<ImagePullContext, 'cluster'>;
  label?: string;
}

/** "Inspect image" action for a container. */
export const InspectImageButton: React.FC<Props> = ({ image, cluster, pullContext, label = 'Inspect image' }) => (
  <button
    type="button"
    className="ap-btn ap-btn--sm inspect-image-btn"
    disabled={!image || !cluster}
    onClick={(e) => {
      e.stopPropagation();
      openImageTab(image, cluster, pullContext);
    }}
    title={`Open ${image} in an Image tab`}
  >
    <LayersIcon /> {label}
  </button>
);

/** Images of a workload's pod template, each with an Inspect action. */
export const WorkloadImagesSection: React.FC<{ resource: any; cluster: string }> = ({ resource, cluster }) => {
  const spec = resource?.spec?.template?.spec || resource?.spec?.jobTemplate?.spec?.template?.spec;
  const containers = [
    ...(spec?.initContainers || []).map((c: any) => ({ ...c, isInit: true })),
    ...(spec?.containers || []),
  ].filter((c: any) => c.image);
  if (containers.length === 0) return null;
  const pc = templatePullContext(resource);
  return (
    <PropertyGroup title="Images" count={containers.length} icon={<LayersIcon />} persistKey="workload-images">
      <div className="ap-card workload-images">
        {containers.map((c: any) => (
          <div key={`${c.isInit ? 'init-' : ''}${c.name}`} className="ap-card-row workload-images-row">
            <span className="workload-images-name">
              {c.name}
              {c.isInit && <span className="ap-badge ap-badge--sm">init</span>}
              <span className="workload-images-ref ap-mono ap-truncate" title={c.image}>{shortImageRef(c.image)}</span>
            </span>
            <InspectImageButton image={c.image} cluster={cluster} pullContext={pc} label="Inspect" />
          </div>
        ))}
      </div>
    </PropertyGroup>
  );
};

export default InspectImageButton;
