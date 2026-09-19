/*
  Monaco themes fed from the Kanivet design tokens.

  Editor chrome (background, gutter, selection, widgets) is read from the CSS
  tokens on <html> at runtime so it follows the appearance; syntax colours are
  the Xcode palette — a code theme, so those literals are intentional.

  Two themes are built (`kanivet-dark`, `kanivet-light`). `kanivet` is the name
  editors mount with: it is redefined to match <html data-theme> whenever the
  appearance flips, so every open editor follows along.
*/
export const KANIVET_MONACO_THEME = 'kanivet';
export const KANIVET_MONACO_THEME_DARK = 'kanivet-dark';
export const KANIVET_MONACO_THEME_LIGHT = 'kanivet-light';

/* ---- Xcode syntax palette (dark / light) --------------------------------- */
const XCODE = {
  dark: {
    keyword: '#fc5fa3',
    string: '#fc6a5d',
    number: '#d0bf69',
    type: '#5dd8ff',
    fn: '#67b7a4',
    comment: '#6c7986',
    key: '#d9c97c',
  },
  light: {
    keyword: '#ad3da4',
    string: '#d12f1b',
    number: '#272ad8',
    type: '#3900a0',
    fn: '#326d74',
    comment: '#5d6c79',
    key: '#326d74',
  },
};

/* ---- Colour helpers ------------------------------------------------------ */
type Rgba = { r: number; g: number; b: number; a: number };

let canvas: CanvasRenderingContext2D | null | undefined;

/** Parse #rgb / #rrggbb / #rrggbbaa / rgb() / rgba() (anything else via the canvas) into channels. */
const parseColor = (input: string): Rgba | null => {
  const value = (input || '').trim();
  if (!value) return null;
  const hex = value.match(/^#([0-9a-f]{3,8})$/i)?.[1];
  if (hex && hex.length !== 5 && hex.length !== 7) {
    const short = hex.length <= 4;
    const step = short ? 1 : 2;
    const channel = (i: number) => {
      const part = hex.slice(i * step, i * step + step);
      return parseInt(short ? part + part : part, 16);
    };
    const hasAlpha = hex.length === 4 || hex.length === 8;
    return { r: channel(0), g: channel(1), b: channel(2), a: hasAlpha ? channel(3) / 255 : 1 };
  }
  const rgb = value.match(/^rgba?\(\s*([\d.]+)[,\s]+([\d.]+)[,\s]+([\d.]+)(?:[,\s/]+([\d.]+%?))?\s*\)$/i);
  if (rgb) {
    const alpha = rgb[4] === undefined ? 1 : rgb[4].endsWith('%') ? parseFloat(rgb[4]) / 100 : parseFloat(rgb[4]);
    return { r: +rgb[1], g: +rgb[2], b: +rgb[3], a: alpha };
  }
  if (canvas === undefined) canvas = document.createElement('canvas').getContext('2d');
  if (!canvas) return null;
  canvas.fillStyle = '#010203';
  canvas.fillStyle = value;
  const normalised = String(canvas.fillStyle);
  if (normalised === '#010203' || normalised === value) return null;
  return parseColor(normalised);
};

const hex2 = (n: number) => Math.round(Math.min(255, Math.max(0, n))).toString(16).padStart(2, '0');

/** Opaque Monaco colour (#RRGGBB) — alpha is dropped. */
const toHex6 = (c: Rgba) => `#${hex2(c.r)}${hex2(c.g)}${hex2(c.b)}`;

/** Monaco colour with alpha (#RRGGBBAA). */
const toHex8 = (c: Rgba, alpha = c.a) => `${toHex6(c)}${hex2(alpha * 255)}`;

/** Flatten a translucent token onto a solid background so Monaco gets an opaque #RRGGBB. */
const composite = (fg: Rgba, bg: Rgba): Rgba => ({
  r: fg.r * fg.a + bg.r * (1 - fg.a),
  g: fg.g * fg.a + bg.g * (1 - fg.a),
  b: fg.b * fg.a + bg.b * (1 - fg.a),
  a: 1,
});

/* ---- Theme ---------------------------------------------------------------- */
const buildColors = (dark: boolean): Record<string, string> => {
  const style = getComputedStyle(document.documentElement);
  const token = (name: string) => parseColor(style.getPropertyValue(name).trim());
  const bg = token('--content');

  /** Solid colour: translucent tokens are composited onto the editor background. */
  const solid = (name: string) => {
    const c = token(name);
    if (!c) return undefined;
    return toHex6(c.a < 1 && bg ? composite(c, bg) : c);
  };
  /** Token at a given alpha (#RRGGBBAA). */
  const alpha = (name: string, a: number) => {
    const c = token(name);
    return c ? toHex8(c, a) : undefined;
  };
  /** Token with the alpha it already carries (#RRGGBBAA). */
  const asIs = (name: string) => {
    const c = token(name);
    return c ? toHex8(c) : undefined;
  };

  const none = '#00000000';
  const onSelection = solid('--on-sel');

  const colors: Record<string, string | undefined> = {
    'editor.background': solid('--content'),
    'editor.foreground': solid('--text'),
    'editor.lineHighlightBackground': solid('--hover'),
    'editor.lineHighlightBorder': none,
    'editor.selectionBackground': alpha('--blue', 0.3),
    'editor.inactiveSelectionBackground': alpha('--blue', 0.15),
    'editor.selectionHighlightBackground': alpha('--blue', 0.18),
    'editor.wordHighlightBackground': alpha('--blue', 0.16),
    'editor.wordHighlightStrongBackground': alpha('--blue', 0.26),
    'editor.findMatchBackground': alpha('--orange', 0.45),
    'editor.findMatchHighlightBackground': alpha('--orange', 0.22),
    'editorCursor.foreground': solid('--blue'),
    'editorLineNumber.foreground': solid('--text3'),
    'editorLineNumber.activeForeground': solid('--text2'),
    'editorGutter.background': solid('--content'),
    'editorGutter.modifiedBackground': solid('--blue'),
    'editorGutter.addedBackground': solid('--green'),
    'editorGutter.deletedBackground': solid('--red'),
    'editorIndentGuide.background1': solid('--hair'),
    'editorIndentGuide.activeBackground1': solid('--sep'),
    'editorWhitespace.foreground': solid('--text4'),
    'editorBracketMatch.background': alpha('--blue', 0.2),
    'editorBracketMatch.border': alpha('--blue', 0.6),
    'editorStickyScroll.background': solid('--win'),
    'editorStickyScroll.shadow': alpha('--content', 0.6),
    'editorStickyScrollHover.background': solid('--hover'),
    'editorError.foreground': solid('--red'),
    'editorWarning.foreground': solid('--orange'),
    'editorInfo.foreground': solid('--blue'),
    'editorOverviewRuler.border': none,
    'editorOverviewRuler.errorForeground': solid('--red'),
    'editorOverviewRuler.warningForeground': solid('--orange'),
    'editorOverviewRuler.findMatchForeground': alpha('--orange', 0.6),
    'scrollbar.shadow': none,
    'scrollbarSlider.background': asIs('--ctrl'),
    'scrollbarSlider.hoverBackground': asIs('--ctrl2'),
    'scrollbarSlider.activeBackground': alpha('--ctrl2', 0.55),
    'minimap.background': solid('--content'),
    'minimapSlider.background': asIs('--ctrl'),
    'minimapSlider.hoverBackground': asIs('--ctrl2'),
    'minimapSlider.activeBackground': alpha('--ctrl2', 0.55),
    'editorWidget.background': solid('--card'),
    'editorWidget.border': solid('--sep'),
    'editorWidget.foreground': solid('--text'),
    'editorHoverWidget.background': solid('--card'),
    'editorHoverWidget.border': solid('--sep'),
    'editorHoverWidget.foreground': solid('--text'),
    'editorSuggestWidget.background': solid('--card'),
    'editorSuggestWidget.border': solid('--sep'),
    'editorSuggestWidget.foreground': solid('--text'),
    'editorSuggestWidget.selectedBackground': solid('--blue'),
    'editorSuggestWidget.selectedForeground': onSelection,
    'editorSuggestWidget.highlightForeground': solid('--blue'),
    'input.background': solid('--ctrl'),
    'input.foreground': solid('--text'),
    'input.border': none,
    'input.placeholderForeground': solid('--text3'),
    'inputOption.activeBackground': alpha('--blue', 0.25),
    'inputOption.activeBorder': solid('--blue'),
    focusBorder: solid('--blue'),
    'list.hoverBackground': solid('--hover'),
    'list.activeSelectionBackground': solid('--blue'),
    'list.activeSelectionForeground': onSelection,
    'list.focusBackground': solid('--blue'),
    'list.focusForeground': onSelection,
    'list.inactiveSelectionBackground': solid('--bg-active'),
    'list.highlightForeground': solid('--blue'),
    'textLink.foreground': solid('--blue'),
    'editorLink.activeForeground': solid('--blue'),
    'widget.shadow': dark ? '#00000059' : '#0000001f',
    'diffEditor.insertedTextBackground': alpha('--green', 0.18),
    'diffEditor.removedTextBackground': alpha('--red', 0.18),
    'diffEditor.insertedLineBackground': alpha('--green', 0.08),
    'diffEditor.removedLineBackground': alpha('--red', 0.08),
  };

  // Tokens that could not be read fall back to the base theme (vs / vs-dark).
  return Object.fromEntries(
    Object.entries(colors).filter((entry): entry is [string, string] => typeof entry[1] === 'string'),
  );
};

const buildRules = (dark: boolean, colors: Record<string, string>) => {
  const p = dark ? XCODE.dark : XCODE.light;
  const fg = colors['editor.foreground'];
  const punctuation = colors['editorLineNumber.activeForeground'] || fg;
  const rules: [string, string | undefined, string?][] = [
    ['', fg],
    ['keyword', p.keyword],
    ['string', p.string],
    ['string.escape', p.keyword],
    ['string.key.json', p.key],
    ['string.value.json', p.string],
    ['number', p.number],
    ['constant', p.number],
    ['type', p.type],
    ['type.yaml', p.key],
    ['type.identifier', p.type],
    ['key', p.key],
    ['property', p.key],
    ['attribute.name', p.key],
    ['attribute.value', p.string],
    ['tag', p.type],
    ['namespace', p.fn],
    ['function', p.fn],
    ['predefined', p.fn],
    ['identifier', fg],
    ['variable', fg],
    ['comment', p.comment],
    ['meta.directive', p.comment],
    ['operators', punctuation],
    ['delimiter', punctuation],
    ['regexp', p.string],
  ];
  return rules
    .filter((rule): rule is [string, string, string?] => typeof rule[1] === 'string')
    .map(([token, color, fontStyle]) => ({
      token,
      foreground: color.slice(1),
      ...(fontStyle ? { fontStyle } : {}),
    }));
};

export const buildKanivetMonacoTheme = (dark: boolean) => {
  const colors = buildColors(dark);
  return {
    base: dark ? 'vs-dark' : 'vs',
    inherit: true,
    rules: buildRules(dark, colors),
    colors,
  };
};

const isDark = () => document.documentElement.getAttribute('data-theme') !== 'light';
let observed: any = null;

export const installKanivetMonacoTheme = (monaco: any) => {
  const apply = () => {
    const dark = isDark();
    const theme = buildKanivetMonacoTheme(dark);
    monaco.editor.defineTheme(dark ? KANIVET_MONACO_THEME_DARK : KANIVET_MONACO_THEME_LIGHT, theme);
    monaco.editor.defineTheme(KANIVET_MONACO_THEME, theme);
    monaco.editor.setTheme(KANIVET_MONACO_THEME);
  };
  apply();
  if (observed === monaco) return;
  observed = monaco;
  new MutationObserver(() => requestAnimationFrame(apply)).observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme', 'style'] });
};
