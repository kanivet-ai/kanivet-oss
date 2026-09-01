import React from 'react';
import PropertyGroup from './detailView/shared/PropertyGroup';
import PropertyRow from './common/PropertyRow';
import './ResourceDetailSkeleton.css';

interface ResourceDetailSkeletonProps {
  resource?: any;
  cluster?: string;
  mode?: 'detail' | 'center';
  actions?: React.ReactNode;
}

const SkeletonLine = ({ width = '100%', className = '' }: { width?: string; className?: string }) => (
  <div className={`skeleton-line ${className}`} style={{ width }} />
);

const ResourceDetailSkeleton = ({
  resource,
  mode = 'detail',
  actions,
}: ResourceDetailSkeletonProps) => {
  const metadata = resource?.metadata || resource || {};
  const name = metadata.name || 'Loading...';
  const namespace = metadata.namespace;
  const kind = resource?.kind || metadata.kind || 'Resource';
  const uid = metadata.uid;
  const hasBasicInfo = name && name !== 'Loading...';

  return (
    <div
      className={`resource-detail resource-detail-skeleton ${
        mode === 'center' ? 'resource-detail-center' : ''
      }`}
    >
      <div className="resource-header">
        <div className="resource-header-top">
          <div className="resource-identity">
            <span className="resource-kind-badge">{kind}</span>
            <h2 className="resource-name">
              {hasBasicInfo ? <span>{name}</span> : <SkeletonLine width="180px" className="skeleton-line-lg" />}
            </h2>
          </div>
          <div className="resource-right-column">
            {actions}
          </div>
        </div>
      </div>

      <div className="resource-detail-content">
        <PropertyGroup title="Metadata" defaultOpen={true}>
          {namespace ? (
            <PropertyRow label="Namespace" value={namespace} />
          ) : (
            <div className="skeleton-row">
              <div className="skeleton-label">Namespace</div>
              <div className="skeleton-value"><SkeletonLine width="70px" /></div>
            </div>
          )}
          <div className="skeleton-row">
            <div className="skeleton-label">Created</div>
            <div className="skeleton-value"><SkeletonLine width="90px" /></div>
          </div>
          {uid ? (
            <PropertyRow label="UID" value={uid} mono />
          ) : (
            <div className="skeleton-row">
              <div className="skeleton-label">UID</div>
              <div className="skeleton-value"><SkeletonLine width="200px" /></div>
            </div>
          )}
        </PropertyGroup>

        <PropertyGroup title="Status" defaultOpen={true}>
          <div className="skeleton-row">
            <div className="skeleton-label">Phase</div>
            <div className="skeleton-value"><SkeletonLine width="60px" /></div>
          </div>
        </PropertyGroup>

        <PropertyGroup title="Conditions" defaultOpen={true}>
          <div className="skeleton-row">
            <div className="skeleton-label"><SkeletonLine width="80px" /></div>
            <div className="skeleton-value"><SkeletonLine width="50px" /></div>
          </div>
          <div className="skeleton-row">
            <div className="skeleton-label"><SkeletonLine width="100px" /></div>
            <div className="skeleton-value"><SkeletonLine width="50px" /></div>
          </div>
          <div className="skeleton-row">
            <div className="skeleton-label"><SkeletonLine width="70px" /></div>
            <div className="skeleton-value"><SkeletonLine width="50px" /></div>
          </div>
        </PropertyGroup>
      </div>
    </div>
  );
};

export default ResourceDetailSkeleton;
