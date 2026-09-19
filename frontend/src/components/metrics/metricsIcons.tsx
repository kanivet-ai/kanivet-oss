import React from 'react';
import type { MetricType } from './metricsFormat';

/* 12px stroked glyphs (1.6px, currentColor) for the metric segments and card actions. */

const glyph = (children: React.ReactNode, size = 12) => (
  <svg
    width={size}
    height={size}
    viewBox="0 0 16 16"
    fill="none"
    stroke="currentColor"
    strokeWidth="1.6"
    strokeLinecap="round"
    strokeLinejoin="round"
    aria-hidden="true"
    focusable="false"
  >
    {children}
  </svg>
);

export const METRIC_ICONS: Record<MetricType, React.ReactNode> = {
  cpu: glyph(
    <>
      <rect x="4" y="4" width="8" height="8" rx="1.5" />
      <rect x="6.5" y="6.5" width="3" height="3" rx="0.5" />
      <path d="M6 1.5V4M10 1.5V4M6 12v2.5M10 12v2.5M1.5 6H4M1.5 10H4M12 6h2.5M12 10h2.5" />
    </>,
  ),
  memory: glyph(
    <>
      <rect x="1.5" y="4.5" width="13" height="7" rx="1.5" />
      <path d="M4.5 7v2M7 7v2M9.5 7v2M12 7v2M3.5 11.5v1.5M8 11.5v1.5M12.5 11.5v1.5" />
    </>,
  ),
  network_rx: glyph(
    <>
      <path d="M8 2v8.5" />
      <path d="M4.8 7.3 8 10.5l3.2-3.2" />
      <path d="M2.5 13.5h11" />
    </>,
  ),
  network_tx: glyph(
    <>
      <path d="M8 13V4.5" />
      <path d="M4.8 7.7 8 4.5l3.2 3.2" />
      <path d="M2.5 2.5h11" />
    </>,
  ),
  disk_read: glyph(
    <>
      <path d="M2 11.5A1.5 1.5 0 0 1 3.5 10h9a1.5 1.5 0 0 1 1.5 1.5v1A1.5 1.5 0 0 1 12.5 14h-9A1.5 1.5 0 0 1 2 12.5Z" />
      <path d="M11.5 12h.01" />
      <path d="M8 1.5v6" />
      <path d="M5.8 5.3 8 7.5l2.2-2.2" />
    </>,
  ),
  disk_write: glyph(
    <>
      <path d="M2 11.5A1.5 1.5 0 0 1 3.5 10h9a1.5 1.5 0 0 1 1.5 1.5v1A1.5 1.5 0 0 1 12.5 14h-9A1.5 1.5 0 0 1 2 12.5Z" />
      <path d="M11.5 12h.01" />
      <path d="M8 7.5v-6" />
      <path d="M5.8 3.7 8 1.5l2.2 2.2" />
    </>,
  ),
};

export const ClockIcon = () =>
  glyph(
    <>
      <circle cx="8" cy="8" r="6" />
      <path d="M8 4.5V8l2.5 1.5" />
    </>,
  );

export const RefreshIcon = () =>
  glyph(
    <>
      <path d="M13.5 8a5.5 5.5 0 1 1-1.6-3.9" />
      <path d="M13.5 2.5v3h-3" />
    </>,
  );

export const ResetZoomIcon = () =>
  glyph(
    <>
      <circle cx="7" cy="7" r="4.5" />
      <path d="M13.5 13.5 10.2 10.2" />
      <path d="M5 7h4" />
    </>,
  );

export const GearIcon = () =>
  glyph(
    <>
      <circle cx="8" cy="8" r="2.2" />
      <path d="M8 1.8v1.7M8 12.5v1.7M1.8 8h1.7M12.5 8h1.7M3.6 3.6l1.2 1.2M11.2 11.2l1.2 1.2M3.6 12.4l1.2-1.2M11.2 4.8l1.2-1.2" />
    </>,
  );

/* Larger (20px) state glyphs for the provider states. */

const stateGlyph = (children: React.ReactNode) => (
  <svg
    width="22"
    height="22"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    strokeWidth="1.6"
    strokeLinecap="round"
    strokeLinejoin="round"
    aria-hidden="true"
    focusable="false"
  >
    {children}
  </svg>
);

export const WaveformIcon = () =>
  stateGlyph(
    <>
      <path d="M3 12h3l2.5-6 3 12 3-9 2 3H21" />
    </>,
  );

export const UnpluggedIcon = () =>
  stateGlyph(
    <>
      <path d="M9 3v4M15 3v4" />
      <path d="M6 7h12v3a6 6 0 0 1-12 0V7Z" />
      <path d="M12 16v5" />
      <path d="M4 20 20 4" />
    </>,
  );

export const KeyIcon = () =>
  stateGlyph(
    <>
      <circle cx="8" cy="14" r="4" />
      <path d="M10.8 11.2 20 2M15 7l3 3M17.5 4.5l2 2" />
    </>,
  );

export const SearchIcon = () =>
  stateGlyph(
    <>
      <circle cx="11" cy="11" r="6.5" />
      <path d="M20.5 20.5 16 16" />
    </>,
  );
