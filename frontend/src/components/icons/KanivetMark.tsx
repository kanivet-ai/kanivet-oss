import { useId, type SVGProps } from 'react';
import {
  KANIVET_MARK_PATH,
  KANIVET_MARK_STROKE,
  KANIVET_MARK_VIEWBOX,
  KANIVET_TILE_GRADIENT,
  KANIVET_TILE_RADIUS,
} from './kanivetMarkPath';

export interface KanivetMarkProps
  extends Omit<SVGProps<SVGSVGElement>, 'width' | 'height'> {
  /** Rendered size in px; the mark is always square. */
  size?: number;
  /** Draw the app-icon tile behind the K. Off, the K alone is drawn in `currentColor`. */
  tile?: boolean;
}

/**
 * The Kanivet monogram. With `tile` it paints the app icon: a white K on the iris gradient tile,
 * the same on both themes like a real app icon. Without, it is a bare glyph that inherits `color`
 * like any other icon.
 */
const KanivetMark = ({
  size = 20,
  tile = false,
  ...rest
}: KanivetMarkProps) => {
  const gradientId = `kanivet-mark-${useId().replace(/:/g, '')}`;
  return (
    <svg
      width={size}
      height={size}
      viewBox={KANIVET_MARK_VIEWBOX}
      xmlns="http://www.w3.org/2000/svg"
      aria-hidden="true"
      focusable="false"
      {...rest}
    >
      {tile && (
        <>
          <defs>
            <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
              <stop offset="0" stopColor={KANIVET_TILE_GRADIENT[0]} />
              <stop offset="1" stopColor={KANIVET_TILE_GRADIENT[1]} />
            </linearGradient>
          </defs>
          <rect
            width={512}
            height={512}
            rx={KANIVET_TILE_RADIUS}
            fill={`url(#${gradientId})`}
          />
        </>
      )}
      <path
        d={KANIVET_MARK_PATH}
        fill="none"
        stroke={tile ? '#ffffff' : 'currentColor'}
        strokeWidth={KANIVET_MARK_STROKE}
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
};

export default KanivetMark;
