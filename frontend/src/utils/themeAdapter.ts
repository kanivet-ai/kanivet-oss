interface VSCodeTokenColor {
  scope: string | string[];
  settings: {
    foreground?: string;
    background?: string;
    fontStyle?: string;
  };
}

export interface VSCodeTheme {
  name: string;
  type?: 'dark' | 'light';
  colors: Record<string, string>;
  tokenColors?: VSCodeTokenColor[];
}

export interface kanivetTheme {
  name: string;
  type: 'dark' | 'light';
  colors: Record<string, string>;
  tokenColors?: VSCodeTokenColor[];
  kubernetesColors?: Record<string, string>;
}

const VSCODE_TO_kanivet_MAP: Record<string, string> = {
  'editor.background': 'bg-primary',
  'editor.foreground': 'text-primary',
  'editorWidget.background': 'bg-secondary',
  'editorGroupHeader.tabsBackground': 'bg-secondary',
  'sideBar.background': 'bg-secondary',
  'sideBar.foreground': 'text-primary',
  'sideBar.border': 'border',
  'sideBarSectionHeader.background': 'bg-tertiary',
  'list.activeSelectionBackground': 'bg-active',
  'list.hoverBackground': 'bg-hover',
  'list.inactiveSelectionBackground': 'bg-tertiary',
  'list.highlightForeground': 'accent',
  'activityBar.background': 'bg-secondary',
  'activityBar.foreground': 'text-primary',
  'activityBar.activeBorder': 'accent',
  'statusBar.background': 'bg-secondary',
  'statusBar.foreground': 'text-secondary',
  'input.background': 'bg-tertiary',
  'input.foreground': 'text-primary',
  'input.border': 'border',
  focusBorder: 'accent',
  'button.background': 'accent',
  'button.foreground': 'text-primary',
  'button.hoverBackground': 'accent-hover',
  'dropdown.background': 'bg-tertiary',
  'dropdown.border': 'border',
  'terminal.ansiGreen': 'success',
  'terminal.ansiYellow': 'warning',
  'terminal.ansiRed': 'danger',
  'terminal.ansiBlue': 'accent',
  'terminal.ansiBrightGreen': 'success',
  'terminal.ansiBrightYellow': 'warning',
  'terminal.ansiBrightRed': 'danger',
  'terminal.ansiBrightBlue': 'accent',
  'editorError.foreground': 'danger',
  'editorWarning.foreground': 'warning',
  'editorInfo.foreground': 'accent',
  'notificationsErrorIcon.foreground': 'danger',
  'notificationsWarningIcon.foreground': 'warning',
  'notificationsInfoIcon.foreground': 'accent',
  'badge.background': 'accent',
  'badge.foreground': 'text-primary',
  'progressBar.background': 'accent',
  'editorCursor.foreground': 'cursor',
  'selection.background': 'selection-bg',
  'editor.selectionBackground': 'selection-bg',
  'editor.inactiveSelectionBackground': 'selection-inactive',
  'editorLineNumber.foreground': 'line-number',
  'editorLineNumber.activeForeground': 'line-number-active',
  'editorIndentGuide.background': 'indent-guide',
  'editorIndentGuide.activeBackground': 'indent-guide-active',
  'panel.background': 'bg-panel',
  'panel.border': 'border',
  'titleBar.activeBackground': 'bg-titlebar',
  'titleBar.activeForeground': 'text-titlebar',
  'titleBar.inactiveBackground': 'bg-titlebar-inactive',
  'tab.activeBackground': 'bg-tab-active',
  'tab.inactiveBackground': 'bg-tab-inactive',
  'tab.activeForeground': 'text-tab-active',
  'tab.inactiveForeground': 'text-tab-inactive',
  'scrollbar.shadow': 'shadow',
  'scrollbarSlider.background': 'scrollbar-bg',
  'scrollbarSlider.hoverBackground': 'scrollbar-hover',
};

function hexToRgb(hex: string): string {
  try {
    const cleanHex = hex.replace('#', '');
    if (!/^[0-9A-F]{6}$/i.test(cleanHex)) {
      return '0, 0, 0';
    }
    const result = /^([a-f\d]{2})([a-f\d]{2})([a-f\d]{2})$/i.exec(cleanHex);
    if (!result) return '0, 0, 0';
    return `${parseInt(result[1], 16)}, ${parseInt(result[2], 16)}, ${parseInt(
      result[3],
      16,
    )}`;
  } catch {
    return '0, 0, 0';
  }
}

function hexToHsl(hex: string): { h: number; s: number; l: number } {
  const rgb = hexToRgb(hex).split(', ').map(Number);
  const r = rgb[0] / 255;
  const g = rgb[1] / 255;
  const b = rgb[2] / 255;

  const max = Math.max(r, g, b);
  const min = Math.min(r, g, b);
  let h = 0,
    s = 0,
    l = (max + min) / 2;

  if (max !== min) {
    const d = max - min;
    s = l > 0.5 ? d / (2 - max - min) : d / (max + min);
    switch (max) {
      case r:
        h = ((g - b) / d + (g < b ? 6 : 0)) / 6;
        break;
      case g:
        h = ((b - r) / d + 2) / 6;
        break;
      case b:
        h = ((r - g) / d + 4) / 6;
        break;
    }
  }

  return { h: h * 360, s: s * 100, l: l * 100 };
}

function hslToHex(h: number, s: number, l: number): string {
  h /= 360;
  s /= 100;
  l /= 100;

  let r, g, b;

  if (s === 0) {
    r = g = b = l;
  } else {
    const hue2rgb = (p: number, q: number, t: number) => {
      if (t < 0) t += 1;
      if (t > 1) t -= 1;
      if (t < 1 / 6) return p + (q - p) * 6 * t;
      if (t < 1 / 2) return q;
      if (t < 2 / 3) return p + (q - p) * (2 / 3 - t) * 6;
      return p;
    };

    const q = l < 0.5 ? l * (1 + s) : l + s - l * s;
    const p = 2 * l - q;
    r = hue2rgb(p, q, h + 1 / 3);
    g = hue2rgb(p, q, h);
    b = hue2rgb(p, q, h - 1 / 3);
  }

  const toHex = (x: number) => {
    const hex = Math.round(x * 255).toString(16);
    return hex.length === 1 ? '0' + hex : hex;
  };

  return `#${toHex(r)}${toHex(g)}${toHex(b)}`;
}

function isValidHexColor(color: string): boolean {
  return /^#?[0-9A-F]{6}$/i.test(color.replace('#', ''));
}

function getColorLuminance(hex: string): number {
  const rgb = hexToRgb(hex).split(', ').map(Number);
  return (0.299 * rgb[0] + 0.587 * rgb[1] + 0.114 * rgb[2]) / 255;
}

function adjustColorBrightness(hex: string, amount: number): string {
  const hsl = hexToHsl(hex);
  hsl.l = Math.max(0, Math.min(100, hsl.l + amount * 100));
  return hslToHex(hsl.h, hsl.s, hsl.l);
}

function mixColors(
  color1: string,
  color2: string,
  weight: number = 0.5,
): string {
  const rgb1 = hexToRgb(color1).split(', ').map(Number);
  const rgb2 = hexToRgb(color2).split(', ').map(Number);

  const r = Math.round(rgb1[0] * weight + rgb2[0] * (1 - weight));
  const g = Math.round(rgb1[1] * weight + rgb2[1] * (1 - weight));
  const b = Math.round(rgb1[2] * weight + rgb2[2] * (1 - weight));

  return `#${[r, g, b].map((x) => x.toString(16).padStart(2, '0')).join('')}`;
}

interface TokenColorPriority {
  color: string;
  priority: number;
}

function deriveUIColorsFromTokens(
  tokenColors: any[],
  isDark: boolean,
): Record<string, string> {
  const colors: Record<string, string> = {};
  const colorCandidates: Record<string, TokenColorPriority[]> = {
    accent: [],
    success: [],
    danger: [],
    warning: [],
    'text-muted': [],
    'text-primary': [],
    'bg-primary': [],
  };

  colors['bg-primary'] = isDark ? '#1e1e1e' : '#ffffff';
  colors['bg-secondary'] = isDark ? '#252526' : '#f3f3f3';
  colors['bg-tertiary'] = isDark ? '#2d2d30' : '#ececec';
  colors['text-primary'] = isDark ? '#cccccc' : '#333333';
  colors['text-secondary'] = isDark ? '#969696' : '#666666';
  colors['border'] = isDark ? '#464647' : '#e1e1e1';

  const scopePriorities: Record<
    string,
    { target: string; priority: number }[]
  > = {
    keyword: [{ target: 'accent', priority: 10 }],
    'keyword.control': [{ target: 'accent', priority: 12 }],
    'keyword.operator': [{ target: 'accent', priority: 8 }],
    'entity.name.function': [{ target: 'accent', priority: 9 }],
    'entity.name.type': [{ target: 'accent', priority: 8 }],
    'entity.name.class': [{ target: 'accent', priority: 8 }],
    'support.function': [{ target: 'accent', priority: 7 }],
    'support.class': [{ target: 'accent', priority: 7 }],
    'variable.language': [{ target: 'accent', priority: 6 }],
    string: [{ target: 'success', priority: 10 }],
    'string.quoted': [{ target: 'success', priority: 11 }],
    'constant.numeric': [{ target: 'success', priority: 8 }],
    'constant.language.boolean': [{ target: 'success', priority: 9 }],
    invalid: [{ target: 'danger', priority: 10 }],
    'invalid.illegal': [{ target: 'danger', priority: 12 }],
    'invalid.deprecated': [{ target: 'warning', priority: 10 }],
    warning: [{ target: 'warning', priority: 11 }],
    comment: [{ target: 'text-muted', priority: 10 }],
    'comment.line': [{ target: 'text-muted', priority: 11 }],
    'comment.block': [{ target: 'text-muted', priority: 11 }],
    'punctuation.definition.comment': [{ target: 'text-muted', priority: 9 }],
  };

  for (const token of tokenColors) {
    if (!token.settings) continue;

    if (token.scope === undefined) {
      if (
        token.settings.background &&
        isValidHexColor(token.settings.background)
      ) {
        colorCandidates['bg-primary'].push({
          color: token.settings.background,
          priority: 15,
        });
      }
      if (
        token.settings.foreground &&
        isValidHexColor(token.settings.foreground)
      ) {
        colorCandidates['text-primary'].push({
          color: token.settings.foreground,
          priority: 15,
        });
      }
      continue;
    }

    const scopes = Array.isArray(token.scope) ? token.scope : [token.scope];
    for (const scope of scopes) {
      if (!scope || !token.settings.foreground) continue;

      for (const [pattern, targets] of Object.entries(scopePriorities)) {
        if (scope.includes(pattern) || scope === pattern) {
          for (const { target, priority } of targets) {
            if (isValidHexColor(token.settings.foreground)) {
              colorCandidates[target].push({
                color: token.settings.foreground,
                priority,
              });
            }
          }
        }
      }
    }
  }

  for (const [key, candidates] of Object.entries(colorCandidates)) {
    if (candidates.length > 0) {
      candidates.sort((a, b) => b.priority - a.priority);
      colors[key] = candidates[0].color;
    }
  }

  if (!colors['bg-hover']) {
    colors['bg-hover'] = isDark
      ? adjustColorBrightness(colors['bg-secondary'], 0.05)
      : adjustColorBrightness(colors['bg-secondary'], -0.05);
  }

  if (!colors['accent']) {
    colors['accent'] = isDark ? '#007acc' : '#0078d4';
  }

  return colors;
}

function detectThemeType(theme: any): 'dark' | 'light' {
  if (theme.type === 'dark' || theme.type === 'light') {
    return theme.type;
  }

  if (theme.uiTheme === 'vs-dark' || theme.uiTheme === 'hc-black') {
    return 'dark';
  }

  if (theme.uiTheme === 'vs' || theme.uiTheme === 'hc-light') {
    return 'light';
  }

  const themeName = (
    theme.name ||
    theme.displayName ||
    theme.label ||
    ''
  ).toLowerCase();
  if (
    themeName.includes('dark') ||
    themeName.includes('night') ||
    themeName.includes('black')
  ) {
    return 'dark';
  }

  if (themeName.includes('light') || themeName.includes('white')) {
    return 'light';
  }

  if (theme.colors?.['editor.background']) {
    const bgLuminance = getColorLuminance(theme.colors['editor.background']);
    return bgLuminance < 0.5 ? 'dark' : 'light';
  }

  if (theme.tokenColors?.length > 0) {
    for (const token of theme.tokenColors) {
      if (!token.scope && token.settings?.background) {
        const bgLuminance = getColorLuminance(token.settings.background);
        return bgLuminance < 0.5 ? 'dark' : 'light';
      }
    }
  }

  return 'dark';
}

export function convertVSCodeTheme(vscodeTheme: any): kanivetTheme {
  try {
    const colors: Record<string, string> = {};
    const themeName =
      vscodeTheme.name ||
      vscodeTheme.displayName ||
      vscodeTheme.label ||
      'Imported Theme';
    const isDark = detectThemeType(vscodeTheme) === 'dark';

    if (vscodeTheme.colors) {
      for (const [vsKey, kanivetKey] of Object.entries(VSCODE_TO_kanivet_MAP)) {
        if (
          vscodeTheme.colors[vsKey] &&
          isValidHexColor(vscodeTheme.colors[vsKey])
        ) {
          colors[kanivetKey] = vscodeTheme.colors[vsKey];
        }
      }
    }

    if (
      (!vscodeTheme.colors || Object.keys(colors).length < 5) &&
      vscodeTheme.tokenColors
    ) {
      const derivedColors = deriveUIColorsFromTokens(
        vscodeTheme.tokenColors,
        isDark,
      );
      Object.assign(colors, derivedColors);
    }

    const primaryBg = colors['bg-primary'] || (isDark ? '#1e1e1e' : '#ffffff');
    const primaryText =
      colors['text-primary'] || (isDark ? '#d4d4d4' : '#333333');

    if (!colors['bg-secondary']) {
      colors['bg-secondary'] = isDark
        ? adjustColorBrightness(primaryBg, 0.08)
        : adjustColorBrightness(primaryBg, -0.04);
    }

    if (!colors['bg-tertiary']) {
      colors['bg-tertiary'] = isDark
        ? adjustColorBrightness(primaryBg, 0.12)
        : adjustColorBrightness(primaryBg, -0.08);
    }

    if (!colors['bg-hover']) {
      colors['bg-hover'] = isDark
        ? adjustColorBrightness(colors['bg-secondary'], 0.05)
        : adjustColorBrightness(colors['bg-secondary'], -0.05);
    }

    if (!colors['bg-active']) {
      colors['bg-active'] = colors['accent']
        ? mixColors(colors['accent'], colors['bg-primary'], 0.2)
        : isDark
          ? '#007acc40'
          : '#0078d440';
    }

    if (!colors['text-secondary']) {
      colors['text-secondary'] = mixColors(
        primaryText,
        colors['bg-primary'],
        0.6,
      );
    }

    if (!colors['text-muted']) {
      colors['text-muted'] = mixColors(primaryText, colors['bg-primary'], 0.4);
    }

    if (!colors['border']) {
      colors['border'] = isDark
        ? adjustColorBrightness(primaryBg, 0.15)
        : adjustColorBrightness(primaryBg, -0.12);
    }

    if (!colors['accent']) {
      colors['accent'] = isDark ? '#007acc' : '#0078d4';
    }

    if (!colors['accent-hover']) {
      colors['accent-hover'] = isDark
        ? adjustColorBrightness(colors['accent'], 0.1)
        : adjustColorBrightness(colors['accent'], -0.1);
    }

    if (!colors['cursor']) {
      colors['cursor'] = colors['accent'];
    }

    if (!colors['selection-bg']) {
      colors['selection-bg'] = colors['accent'] + '30';
    }

    if (!colors['selection-inactive']) {
      colors['selection-inactive'] = colors['accent'] + '20';
    }

    if (!colors['line-number']) {
      colors['line-number'] = colors['text-muted'];
    }

    if (!colors['line-number-active']) {
      colors['line-number-active'] = colors['text-secondary'];
    }

    if (!colors['indent-guide']) {
      colors['indent-guide'] = colors['border'];
    }

    if (!colors['indent-guide-active']) {
      colors['indent-guide-active'] = colors['text-muted'];
    }

    if (!colors['bg-panel']) {
      colors['bg-panel'] = colors['bg-secondary'];
    }

    if (!colors['bg-titlebar']) {
      colors['bg-titlebar'] = colors['bg-secondary'];
    }

    if (!colors['text-titlebar']) {
      colors['text-titlebar'] = colors['text-primary'];
    }

    if (!colors['bg-titlebar-inactive']) {
      colors['bg-titlebar-inactive'] = colors['bg-tertiary'];
    }

    if (!colors['bg-tab-active']) {
      colors['bg-tab-active'] = colors['bg-primary'];
    }

    if (!colors['bg-tab-inactive']) {
      colors['bg-tab-inactive'] = colors['bg-secondary'];
    }

    if (!colors['text-tab-active']) {
      colors['text-tab-active'] = colors['text-primary'];
    }

    if (!colors['text-tab-inactive']) {
      colors['text-tab-inactive'] = colors['text-secondary'];
    }

    if (!colors['shadow']) {
      colors['shadow'] = '#00000033';
    }

    if (!colors['scrollbar-bg']) {
      colors['scrollbar-bg'] = colors['text-muted'] + '30';
    }

    if (!colors['scrollbar-hover']) {
      colors['scrollbar-hover'] = colors['text-muted'] + '50';
    }

    if (colors['accent']) {
      colors['accent-rgb'] = hexToRgb(colors['accent']);
    }

    colors['success-fg'] = colors['success'] || '#16a34a';
    colors['success-bg'] = colors['success-fg'] + '26';
    colors['warning-fg'] = colors['warning'] || '#d97706';
    colors['warning-bg'] = colors['warning-fg'] + '26';
    colors['danger-fg'] = colors['danger'] || '#dc2626';
    colors['danger-bg'] = colors['danger-fg'] + '26';
    colors['info-fg'] = colors['accent'] || '#2563eb';
    colors['info-bg'] = colors['info-fg'] + '26';

    colors['icon-muted'] = colors['text-muted'] || '#6e7781';
    colors['icon-mid'] = colors['accent'] || '#3b82f6';
    colors['icon-top'] = adjustColorBrightness(
      colors['accent'] || '#2563eb',
      -0.1,
    );
    colors['icon-low'] = adjustColorBrightness(
      colors['accent'] || '#60a5fa',
      0.1,
    );

    colors['surface-elev'] = isDark
      ? adjustColorBrightness(primaryBg, -0.05)
      : adjustColorBrightness(primaryBg, 0.02);

    return {
      name: themeName,
      type: isDark ? 'dark' : 'light',
      colors,
      tokenColors: vscodeTheme.tokenColors,
      kubernetesColors: {
        'pod.running': colors['success'] || '#16a34a',
        'pod.pending': colors['warning'] || '#d97706',
        'pod.failed': colors['danger'] || '#dc2626',
        'pod.unknown': colors['text-muted'] || '#6e7781',
        'namespace.border': colors['accent'] || '#2563eb',
        'container.ready': colors['success'] || '#16a34a',
        'container.notReady': colors['danger'] || '#dc2626',
      },
    };
  } catch (error) {
    console.error('Error converting VSCode theme:', error);
    return {
      name: 'Fallback Theme',
      type: 'dark',
      colors: {
        'bg-primary': '#1e1e1e',
        'bg-secondary': '#252526',
        'bg-tertiary': '#2d2d30',
        'text-primary': '#d4d4d4',
        'text-secondary': '#969696',
        border: '#464647',
        accent: '#007acc',
      },
      kubernetesColors: {
        'pod.running': '#16a34a',
        'pod.pending': '#d97706',
        'pod.failed': '#dc2626',
        'pod.unknown': '#6e7781',
        'namespace.border': '#2563eb',
        'container.ready': '#16a34a',
        'container.notReady': '#dc2626',
      },
    };
  }
}

export function applyTheme(theme: kanivetTheme): void {
  const root = document.documentElement;

  for (const [key, value] of Object.entries(theme.colors)) {
    root.style.setProperty(`--${key}`, value);
  }

  root.setAttribute('data-theme', theme.type);
  root.style.colorScheme = theme.type;

  if (theme.kubernetesColors) {
    for (const [key, value] of Object.entries(theme.kubernetesColors)) {
      root.style.setProperty(`--k8s-${key.replace('.', '-')}`, value);
    }
  }

  if (window.monaco && theme.tokenColors) {
    window.monaco.editor.defineTheme('custom', {
      base: theme.type === 'dark' ? 'vs-dark' : 'vs',
      inherit: true,
      rules: theme.tokenColors
        .map((token) => ({
          token: Array.isArray(token.scope) ? token.scope[0] : token.scope,
          foreground: token.settings.foreground?.replace('#', ''),
          background: token.settings.background?.replace('#', ''),
          fontStyle: token.settings.fontStyle,
        }))
        .filter((r) => r.token),
      colors: Object.fromEntries(
        Object.entries(theme.colors).map(([k, v]) => [`editor.${k}`, v]),
      ),
    });
    window.monaco.editor.setTheme('custom');
  }
}

export function validateTheme(theme: unknown): theme is VSCodeTheme {
  try {
    if (typeof theme !== 'object' || theme === null) {
      return false;
    }

    const t = theme as any;

    const hasName =
      typeof t.name === 'string' ||
      typeof t.displayName === 'string' ||
      typeof t.label === 'string';

    if (!hasName) {
      return false;
    }

    const hasValidType =
      !t.type ||
      t.type === 'dark' ||
      t.type === 'light' ||
      t.uiTheme === 'vs' ||
      t.uiTheme === 'vs-dark' ||
      t.uiTheme === 'hc-black' ||
      t.uiTheme === 'hc-light';

    if (!hasValidType) {
      return false;
    }

    const hasColorData =
      (typeof t.colors === 'object' && t.colors !== null) ||
      (Array.isArray(t.tokenColors) && t.tokenColors.length > 0);

    if (!hasColorData) {
      return false;
    }

    if (t.colors) {
      for (const value of Object.values(t.colors)) {
        if (typeof value !== 'string') {
          return false;
        }
      }
    }

    if (t.tokenColors) {
      for (const token of t.tokenColors) {
        if (typeof token !== 'object' || !token.settings) {
          continue;
        }
        if (
          token.settings.foreground &&
          typeof token.settings.foreground !== 'string'
        ) {
          return false;
        }
        if (
          token.settings.background &&
          typeof token.settings.background !== 'string'
        ) {
          return false;
        }
      }
    }

    return true;
  } catch {
    return false;
  }
}
