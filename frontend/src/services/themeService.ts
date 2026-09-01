import { getApiBase } from './api/types';
import { wsManager } from './api/websocket';

export interface Theme {
  id: string;
  name: string;
  type: 'built-in' | 'custom';
  colors: Record<string, any>;
  tokenColors?: any[];
  semantic?: Record<string, any>;
  createdAt: string;
  updatedAt: string;
}

export interface ThemeSettings {
  currentMode: 'light' | 'dark' | 'custom';
  currentCustomTheme?: string;
}

export interface MarketplaceTheme {
  name: string;
  displayName: string;
  namespace: string;
  version: string;
  downloadUrl: string;
  downloads: number;
  description?: string;
  publisher?: string;
}

class ThemeService {
  private async getHeaders(): Promise<Record<string, string>> {
    await wsManager.waitForSessionSecret();
    const sessionSecret = wsManager.getSessionSecret();
    return sessionSecret ? { 'X-Session-Secret': sessionSecret } : {};
  }

  async listThemes(): Promise<Theme[]> {
    const headers = await this.getHeaders();
    const response = await fetch(`${getApiBase()}/themes`, { headers });
    if (!response.ok) {
      throw new Error('Failed to fetch themes');
    }
    const data = await response.json();
    return data.themes;
  }

  async getTheme(id: string): Promise<Theme> {
    const headers = await this.getHeaders();
    const response = await fetch(`${getApiBase()}/themes/${id}`, { headers });
    if (!response.ok) {
      throw new Error('Failed to fetch theme');
    }
    const data = await response.json();
    return data.theme;
  }

  async deleteTheme(id: string): Promise<void> {
    const headers = await this.getHeaders();
    const response = await fetch(`${getApiBase()}/themes/${id}`, {
      method: 'DELETE',
      headers,
    });
    if (!response.ok) {
      throw new Error('Failed to delete theme');
    }
  }

  async applyTheme(themeId: string): Promise<void> {
    const headers = await this.getHeaders();
    const response = await fetch(`${getApiBase()}/themes/apply`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        ...headers,
      },
      body: JSON.stringify({ themeId }),
    });
    if (!response.ok) throw new Error('Failed to apply theme');
  }

  async getSettings(): Promise<ThemeSettings> {
    const headers = await this.getHeaders();
    const response = await fetch(`${getApiBase()}/themes/current`, { headers });
    if (!response.ok) {
      throw new Error('Failed to fetch theme settings');
    }
    const data = await response.json();
    return data.settings;
  }

  async searchMarketplaceThemes(query?: string, limit = 50): Promise<MarketplaceTheme[]> {
    const params = new URLSearchParams();
    if (query) params.set('q', query);
    params.set('limit', limit.toString());

    const headers = await this.getHeaders();
    const response = await fetch(`${getApiBase()}/themes/marketplace?${params}`, { headers });
    if (!response.ok) {
      throw new Error('Failed to search marketplace themes');
    }
    const data = await response.json();
    return data.themes;
  }

  async downloadAndInstallTheme(
    marketplaceTheme: MarketplaceTheme,
  ): Promise<Theme> {
    const headers = await this.getHeaders();
    const response = await fetch(`${getApiBase()}/themes/download`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        ...headers,
      },
      body: JSON.stringify({ marketplaceTheme }),
    });
    if (!response.ok) throw new Error('Failed to download theme');
    return (await response.json()).theme;
  }
}

export const themeService = new ThemeService();
