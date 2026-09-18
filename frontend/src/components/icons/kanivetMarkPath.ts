import mark from './kanivetMark.json';

/**
 * Geometry of the Kanivet monogram, shared by the in-app mark and the packaged app icons.
 *
 * The mark is a K built from three overlapping rounded bars on a 512×512 grid: a stem, an arm, and
 * a leg that branches off the arm. Drawn back to front they read as one letter at small sizes and
 * as layered shapes in the app icon, where each bar casts a soft shadow onto the one beneath. The
 * stroke bounding box is centred on (256, 256).
 *
 * `kanivetMark.json` is the single source of truth. `frontend/scripts/build-icons.cjs` reads the
 * same file to render `assets/icon.svg` and the platform icon files; `kanivetMark.test.ts` fails if
 * the generated assets or the pre-hydration splash in `index.html` drift from it.
 */
export const KANIVET_MARK_VIEWBOX = mark.viewBox;
export const KANIVET_MARK_SEGMENTS = mark.segments;
/** All three bars as one path, for flat single-colour rendering. */
export const KANIVET_MARK_PATH = `${mark.segments.stem}${mark.segments.arm}${mark.segments.leg}`;
export const KANIVET_MARK_STROKE = mark.stroke;
/** Corner radius of the tile behind the mark, matching Apple's ~22.4% squircle ratio. */
export const KANIVET_TILE_RADIUS = mark.tileRadius;
/** Top and bottom stops of the tile gradient: the dark theme accent falling into the light one. */
export const KANIVET_TILE_GRADIENT = mark.tileGradient as [string, string];
