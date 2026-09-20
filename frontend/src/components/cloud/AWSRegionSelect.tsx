import React from 'react';

/** IAM Identity Center regions, grouped the way the AWS console lists them. */
export const AWS_REGION_GROUPS: ReadonlyArray<{
  label: string;
  regions: ReadonlyArray<[code: string, name: string]>;
}> = [
  {
    label: 'US',
    regions: [
      ['us-east-1', 'N. Virginia'],
      ['us-east-2', 'Ohio'],
      ['us-west-1', 'N. California'],
      ['us-west-2', 'Oregon'],
    ],
  },
  {
    label: 'Canada',
    regions: [
      ['ca-central-1', 'Central'],
      ['ca-west-1', 'Calgary'],
    ],
  },
  { label: 'South America', regions: [['sa-east-1', 'São Paulo']] },
  {
    label: 'Europe',
    regions: [
      ['eu-west-1', 'Ireland'],
      ['eu-west-2', 'London'],
      ['eu-west-3', 'Paris'],
      ['eu-central-1', 'Frankfurt'],
      ['eu-central-2', 'Zurich'],
      ['eu-north-1', 'Stockholm'],
      ['eu-south-1', 'Milan'],
      ['eu-south-2', 'Spain'],
    ],
  },
  {
    label: 'Asia Pacific',
    regions: [
      ['ap-northeast-1', 'Tokyo'],
      ['ap-northeast-2', 'Seoul'],
      ['ap-northeast-3', 'Osaka'],
      ['ap-southeast-1', 'Singapore'],
      ['ap-southeast-2', 'Sydney'],
      ['ap-southeast-3', 'Jakarta'],
      ['ap-southeast-4', 'Melbourne'],
      ['ap-southeast-5', 'Malaysia'],
      ['ap-southeast-6', 'New Zealand'],
      ['ap-southeast-7', 'Thailand'],
      ['ap-south-1', 'Mumbai'],
      ['ap-south-2', 'Hyderabad'],
      ['ap-east-1', 'Hong Kong'],
      ['ap-east-2', 'Taipei'],
    ],
  },
  {
    label: 'Middle East',
    regions: [
      ['me-south-1', 'Bahrain'],
      ['me-central-1', 'UAE'],
    ],
  },
  { label: 'Africa', regions: [['af-south-1', 'Cape Town']] },
  { label: 'Israel', regions: [['il-central-1', 'Tel Aviv']] },
  { label: 'Mexico', regions: [['mx-central-1', 'Central']] },
];

interface AWSRegionSelectProps {
  value: string;
  onChange: (region: string) => void;
  className?: string;
  id?: string;
  'aria-label'?: string;
}

const AWSRegionSelect: React.FC<AWSRegionSelectProps> = ({
  value,
  onChange,
  className = 'ap-select',
  id,
  ...rest
}) => (
  <select
    id={id}
    className={className}
    value={value}
    onChange={(e) => onChange(e.target.value)}
    onKeyDown={(e) => e.stopPropagation()}
    aria-label={rest['aria-label'] || 'Identity Center region'}
  >
    {AWS_REGION_GROUPS.map((group) => (
      <optgroup key={group.label} label={group.label}>
        {group.regions.map(([code, name]) => (
          <option key={code} value={code}>
            {code} ({name})
          </option>
        ))}
      </optgroup>
    ))}
  </select>
);

export default AWSRegionSelect;
