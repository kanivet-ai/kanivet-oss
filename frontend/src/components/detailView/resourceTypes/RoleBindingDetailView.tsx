import React from 'react';
import { Link2Icon, PersonIcon } from '@radix-ui/react-icons';
import PropertyRow from '../../common/PropertyRow';
import PropertyGroup from '../shared/PropertyGroup';
import MetadataSection from '../shared/MetadataSection';
import EventsSection from '../shared/EventsSection';
import { DetailViewProps } from '../../../types/detailView';
import './RoleBindingDetailView.css';

const RoleBindingDetailView: React.FC<DetailViewProps> = ({ resource, handleResourceClick }) => {
  const { metadata = {}, roleRef = {} } = resource;
  const subjects = resource.subjects || [];
  const isClusterBinding = resource.kind === 'ClusterRoleBinding';

  return (
    <>
      <PropertyGroup title="Binding" icon={<Link2Icon />} defaultOpen>
        <PropertyRow label="Scope" value={isClusterBinding ? 'Cluster' : 'Namespace'} />
        <PropertyRow
          label="Role"
          value={
            <div className="role-ref">
              <span className="role-ref-kind">{roleRef.kind}</span>
              {handleResourceClick ? (
                <button
                  className="link-button"
                  onClick={(e) => handleResourceClick(roleRef.kind, roleRef.name, roleRef.kind === 'Role' ? metadata.namespace : undefined, e)}
                >
                  {roleRef.name}
                </button>
              ) : (
                <span>{roleRef.name}</span>
              )}
            </div>
          }
          copyText={`${roleRef.kind}/${roleRef.name}`}
        />
      </PropertyGroup>
      <div className="section-divider" />

      <MetadataSection metadata={metadata} handleResourceClick={handleResourceClick} showNamespace={!isClusterBinding} />
      <div className="section-divider" />

      {subjects.length > 0 && (
        <>
          <PropertyGroup title="Subjects" count={subjects.length} icon={<PersonIcon />} defaultOpen>
            <div className="subjects-list">
              {subjects.map((subj: any, i: number) => {
                const displayName = subj.namespace && !isClusterBinding ? `${subj.name} (${subj.namespace})` : subj.name;
                const isClickable = subj.kind === 'ServiceAccount' && handleResourceClick;
                return (
                  <div key={i} className="subject-item">
                    <span className={`subject-kind subject-${subj.kind.toLowerCase()}`}>{subj.kind}</span>
                    {isClickable ? (
                      <button
                        className="link-button"
                        onClick={(e) => handleResourceClick('ServiceAccount', subj.name, subj.namespace || metadata.namespace, e)}
                      >
                        {displayName}
                      </button>
                    ) : (
                      <span className="subject-name">{displayName}</span>
                    )}
                  </div>
                );
              })}
            </div>
          </PropertyGroup>
          <div className="section-divider" />
        </>
      )}

      <EventsSection events={resource.events} />
    </>
  );
};

export default RoleBindingDetailView;
