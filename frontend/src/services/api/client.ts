import axios, { AxiosInstance, InternalAxiosRequestConfig } from 'axios';
import logger from '../../utils/logger';
import { getApiBase, CacheEntry } from './types';
import { wsManager } from './websocket';

class ApiClient {
  private client: AxiosInstance;
  private cache: Map<string, CacheEntry> = new Map();
  private sessionRefreshInProgress: Promise<boolean> | null = null;

  constructor() {
    this.client = axios.create();
    this.setupInterceptors();
  }

  private setupInterceptors() {
    this.client.interceptors.request.use(async (config: InternalAxiosRequestConfig) => {
      (config as any).__startTime = performance.now();
      config.baseURL = getApiBase();
      await wsManager.waitForSessionSecret();
      const sessionSecret = wsManager.getSessionSecret();
      if (sessionSecret) config.headers['X-Session-Secret'] = sessionSecret;
      return config;
    });

    this.client.interceptors.response.use(
      (response) => {
        const endTime = performance.now();
        const startTime = (response.config as any).__startTime || endTime;
        const duration = endTime - startTime;
        if (!(window as any).__apiPerformance) (window as any).__apiPerformance = [];
        (window as any).__apiPerformance.push(duration);
        if ((window as any).__apiPerformance.length > 100) (window as any).__apiPerformance.shift();
        if (response.headers['x-cache-hit'] === 'true') {
          (window as any).__cacheHits = ((window as any).__cacheHits || 0) + 1;
        } else {
          (window as any).__cacheMisses = ((window as any).__cacheMisses || 0) + 1;
        }
        return response;
      },
      async (error) => {
        if (error.response?.status === 403) {
          const errorData = error.response?.data;
          const refreshRequired = error.response?.headers?.['x-session-refresh-required'] === 'true';

          if (errorData?.error === 'session_secret_mismatch' || errorData?.error === 'session_secret_missing' || refreshRequired) {
            logger.warn('Session secret issue detected, attempting recovery', { error: errorData?.error });

            if (!this.sessionRefreshInProgress) {
              this.sessionRefreshInProgress = wsManager.refreshSessionSecret().finally(() => {
                this.sessionRefreshInProgress = null;
              });
            }

            const refreshed = await this.sessionRefreshInProgress;
            if (refreshed && error.config && !(error.config as any).__sessionRetried) {
              logger.info('Session secret refreshed, retrying request');
              (error.config as any).__sessionRetried = true;
              const newSecret = wsManager.getSessionSecret();
              if (newSecret) {
                error.config.headers['X-Session-Secret'] = newSecret;
              }
              return this.client.request(error.config);
            }

            logger.error('Session recovery failed');
            window.dispatchEvent(new CustomEvent('toast:error', {
              detail: { message: 'Local session recovery failed. Restart Kanivet.' }
            }));
            window.dispatchEvent(new CustomEvent('session:invalid', {
              detail: { message: 'Session recovery failed. Please restart the application.' }
            }));
          } else if (errorData?.error === 'invalid_session') {
            logger.error('Invalid session - legacy error');
            window.dispatchEvent(new CustomEvent('session:invalid', {
              detail: { message: 'Session expired. Please restart the application.' }
            }));
          }
        }
        return Promise.reject(error);
      }
    );
  }

  getAxios(): AxiosInstance {
    return this.client;
  }

  getCacheKey(endpoint: string, params?: any): string {
    return `${endpoint}:${JSON.stringify(params || {})}`;
  }

  clearCache() {
    this.cache.clear();
  }

  invalidateCachePattern(pattern: string) {
    const keysToDelete: string[] = [];
    for (const key of this.cache.keys()) {
      if (key.includes(pattern)) keysToDelete.push(key);
    }
    for (const key of keysToDelete) this.cache.delete(key);
  }

  invalidateCache(pattern?: string): void {
    if (!pattern) {
      this.cache.clear();
      return;
    }
    for (const key of this.cache.keys()) {
      if (key.includes(pattern)) this.cache.delete(key);
    }
  }

  async request(endpoint: string, params?: any, useCache: boolean = true, signal?: AbortSignal): Promise<any> {
    const cacheKey = this.getCacheKey(endpoint, params);
    if (useCache && this.cache.has(cacheKey)) {
      const cached = this.cache.get(cacheKey)!;
      if (Date.now() - cached.timestamp < 60000) return cached.data;
    }
    try {
      logger.debug(`API Request: ${endpoint}`, params);
      const response = await this.client.get(endpoint, { params, signal });
      const data = response.data;
      if (useCache) {
        this.cache.set(cacheKey, { data, timestamp: Date.now() });
        if (this.cache.size > 500) {
          const cutoff = Date.now() - 60000;
          for (const [k, v] of this.cache) {
            if (v.timestamp < cutoff) this.cache.delete(k);
          }
          while (this.cache.size > 500) this.cache.delete(this.cache.keys().next().value as string);
        }
      }
      return data;
    } catch (error: any) {
      if (error.response?.status === 404) {
        logger.warn(`Resource not found: ${endpoint}`, { params });
      } else {
        logger.error(`API request failed: ${endpoint}`, { error: error.message, params });
      }
      throw error;
    }
  }
}

export const apiClient = new ApiClient();
