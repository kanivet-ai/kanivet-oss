import React from 'react';
import { GearIcon } from '@radix-ui/react-icons';
import PropertyGroup from './PropertyGroup';
import SmartValue from '../../common/SmartValue';
import './SpecificationSection.css';

interface SpecificationSectionProps {
  spec: Record<string, any>;
  title?: string;
  defaultOpen?: boolean;
  exclude?: string[];
  handleResourceClick?: (kind: string, name: string, namespace?: string, e?: React.MouseEvent) => void;
}

const SKIP_KEYS = ['containers', 'initContainers', 'volumes', 'template'];

const SpecificationSection: React.FC<SpecificationSectionProps> = ({
  spec,
  title = 'Specification',
  defaultOpen = true,
  exclude = [],
}) => {
  if (!spec || Object.keys(spec).length === 0) return null;

  const allExclude = [...SKIP_KEYS, ...exclude];
  const entries = Object.entries(spec).filter(([key]) => !allExclude.includes(key));

  if (entries.length === 0) return null;

  const specObject = Object.fromEntries(entries);

  return (
    <PropertyGroup title={title} icon={<GearIcon />} count={entries.length} defaultOpen={defaultOpen}>
      <div className="spec-content">
        <SmartValue value={specObject} forceExpanded />
      </div>
    </PropertyGroup>
  );
};

export default SpecificationSection;
