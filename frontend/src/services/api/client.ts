import axios, { AxiosAdapter, AxiosInstance, InternalAxiosRequestConfig } from 'axios';
import logger from '../../utils/logger';
import { getApiBase, CacheEntry } from './types';
import { wsManager } from './websocket';

declare module 'axios' {
  interface AxiosRequestConfig {
    /** The request can hold its connection for long: it waits for one of
     * the LONG_REQUEST_SLOTS instead of taking an interactive one. */
    long?: boolean;
  }
}

// Chromium opens at most six HTTP/1.1 connections per host, and every API
// call goes to the one backend host. A request that can run for long (a
// workload's evidence worked out from history, metrics provider detection,
// a pod's metric history) holds its connection all that time: six of them
// froze every other call, and a fast one waited 7.7 s. Requests marked
// `long` share two connections, first come first served, so four always
// stay free for the rest. A slot is held until the request settles, so each
// one needs a timeout; it starts once the request is sent, not while it waits.
//
// The streamed dashboards (sseFetch) are not counted. Each is the view on
// screen loading, aborted when it closes, and it ends once the backend has
// sent it (the cluster dashboard within 15 s, FinOps once its cached
// dashboard is computed), so queueing one behind a slow evidence refresh
// would only hold back what the user is looking at. WebSockets have a pool
// of their own.
export const LONG_REQUEST_SLOTS = 2;

/** A first-come, first-served gate with `slots` holders at a time. A
 * waiter whose signal aborts leaves the queue without ever holding a slot. */
export class RequestGate {
  private holders = 0;
  private readonly queue: (() => void)[] = [];

  constructor(private readonly slots: number) {}

  /** Resolves with the function that gives the slot back. */
  acquire(signal?: AbortSignal): Promise<() => void> {
    return new Promise((resolve, reject) => {
      if (signal?.aborted) return reject(signal.reason);
      const grant = () => {
        signal?.removeEventListener('abort', onAbort);
        this.holders++;
        let held = true;
        resolve(() => {
          if (!held) return;
          held = false;
          this.holders--;
          this.queue.shift()?.();
        });
      };
      const onAbort = () => {
        const i = this.queue.indexOf(grant);
        if (i >= 0) this.queue.splice(i, 1);
        reject(signal?.reason);
      };
      if (this.holders < this.slots) return grant();
      signal?.addEventListener('abort', onAbort, { once: true });
      this.queue.push(grant);
    });
  }

  /** Runs task once it holds a slot, and gives the slot back however it ends. */
  async run<T>(task: () => Promise<T>, signal?: AbortSignal): Promise<T> {
    const release = await this.acquire(signal);
    try {
      return await task();
    } finally {
      release();
    }
  }
}

const longRequests = new RequestGate(LONG_REQUEST_SLOTS);

/** An adapter that sends a `long` request only once it holds a slot of the
 * gate, for as long as it is on the wire. One aborted while it waits is
 * never sent: axios reports it as canceled, as its signal has aborted. */
export function gateLongRequests(send: AxiosAdapter, gate: RequestGate): AxiosAdapter {
  return (config) =>
    config.long ? gate.run(() => send(config), config.signal as AbortSignal | undefined) : send(config);
}

class ApiClient {
  private client: AxiosInstance;
  private cache: Map<string, CacheEntry> = new Map();
  private inflight: Map<string, Promise<any>> = new Map();
  private sessionRefreshInProgress: Promise<boolean> | null = null;

  constructor() {
    this.client = axios.create({
      adapter: gateLongRequests((config) => axios.getAdapter(axios.defaults.adapter)(config), longRequests),
    });
    this.setupInterceptors();
  }

  private setupInterceptors() {
    this.client.interceptors.request.use(async (config: InternalAxiosRequestConfig) => {
      (config as any).__startTime = performance.now();
      await wsManager.waitForSessionSecret();
      config.baseURL = getApiBase();
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
    this.inflight.clear();
  }

  invalidateCachePattern(pattern: string) {
    const keysToDelete: string[] = [];
    for (const key of this.cache.keys()) {
      if (key.includes(pattern)) keysToDelete.push(key);
    }
    for (const key of keysToDelete) this.cache.delete(key);
    this.forgetInflight(pattern);
  }

  invalidateCache(pattern?: string): void {
    if (!pattern) {
      this.cache.clear();
      this.inflight.clear();
      return;
    }
    for (const key of this.cache.keys()) {
      if (key.includes(pattern)) this.cache.delete(key);
    }
    this.forgetInflight(pattern);
  }

  // A request started before an invalidation must not answer callers that
  // come after it: they get a fresh round trip instead.
  private forgetInflight(pattern: string) {
    for (const key of this.inflight.keys()) {
      if (key.includes(pattern)) this.inflight.delete(key);
    }
  }

  async request(endpoint: string, params?: any, useCache: boolean = true, signal?: AbortSignal): Promise<any> {
    const cacheKey = this.getCacheKey(endpoint, params);
    if (useCache && this.cache.has(cacheKey)) {
      const cached = this.cache.get(cacheKey)!;
      if (Date.now() - cached.timestamp < 60000) return cached.data;
    }
    // Identical cacheable GETs in flight share one round trip: on startup the
    // hydrate step, the sidebar and the restore effect all ask for the same
    // tree, and every duplicate holds one of the six sockets Chromium allows
    // per host. useCache=false callers (fresh reads after a write) and
    // requests with their own abort signal always go out on their own.
    if (!useCache || signal) {
      return this.fetchAndCache(endpoint, params, useCache, cacheKey, signal);
    }
    const pending = this.inflight.get(cacheKey);
    if (pending) return pending;
    const request = this.fetchAndCache(endpoint, params, true, cacheKey);
    const forget = () => {
      if (this.inflight.get(cacheKey) === request) {
        this.inflight.delete(cacheKey);
      }
    };
    request.then(forget, forget);
    this.inflight.set(cacheKey, request);
    return request;
  }

  private async fetchAndCache(
    endpoint: string,
    params: any,
    useCache: boolean,
    cacheKey: string,
    signal?: AbortSignal,
  ): Promise<any> {
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
