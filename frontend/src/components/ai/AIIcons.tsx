import React from 'react';

/** Stroked sparkle used for every AI affordance. */
export const SparkleIcon: React.FC<{ size?: number }> = ({ size = 15 }) => (
  <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <path d="M12 3l1.8 4.9a3 3 0 0 0 1.8 1.8L20.5 11.5l-4.9 1.8a3 3 0 0 0-1.8 1.8L12 20l-1.8-4.9a3 3 0 0 0-1.8-1.8L3.5 11.5l4.9-1.8a3 3 0 0 0 1.8-1.8z" />
    <path d="M19 3v3M17.5 4.5h3" />
  </svg>
);

export const ToolIcon: React.FC = () => (
  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <path d="M14.7 6.3a4 4 0 0 0-5.4 5.4L3 18l3 3 6.3-6.3a4 4 0 0 0 5.4-5.4l-2.6 2.6-2.4-.6-.6-2.4z" />
  </svg>
);
