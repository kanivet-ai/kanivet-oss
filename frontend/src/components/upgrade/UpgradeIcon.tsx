import type { SVGProps } from 'react';

/** Arrow rising out of a base line: Kubernetes upgrade readiness. */
export const UpgradeIcon = ({ width = 15, height = 15, ...rest }: SVGProps<SVGSVGElement>) => (
  <svg
    width={width}
    height={height}
    viewBox="0 0 16 16"
    fill="none"
    stroke="currentColor"
    strokeWidth={1.25}
    strokeLinecap="round"
    strokeLinejoin="round"
    xmlns="http://www.w3.org/2000/svg"
    aria-hidden="true"
    focusable="false"
    {...rest}
  >
    <path d="M8 11.5V2.75M4.75 6 8 2.75 11.25 6" />
    <path d="M2.75 13.25h10.5" />
  </svg>
);

export default UpgradeIcon;
