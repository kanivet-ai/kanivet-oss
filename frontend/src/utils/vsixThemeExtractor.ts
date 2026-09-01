import JSZip from 'jszip';
import { VSCodeTheme } from './themeAdapter';

/**
 * Cleans JSON content by removing comments and handling JSONC files
 */
function cleanJsonContent(content: string): string {
  // Remove single line comments
  content = content.replace(/\/\/.*$/gm, '');

  // Remove multi-line comments
  content = content.replace(/\/\*[\s\S]*?\*\//g, '');

  // Remove trailing commas
  content = content.replace(/,(\s*[}\]])/g, '$1');

  return content;
}

/**
 * Extracts theme files from a VSIX archive
 */
async function extractThemesFromVSIX(
  vsixData: ArrayBuffer,
): Promise<VSCodeTheme[]> {
  const zip = new JSZip();
  const archive = await zip.loadAsync(vsixData);

  // First, get the package.json to understand the structure
  const packageJsonFile = archive.file('extension/package.json');
  if (!packageJsonFile) {
    throw new Error('Invalid VSIX: no package.json found');
  }

  const packageJsonContent = await packageJsonFile.async('string');
  const packageJson = JSON.parse(packageJsonContent);

  // Find theme contributions
  const themes: VSCodeTheme[] = [];
  if (packageJson.contributes?.themes) {
    for (const themeContrib of packageJson.contributes.themes) {
      const themePath = `extension/${themeContrib.path}`;
      const themeFile = archive.file(themePath);

      if (themeFile) {
        const themeContent = await themeFile.async('string');
        const cleanContent = cleanJsonContent(themeContent);
        const theme = JSON.parse(cleanContent);

        // Ensure the theme has a name
        if (!theme.name) {
          theme.name =
            themeContrib.label || themeContrib.id || 'Untitled Theme';
        }

        themes.push(theme);
      }
    }
  }

  return themes;
}

/**
 * Imports theme from any supported URL
 */
export async function importThemeFromUrl(url: string): Promise<VSCodeTheme> {
  // Check if it's a blob URL (from file upload)
  if (url.startsWith('blob:')) {
    try {
      const response = await fetch(url);
      const arrayBuffer = await response.arrayBuffer();

      // Try to extract as VSIX first
      try {
        const themes = await extractThemesFromVSIX(arrayBuffer);
        if (themes.length > 0) {
          return themes[0];
        }
      } catch {
        // If not a VSIX, try as JSON
        const text = new TextDecoder().decode(arrayBuffer);
        const cleanJson = cleanJsonContent(text);
        return JSON.parse(cleanJson);
      }
    } catch (error) {
      console.error('Failed to process blob URL:', error);
      throw new Error('Failed to process uploaded file');
    }
  }

  // Check if it's a direct JSON URL
  if (url.endsWith('.json') || url.endsWith('.jsonc')) {
    try {
      const response = await fetch(url);
      if (response.ok) {
        const text = await response.text();
        const cleanJson = cleanJsonContent(text);
        return JSON.parse(cleanJson);
      }
    } catch (error) {
      console.error('Failed to fetch direct JSON:', error);
    }
  }

  // Check if it's an open-vsx.org URL
  if (url.includes('open-vsx.org')) {
    throw new Error('Open-VSX import is no longer supported');
  }

  // Check if it's a GitHub URL
  if (url.includes('github.com')) {
    // Convert github.com URL to raw.githubusercontent.com
    const rawUrl = url
      .replace('github.com', 'raw.githubusercontent.com')
      .replace('/blob/', '/');

    try {
      const response = await fetch(rawUrl);
      if (response.ok) {
        const text = await response.text();
        const cleanJson = cleanJsonContent(text);
        return JSON.parse(cleanJson);
      }
    } catch (error) {
      console.error('Failed to fetch from GitHub:', error);
    }
  }

  // Last resort: try to fetch as-is (for direct raw URLs)
  try {
    const response = await fetch(url);
    if (response.ok) {
      const text = await response.text();
      const cleanJson = cleanJsonContent(text);
      return JSON.parse(cleanJson);
    }
  } catch (error) {
    console.error('Failed to fetch as direct URL:', error);
  }

  throw new Error(
    'Unable to import theme from the provided URL. Supported: GitHub, or direct JSON URLs',
  );
}
