import logger from '../../utils/logger';
import { getWsBase, getApiBase } from './types';

type MessageHandler = (msg: any) => void;
type ConnectionEventType = 'backend' | 'websocket' | 'cluster';

export class WebSocketManager {
  private ws?: WebSocket;
  private wsHandlers: Map<string, Set<MessageHandler>> = new Map();
  private wsFailureCount: number = 0;
  private wsConnecting: boolean = false;
  private sessionSecret: string | null = null;
  private backendReady: boolean = false;
  private activeClusters: Set<string> = new Set();
  private backendHealthInterval?: NodeJS.Timeout;
  private reconnectCountdownInterval?: NodeJS.Timeout;
  private connectWatchdog?: NodeJS.Timeout;
  private subscribeTimestamps: Map<string, number> = new Map();
  private topicSortPrefs: Map<string, { sortBy: string; sortOrder: string }> = new Map();
  private wasDisconnected: boolean = false;
  private lastBackendState: 'connected' | 'disconnected' = 'disconnected';
  private sessionSecretPromise: Promise<void>;
  private lastHeartbeat: number = 0;
  private heartbeatCheckInterval?: NodeJS.Timeout;

  constructor() {
    this.sessionSecretPromise = this.initSessionSecret();
    this.setupConnectivityListeners();
    this.startBackendHealthCheck();
  }

  private dispatchConnectionEvent(type: ConnectionEventType, state: string, detail?: any) {
    window.dispatchEvent(new CustomEvent('connection:state', { detail: { type, state, ...detail } }));
  }

  private async initSessionSecret() {
    try {
      const electronAPI = (window as any).electronAPI;
      if (electronAPI?.security?.getSessionSecret) {
        this.sessionSecret = await electronAPI.security.getSessionSecret();
        logger.debug('Session secret initialized');
      }
    } catch (error) {
      logger.warn('Failed to get session secret', { error });
    }
  }

  async refreshSessionSecret(): Promise<boolean> {
    try {
      const electronAPI = (window as any).electronAPI;
      if (electronAPI?.security?.getSessionSecret) {
        const newSecret = await electronAPI.security.getSessionSecret();
        if (newSecret && newSecret !== this.sessionSecret) {
          logger.info('Session secret refreshed');
          this.sessionSecret = newSecret;
          return true;
        }
        if (newSecret) {
          this.sessionSecret = newSecret;
          return true;
        }
      }
      return false;
    } catch (error) {
      logger.warn('Failed to refresh session secret', { error });
      return false;
    }
  }

  getSessionSecret(): string | null {
    return this.sessionSecret;
  }

  async waitForSessionSecret(): Promise<void> {
    return this.sessionSecretPromise;
  }

  private startBackendHealthCheck() {
    if (this.backendHealthInterval) clearInterval(this.backendHealthInterval);
    this.backendHealthInterval = setInterval(async () => {
      if (!this.backendReady) return;
      try {
        const response = await fetch(`${getApiBase()}/health`, { method: 'GET', signal: AbortSignal.timeout(5000) });
        if (response.ok) {
          const wasDisconnected = this.lastBackendState === 'disconnected';
          this.lastBackendState = 'connected';
          this.dispatchConnectionEvent('backend', 'connected', { timestamp: Date.now() });
          if (wasDisconnected && (!this.ws || this.ws.readyState !== WebSocket.OPEN)) {
            console.log('[API] Backend recovered, reconnecting WebSocket...');
            this.reconnectWebSocket();
          }
        } else {
          this.lastBackendState = 'disconnected';
          this.dispatchConnectionEvent('backend', 'disconnected');
        }
      } catch {
        this.lastBackendState = 'disconnected';
        this.dispatchConnectionEvent('backend', 'disconnected');
      }
    }, 5000);
  }

  private setupConnectivityListeners() {
    let hiddenTimestamp = 0;
    const STALE_THRESHOLD = 60 * 1000;

    document.addEventListener('visibilitychange', () => {
      if (document.visibilityState === 'hidden') {
        hiddenTimestamp = Date.now();
      } else if (document.visibilityState === 'visible') {
        console.log('[WS] App became visible, checking connection...');
        const wasHiddenFor = Date.now() - hiddenTimestamp;
        if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
          console.log('[WS] Connection lost while hidden, reconnecting...');
          this.wasDisconnected = true;
          this.reconnectWebSocket();
        } else if (wasHiddenFor > STALE_THRESHOLD) {
          console.log(`[WS] App was hidden for ${Math.round(wasHiddenFor / 1000)}s, refreshing subscriptions`);
          window.dispatchEvent(new CustomEvent('connection:restored', { detail: { timestamp: Date.now(), reason: 'visibility' } }));
        }
      }
    });

    window.addEventListener('online', () => {
      console.log('[WS] Network came online, reconnecting...');
      this.wasDisconnected = true;
      this.reconnectWebSocket();
    });

    window.addEventListener('focus', () => {
      if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
        console.log('[WS] Window focused with dead connection, reconnecting...');
        this.wasDisconnected = true;
        this.reconnectWebSocket();
      }
    });
  }

  private startHeartbeatCheck() {
    this.stopHeartbeatCheck();
    this.heartbeatCheckInterval = setInterval(() => {
      if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return;
      const elapsed = Date.now() - this.lastHeartbeat;
      if (elapsed > 20_000) {
        console.log(`[WS] No heartbeat for ${Math.round(elapsed / 1000)}s, reconnecting...`);
        this.wasDisconnected = true;
        this.reconnectWebSocket();
      }
    }, 5000);
  }

  private stopHeartbeatCheck() {
    if (this.heartbeatCheckInterval) {
      clearInterval(this.heartbeatCheckInterval);
      this.heartbeatCheckInterval = undefined;
    }
  }

  reconnectWebSocket() {
    this.stopHeartbeatCheck();
    if (this.ws) {
      this.ws.onclose = null;
      this.ws.onerror = null;
      this.ws.onmessage = null;
      this.ws.close();
      this.ws = undefined;
    }
    this.wsConnecting = false;
    this.initWebSocket();
  }

  registerActiveCluster(clusterId: string) {
    this.activeClusters.add(clusterId);
  }

  unregisterActiveCluster(clusterId: string) {
    this.activeClusters.delete(clusterId);
    this.cleanupClusterHandlers(clusterId);
  }

  private cleanupClusterHandlers(clusterId: string) {
    const topicsToRemove: string[] = [];
    for (const topic of this.wsHandlers.keys()) {
      if (topic.startsWith(`items:${clusterId}:`) || topic === `counts:${clusterId}`) {
        topicsToRemove.push(topic);
      }
    }
    for (const topic of topicsToRemove) {
      this.sendWS({ type: 'unsubscribe', payload: { topic } });
      this.wsHandlers.delete(topic);
    }
    if (topicsToRemove.length > 0) {
      console.log(`[WS] Cleaned up ${topicsToRemove.length} handlers for closed cluster: ${clusterId}`);
    }
  }

  private async handleHandshakeFailure() {
    const refreshed = await this.refreshSessionSecret();
    if (refreshed) {
      console.log('[WS] Session secret refreshed after handshake failure');
      this.reconnectWebSocket();
      return;
    }
    window.dispatchEvent(new CustomEvent('toast:error', {
      detail: { message: 'WebSocket session failed. Restart Kanivet.' }
    }));
    window.dispatchEvent(new CustomEvent('session:invalid', {
      detail: { message: 'WebSocket session failed. Please restart the application.' }
    }));
  }

  async waitForBackend(maxWaitMs: number = 30000): Promise<boolean> {
    const startTime = Date.now();
    const checkInterval = 500;
    this.dispatchConnectionEvent('backend', 'connecting');

    while (Date.now() - startTime < maxWaitMs) {
      try {
        const response = await fetch(`${getApiBase()}/health`, {
          method: 'GET',
          signal: AbortSignal.timeout(2000)
        });
        if (response.ok) {
          console.log('[API] Backend is ready');
          await this.sessionSecretPromise;
          this.backendReady = true;
          this.lastBackendState = 'connected';
          this.dispatchConnectionEvent('backend', 'connected', { timestamp: Date.now() });
          this.initWebSocket();
          return true;
        }
      } catch {
      }
      await new Promise(resolve => setTimeout(resolve, checkInterval));
    }
    console.warn('[API] Backend did not become ready within timeout');
    this.dispatchConnectionEvent('backend', 'disconnected');
    return false;
  }

  isReady(): boolean {
    return this.backendReady && this.ws?.readyState === WebSocket.OPEN;
  }

  private async initWebSocket() {
    if (this.wsConnecting) {
      console.log('[WS] Connection already in progress, skipping...');
      return;
    }
    if (this.ws?.readyState === WebSocket.OPEN || this.ws?.readyState === WebSocket.CONNECTING) {
      console.log('[WS] WebSocket already connected/connecting, skipping...');
      return;
    }
    this.wsConnecting = true;
    this.dispatchConnectionEvent('websocket', 'connecting');
    const wsBase = getWsBase();
    console.log('[WS] Attempting WebSocket connection to:', wsBase);
    try {
      await this.sessionSecretPromise;
      let wsUrl = wsBase;
      if (this.sessionSecret) {
        wsUrl += `?session_secret=${encodeURIComponent(this.sessionSecret)}`;
      }
      try {
        this.ws = new WebSocket(wsUrl);
      } catch (wsError) {
        console.error('[WS] WebSocket constructor failed:', wsError);
        this.wsConnecting = false;
        setTimeout(() => this.initWebSocket(), 2000);
        return;
      }

      if (this.connectWatchdog) clearTimeout(this.connectWatchdog);
      this.connectWatchdog = setTimeout(() => {
        if (this.ws && this.ws.readyState !== WebSocket.OPEN) {
          console.log('[WS] Connect attempt timed out, forcing reconnect');
          this.wasDisconnected = true;
          this.reconnectWebSocket();
        }
      }, 15000);

      (window as any).__activeWebSockets = ((window as any).__activeWebSockets || 0) + 1;

      const isDev = process.env.NODE_ENV !== 'production' || (window as any).electron?.isDev;
      if (isDev && !(window as any).__wsEvents) {
        (window as any).__wsEvents = { total: 0, perSecond: {}, byType: {}, lastMinute: [] };
      }

      this.ws.onopen = () => {
        console.log('[WS] WebSocket connected successfully to:', wsBase);
        if (this.connectWatchdog) {
          clearTimeout(this.connectWatchdog);
          this.connectWatchdog = undefined;
        }
        this.wsConnecting = false;
        this.wsFailureCount = 0;
        this.lastHeartbeat = Date.now();
        this.startHeartbeatCheck();
        const reconnectedAfterDisconnect = this.wasDisconnected;
        this.wasDisconnected = false;
        this.dispatchConnectionEvent('websocket', 'connected');
        if (this.reconnectCountdownInterval) {
          clearInterval(this.reconnectCountdownInterval);
          this.reconnectCountdownInterval = undefined;
        }
        this.resubscribeAllTopics();
        if (reconnectedAfterDisconnect) {
          console.log('[WS] Connection restored after disconnect, triggering data refresh');
          window.dispatchEvent(new CustomEvent('connection:restored', { detail: { timestamp: Date.now() } }));
        }
      };

      this.ws.onclose = (event) => {
        this.stopHeartbeatCheck();
        if (this.connectWatchdog) {
          clearTimeout(this.connectWatchdog);
          this.connectWatchdog = undefined;
        }
        this.wsConnecting = false;
        this.wasDisconnected = true;
        console.log('[WS] WebSocket disconnected:', { code: event.code, reason: event.reason, wasClean: event.wasClean });
        this.dispatchConnectionEvent('websocket', 'disconnected');

        if ((window as any).__activeWebSockets) {
          (window as any).__activeWebSockets--;
        }

        if (!this.wsFailureCount) this.wsFailureCount = 0;
        const isHandshakeFailure = event.code === 1006 && !event.wasClean;

        if (isHandshakeFailure) {
          this.wsFailureCount++;
          console.log(`[WS] WebSocket handshake failed (attempt ${this.wsFailureCount})`);
          if (this.wsFailureCount >= 3) {
            console.log('[WS] Multiple WebSocket handshake failures - checking local session');
            this.wsFailureCount = 0;
            void this.handleHandshakeFailure();
            return;
          }
        } else {
          this.wsFailureCount = 0;
        }

        this.dispatchConnectionEvent('websocket', 'reconnecting', { countdown: 1 });
        console.log('[WS] Reconnecting in 1 second...');
        setTimeout(() => this.initWebSocket(), 1000);
      };

      this.ws.onerror = (error) => {
        console.error('🚨 WebSocket error:', error);
      };

      this.ws.onmessage = (ev) => this.handleMessage(ev);
    } catch (e) {
      console.error('[WS] WebSocket initialization failed:', e);
      this.wsConnecting = false;
      this.dispatchConnectionEvent('websocket', 'reconnecting');
      setTimeout(() => this.initWebSocket(), 2000);
    }
  }

  private handleMessage(ev: MessageEvent) {
    try {
      const msg = JSON.parse(ev.data);

      if (msg.type === 'heartbeat') {
        this.lastHeartbeat = Date.now();
        return;
      }

      if (msg.type === 'cluster_error') {
        logger.warn('Cluster connection error:', msg);
        window.dispatchEvent(new CustomEvent('cluster:error', {
          detail: { cluster: msg.cluster, errorCode: msg.errorCode, errorMessage: msg.errorMessage, details: msg.details, recoverable: msg.recoverable }
        }));
        return;
      }

      if (msg.type === 'cluster_error_cleared') {
        window.dispatchEvent(new CustomEvent('cluster:error-cleared', {
          detail: { cluster: msg.cluster }
        }));
        return;
      }

      if (msg.type === 'vcluster_status') {
        window.dispatchEvent(new CustomEvent('vcluster:status', {
          detail: { cluster: msg.cluster, state: msg.state, generation: msg.generation, localPort: msg.localPort, detail: msg.detail }
        }));
        return;
      }

      if (msg.type === 'clusters_refreshed') {
        window.dispatchEvent(new CustomEvent('clusters:refreshed', {
          detail: { clusters: msg.clusters || [], reason: msg.reason || 'unknown' }
        }));
        return;
      }

      this.trackDevMetrics(msg);
      this.dispatchMessage(msg);
    } catch (e) {
      console.error('Failed to parse WebSocket message:', e);
    }
  }

  private trackDevMetrics(msg: any) {
    const isDev = process.env.NODE_ENV !== 'production' || (window as any).electron?.isDev;
    if (isDev && (window as any).__wsEvents) {
      const now = Date.now();
      const wsEvents = (window as any).__wsEvents;
      wsEvents.total++;
      const second = Math.floor(now / 1000);
      if (!wsEvents.perSecond[second]) wsEvents.perSecond[second] = 0;
      wsEvents.perSecond[second]++;
      const cutoff = second - 60;
      Object.keys(wsEvents.perSecond).forEach((s) => {
        if (parseInt(s) < cutoff) delete wsEvents.perSecond[s];
      });
      const msgType = msg.type || msg.MessageType || msg.messageType || 'unknown';
      wsEvents.byType[msgType] = (wsEvents.byType[msgType] || 0) + 1;
      wsEvents.lastMinute.push(now);
      wsEvents.lastMinute = wsEvents.lastMinute.filter((t: number) => t > now - 60000);
    }
  }

  private dispatchMessage(msg: any) {
    if ((msg.type === 'batch' || msg.messageType === 'batch' || msg.MessageType === 'batch') && msg.events) {
      const topic = (msg.topic || msg.Topic) as string;
      this.logFirstResponse(topic, msg.events?.length || 0, 'BATCH');
      const handlers = this.wsHandlers.get(topic);
      if (handlers) {
        const events = msg.events;
        handlers.forEach((h) => { try { h({ isBatch: true, events, topic }); } catch (e) { console.error('[WS] Batch handler failed:', e); } });
      } else {
        this.unsubscribe(topic);
      }
    } else if (msg.type === 'bulk_list' || msg.messageType === 'bulk_list' || msg.MessageType === 'bulk_list') {
      const topic = (msg.topic || msg.Topic) as string;
      const items = msg.items || msg.Items || [];
      this.logFirstResponse(topic, items.length, 'BULK_LIST');
      const handlers = this.wsHandlers.get(topic);
      if (handlers) {
        const epoch = msg.epoch || msg.Epoch || 0;
        const events = items.map((item: any) => ({ channel: 'items', action: 'added', item }));
        handlers.forEach((h) => { try { h({ isBatch: true, events, topic, epoch, bulk: true }); } catch (e) { console.error('[WS] Bulk list handler failed:', e); } });
      } else {
        this.unsubscribe(topic);
      }
    } else {
      const topic = (msg.topic || msg.Topic) as string;
      if (!topic) {
        const msgType = (msg.type || msg.MessageType || msg.messageType) as string;
        if (msgType && this.wsHandlers.has(msgType)) {
          const handlers = this.wsHandlers.get(msgType)!;
          handlers.forEach((h) => { try { h(msg); } catch (e) { console.error('[WS] Type handler failed:', e); } });
        } else {
          console.warn('WebSocket message without topic/type handler:', msg);
        }
        return;
      }
      const handlers = this.wsHandlers.get(topic);
      if (handlers) {
        handlers.forEach((h) => { try { h({ isBatch: true, events: [msg], topic }); } catch (e) { console.error('[WS] Handler failed:', e); } });
      } else {
        this.unsubscribe(topic);
      }
    }
  }

  private logFirstResponse(topic: string, count: number, type: string) {
    const subscribeTime = this.subscribeTimestamps.get(topic);
    if (subscribeTime) {
      const elapsed = performance.now() - subscribeTime;
      console.log(`[WS] ⏱️ First ${type} for ${topic.split(':').slice(-2, -1)[0]}: ${elapsed.toFixed(2)}ms (${count} ${type === 'BATCH' ? 'events' : 'items'})`);
      this.subscribeTimestamps.delete(topic);
    }
  }

  private ensureWSReady() {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
      this.initWebSocket().catch(console.error);
    }
  }

  private parseItemsTopic(topic: string): { cluster: string; group: string; version: string; kind: string; namespace: string } | null {
    const prefix = 'items:';
    if (!topic.startsWith(prefix)) return null;
    const remainder = topic.slice(prefix.length);
    const sepPositions: number[] = [];
    for (let i = 0; i < remainder.length; i++) {
      if (remainder[i] === ':') sepPositions.push(i);
    }
    if (sepPositions.length < 4) return null;
    const relevantSeps = sepPositions.slice(-4);
    return {
      cluster: remainder.slice(0, relevantSeps[0]),
      group: remainder.slice(relevantSeps[0] + 1, relevantSeps[1]),
      version: remainder.slice(relevantSeps[1] + 1, relevantSeps[2]),
      kind: remainder.slice(relevantSeps[2] + 1, relevantSeps[3]),
      namespace: remainder.slice(relevantSeps[3] + 1),
    };
  }

  private resubscribeAllTopics() {
    const topics = Array.from(this.wsHandlers.keys());
    if (topics.length === 0) return;
    const staleTopics: string[] = [];
    let resubscribedCount = 0;
    for (const topic of topics) {
      if (topic.startsWith('items:')) {
        const parsed = this.parseItemsTopic(topic);
        if (parsed) {
          if (!this.activeClusters.has(parsed.cluster)) {
            staleTopics.push(topic);
            continue;
          }
          const prefs = this.topicSortPrefs.get(topic);
          this.sendWS({
            type: 'subscribe',
            payload: { channel: 'items', cluster: parsed.cluster, group: parsed.group || '', version: parsed.version, kind: parsed.kind, namespace: parsed.namespace || '', sortBy: prefs?.sortBy || 'age', sortOrder: prefs?.sortOrder || 'desc' },
          });
          resubscribedCount++;
        }
      } else if (topic.startsWith('counts:')) {
        const clusterId = topic.slice('counts:'.length);
        if (!this.activeClusters.has(clusterId)) {
          staleTopics.push(topic);
          continue;
        }
        this.sendWS({ type: 'subscribe', payload: { action: 'subscribe', topic } });
        resubscribedCount++;
      }
    }
    for (const topic of staleTopics) {
      this.wsHandlers.delete(topic);
    }
    if (staleTopics.length > 0) console.log(`[WS] Removed ${staleTopics.length} stale topics for inactive clusters`);
    if (resubscribedCount > 0) console.log(`[WS] Re-subscribed to ${resubscribedCount} topics after reconnect`);
  }

  sendWS(payload: any) {
    this.ensureWSReady();
    const data = JSON.stringify(payload);
    const trySend = () => {
      if (this.ws && this.ws.readyState === WebSocket.OPEN) {
        this.ws.send(data);
      } else {
        setTimeout(trySend, 100);
      }
    };
    trySend();
  }

  subscribeToCounts(cluster: string, handler: (msg: { group: string; resource: string; count: number }) => void): () => void {
    const topic = `counts:${cluster}`;
    if (!this.wsHandlers.has(topic)) this.wsHandlers.set(topic, new Set());
    const wrapped = (raw: any) => {
      if (raw && raw.isBatch && raw.events) {
        for (const event of raw.events) {
          if (event && event.channel === 'counts' && typeof event.count === 'number') {
            handler({ group: event.group || '', resource: event.resource || '', count: event.count });
          }
        }
      } else if (raw && raw.channel === 'counts' && typeof raw.count === 'number') {
        handler({ group: raw.group || '', resource: raw.resource || '', count: raw.count });
      }
    };
    this.wsHandlers.get(topic)!.add(wrapped);
    this.sendWS({ type: 'subscribe', payload: { action: 'subscribe', topic } });
    return () => {
      const set = this.wsHandlers.get(topic);
      if (!set) return;
      set.delete(wrapped);
      if (set.size === 0) {
        this.wsHandlers.delete(topic);
        this.sendWS({ type: 'unsubscribe', payload: { action: 'unsubscribe', topic } });
      }
    };
  }

  subscribeToDashboard(cluster: string, handler: (msg: any) => void): () => void {
    const messageType = 'dashboard';
    if (!this.wsHandlers.has(messageType)) this.wsHandlers.set(messageType, new Set());
    this.wsHandlers.get(messageType)!.add(handler);
    console.log(`[API] Subscribed to dashboard updates for cluster: ${cluster}`);
    return () => {
      console.log(`[API] Unsubscribing from dashboard updates for cluster: ${cluster}`);
      const handlers = this.wsHandlers.get(messageType);
      if (handlers) {
        handlers.delete(handler);
        if (handlers.size === 0) this.wsHandlers.delete(messageType);
      }
    };
  }

  subscribeToItems(
    cluster: string, group: string, version: string, kind: string, namespace?: string,
    onEvent?: (event: any) => void, sortBy?: string, sortOrder?: 'asc' | 'desc'
  ): string {
    const ns = namespace || '';
    const topic = `items:${cluster}:${group || ''}:${version}:${kind}:${ns}`;
    if (onEvent) {
      if (!this.wsHandlers.has(topic)) this.wsHandlers.set(topic, new Set());
      this.wsHandlers.get(topic)!.add(onEvent);
    }
    this.topicSortPrefs.set(topic, { sortBy: sortBy || 'age', sortOrder: sortOrder || 'desc' });
    this.subscribeTimestamps.set(topic, performance.now());
    console.log(`[WS] Subscribe sent for ${kind} at ${performance.now().toFixed(2)}ms`);
    this.sendWS({
      type: 'subscribe',
      payload: { channel: 'items', cluster, group: group || '', version, kind, namespace: ns, sortBy: sortBy || 'age', sortOrder: sortOrder || 'desc' },
    });
    return topic;
  }

  unsubscribe(topic: string, onEvent?: (event: any) => void, clearAll: boolean = false) {
    const set = this.wsHandlers.get(topic);
    if (set) {
      if (clearAll || !onEvent) this.wsHandlers.delete(topic);
      else {
        set.delete(onEvent);
        if (set.size === 0) this.wsHandlers.delete(topic);
      }
    }
    if (!this.wsHandlers.has(topic)) {
      this.topicSortPrefs.delete(topic);
      this.sendWS({ type: 'unsubscribe', payload: { topic } });
    }
  }

  getHandlers(): Map<string, Set<MessageHandler>> {
    return this.wsHandlers;
  }
}

export const wsManager = new WebSocketManager();
