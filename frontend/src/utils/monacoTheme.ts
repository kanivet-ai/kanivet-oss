export const KANIVET_MONACO_THEME = 'kanivet';

let canvas: CanvasRenderingContext2D | null = null;
const toHex = (value: string) => {
  canvas ||= document.createElement('canvas').getContext('2d');
  if (!canvas) return '#000000';
  canvas.fillStyle = '#000';
  canvas.fillStyle = value.trim();
  return canvas.fillStyle.startsWith('#') ? canvas.fillStyle : '#000000';
};

const readVars = () => {
  const style = getComputedStyle(document.documentElement);
  const get = (name: string) => toHex(style.getPropertyValue(name));
  return {
    bg: get('--bg-primary'), bg2: get('--bg-secondary'), bg3: get('--bg-tertiary'), hover: get('--bg-hover'), active: get('--bg-active'),
    text: get('--text-primary'), text2: get('--text-secondary'), muted: get('--text-muted'), border: get('--border'),
    accent: get('--accent'), accent2: get('--accent-vcluster'), success: get('--success'), warning: get('--warning'), danger: get('--danger'),
  };
};

export const buildKanivetMonacoTheme = (dark: boolean) => {
  const v = readVars();
  const a = (hex: string, alpha: number) => hex + Math.round(alpha * 255).toString(16).padStart(2, '0');
  return {
    base: dark ? 'vs-dark' : 'vs',
    inherit: true,
    rules: ([
      ['', v.text], ['type', v.accent], ['string', v.text], ['number', v.success], ['keyword', v.accent2],
      ['comment', v.muted, 'italic'], ['operators', v.muted], ['tag', v.warning], ['meta.directive', v.muted],
      ['namespace', v.accent2], ['delimiter', v.text2],
    ] as [string, string, string?][]).flatMap(([token, color, fontStyle]) =>
      (token ? [token, `${token}.yaml`] : ['']).map((t) => ({ token: t, foreground: color.slice(1), ...(fontStyle ? { fontStyle } : {}) })),
    ),
    colors: {
      'editor.background': v.bg,
      'editor.foreground': v.text,
      'editor.lineHighlightBackground': a(v.hover, dark ? 0.55 : 0.7),
      'editor.lineHighlightBorder': '#00000000',
      'editor.selectionBackground': a(v.accent, 0.3),
      'editor.inactiveSelectionBackground': a(v.accent, 0.15),
      'editor.selectionHighlightBackground': a(v.accent, 0.18),
      'editor.wordHighlightBackground': a(v.accent, 0.16),
      'editor.wordHighlightStrongBackground': a(v.accent, 0.26),
      'editor.findMatchBackground': a(v.warning, 0.45),
      'editor.findMatchHighlightBackground': a(v.warning, 0.22),
      'editorCursor.foreground': v.accent,
      'editorLineNumber.foreground': v.muted,
      'editorLineNumber.activeForeground': v.text2,
      'editorGutter.background': v.bg,
      'editorGutter.modifiedBackground': v.accent,
      'editorGutter.addedBackground': v.success,
      'editorGutter.deletedBackground': v.danger,
      'editorIndentGuide.background1': a(v.border, 0.9),
      'editorIndentGuide.activeBackground1': v.muted,
      'editorWhitespace.foreground': a(v.muted, 0.4),
      'editorBracketMatch.background': a(v.accent, 0.2),
      'editorBracketMatch.border': a(v.accent, 0.6),
      'editorStickyScroll.background': v.bg2,
      'editorStickyScroll.shadow': a(v.bg, 0.6),
      'editorStickyScrollHover.background': v.hover,
      'editorError.foreground': v.danger,
      'editorWarning.foreground': v.warning,
      'editorInfo.foreground': v.accent,
      'editorOverviewRuler.border': '#00000000',
      'editorOverviewRuler.errorForeground': v.danger,
      'editorOverviewRuler.warningForeground': v.warning,
      'editorOverviewRuler.findMatchForeground': a(v.warning, 0.6),
      'scrollbar.shadow': '#00000000',
      'scrollbarSlider.background': a(v.muted, 0.22),
      'scrollbarSlider.hoverBackground': a(v.muted, 0.38),
      'scrollbarSlider.activeBackground': a(v.muted, 0.55),
      'editorWidget.background': v.bg3,
      'editorWidget.border': v.border,
      'editorWidget.foreground': v.text,
      'editorHoverWidget.background': v.bg3,
      'editorHoverWidget.border': v.border,
      'editorSuggestWidget.background': v.bg3,
      'editorSuggestWidget.border': v.border,
      'editorSuggestWidget.selectedBackground': v.active,
      'editorSuggestWidget.highlightForeground': v.accent,
      'input.background': v.bg,
      'input.foreground': v.text,
      'input.border': v.border,
      'input.placeholderForeground': v.muted,
      'inputOption.activeBackground': a(v.accent, 0.25),
      'inputOption.activeBorder': v.accent,
      'focusBorder': v.accent,
      'list.hoverBackground': v.hover,
      'list.activeSelectionBackground': v.active,
      'list.focusBackground': v.active,
      'widget.shadow': a('#000000', dark ? 0.35 : 0.12),
      'diffEditor.insertedTextBackground': a(v.success, 0.18),
      'diffEditor.removedTextBackground': a(v.danger, 0.18),
      'diffEditor.insertedLineBackground': a(v.success, 0.08),
      'diffEditor.removedLineBackground': a(v.danger, 0.08),
    },
  };
};

const isDark = () => document.documentElement.getAttribute('data-theme') !== 'light';
let observed: any = null;

export const installKanivetMonacoTheme = (monaco: any) => {
  const apply = () => {
    monaco.editor.defineTheme(KANIVET_MONACO_THEME, buildKanivetMonacoTheme(isDark()));
    monaco.editor.setTheme(KANIVET_MONACO_THEME);
  };
  apply();
  if (observed === monaco) return;
  observed = monaco;
  new MutationObserver(() => requestAnimationFrame(apply)).observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme', 'style'] });
};
