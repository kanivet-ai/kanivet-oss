/*
 * SVG compositions of the Kanivet mark, built from src/components/icons/kanivetMark.json.
 * Shared by build-icons.cjs (the asset generator) and anything that wants to preview the icon.
 */
const path = require('path');

const MARK = require(
  path.resolve(__dirname, '../src/components/icons/kanivetMark.json'),
);

const { stem, arm, leg } = MARK.segments;
const [GRAD_TOP, GRAD_BOTTOM] = MARK.tileGradient;
const INK_TOP = '#ffffff';
const INK_BOTTOM = '#e9e8ff';
const SHADOW = '#1b1560';

const bar = (d, stroke, extra = '') =>
  `<path d="${d}" fill="none" stroke="${stroke}" stroke-width="${MARK.stroke}" ` +
  `stroke-linecap="round" stroke-linejoin="round"${extra}/>`;

/** Flat K as one path, for template and monochrome uses. */
const glyph = (color) => bar(`${stem}${arm}${leg}`, color);

/**
 * The app icon in 512-unit tile space: gradient tile, a whisper of top light, then stem, arm and
 * leg drawn back to front, each lifting off the surface beneath with a soft shadow. `id` keeps
 * defs unique when several tiles share a document.
 */
const tileGroup = (id) =>
  `<defs>` +
  `<linearGradient id="${id}-bg" x1="0" y1="0" x2="0" y2="1">` +
  `<stop offset="0" stop-color="${GRAD_TOP}"/><stop offset="1" stop-color="${GRAD_BOTTOM}"/></linearGradient>` +
  // User-space gradient across the whole glyph: object-bounding-box units are undefined on the
  // zero-width stem, and the spec then paints nothing at all.
  `<linearGradient id="${id}-ink" gradientUnits="userSpaceOnUse" x1="0" y1="98" x2="0" y2="414">` +
  `<stop offset="0" stop-color="${INK_TOP}"/><stop offset="1" stop-color="${INK_BOTTOM}"/></linearGradient>` +
  `<linearGradient id="${id}-light" x1="0" y1="0" x2="0" y2="1">` +
  `<stop offset="0" stop-color="#ffffff" stop-opacity="0.16"/><stop offset="0.55" stop-color="#ffffff" stop-opacity="0"/></linearGradient>` +
  // User-space region: a bar's own bounding box can be zero wide (the stem), which would collapse
  // a percentage-based filter region to nothing.
  `<filter id="${id}-lift" filterUnits="userSpaceOnUse" x="-64" y="-64" width="640" height="640">` +
  `<feDropShadow dx="0" dy="7" stdDeviation="7" flood-color="${SHADOW}" flood-opacity="0.32"/></filter>` +
  `<clipPath id="${id}-clip"><rect width="512" height="512" rx="${MARK.tileRadius}"/></clipPath>` +
  `</defs>` +
  `<g clip-path="url(#${id}-clip)">` +
  `<rect width="512" height="512" fill="url(#${id}-bg)"/>` +
  `<rect width="512" height="512" fill="url(#${id}-light)"/>` +
  bar(stem, `url(#${id}-ink)`, ` filter="url(#${id}-lift)"`) +
  bar(arm, `url(#${id}-ink)`, ` filter="url(#${id}-lift)"`) +
  bar(leg, `url(#${id}-ink)`, ` filter="url(#${id}-lift)"`) +
  `</g>` +
  `<rect x="1" y="1" width="510" height="510" rx="${MARK.tileRadius - 1}" fill="none" ` +
  `stroke="#ffffff" stroke-opacity="0.14" stroke-width="2"/>`;

/**
 * Complete icon SVG. `inset` leaves transparent margin around the tile (Apple's 824-in-1024
 * composition) and, when present, adds the tile's own drop shadow the way Apple's template does.
 */
const tileSvg = (size, inset = 0, id = 'k') => {
  const tile = size - inset * 2;
  const k = tile / 512;
  const shadow = inset
    ? `<defs><filter id="${id}-drop" filterUnits="userSpaceOnUse" x="0" y="0" width="${size}" height="${size}">` +
      `<feDropShadow dx="0" dy="${(12 * k).toFixed(2)}" stdDeviation="${(12 * k).toFixed(2)}" ` +
      `flood-color="#000000" flood-opacity="0.3"/></filter></defs>`
    : '';
  const filter = inset ? ` filter="url(#${id}-drop)"` : '';
  return (
    `<svg width="${size}" height="${size}" viewBox="0 0 ${size} ${size}" xmlns="http://www.w3.org/2000/svg">` +
    shadow +
    `<g${filter}><g transform="translate(${inset} ${inset}) scale(${k})">${tileGroup(id)}</g></g>` +
    `</svg>`
  );
};

/** Bare glyph, cropped tight, for macOS template (menu bar) icons. */
const glyphSvg = (size, color) =>
  `<svg width="${size}" height="${size}" viewBox="96 96 320 320" xmlns="http://www.w3.org/2000/svg">` +
  `${glyph(color)}</svg>`;

/**
 * Unmasked, effect-free 1024px layers for Icon Composer: the system supplies the rounded mask,
 * specular highlights, shadows and translucency, so these carry only shape and colour.
 */
const layerSvgs = () => {
  const wrap = (body) =>
    `<svg width="1024" height="1024" viewBox="0 0 512 512" xmlns="http://www.w3.org/2000/svg">${body}</svg>`;
  return {
    'background.svg': wrap(
      `<defs><linearGradient id="bg" x1="0" y1="0" x2="0" y2="1">` +
        `<stop offset="0" stop-color="${GRAD_TOP}"/><stop offset="1" stop-color="${GRAD_BOTTOM}"/></linearGradient></defs>` +
        `<rect width="512" height="512" fill="url(#bg)"/>`,
    ),
    '1-stem.svg': wrap(bar(stem, INK_TOP)),
    '2-arm.svg': wrap(bar(arm, INK_TOP)),
    '3-leg.svg': wrap(bar(leg, INK_TOP)),
  };
};

module.exports = { MARK, tileSvg, glyphSvg, layerSvgs };
