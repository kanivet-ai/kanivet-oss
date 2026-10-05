import logger from '../../utils/logger';
import { getWsBase, getApiBase, setBackendPort } from './types';

type MessageHandler = (msg: any) => void;
type ConnectionEventType = 'backend' | 'websocket' | 'cluster';

// The backend sends a heartbeat every 10s; a socket silent for longer than
// this is treated as dead.
const HEARTBEAT_TIMEOUT_MS = 20_000;

// Reconnect delay: the first retry is quick so a backend that restarts is
// picked up at once, later ones back off with jitter up to the cap.
const RECONNECT_BASE_MS = 1000;
const RECONNECT_MAX_MS = 30_000;

// Messages sent while the socket is down wait here until it opens. Bounded so
// a long outage cannot grow it.
const OUTBOX_LIMIT = 500;

// Streams the backend keeps per connection: after a reconnect they are asked
// for again, since the new connection knows nothing of them. Logs are not
// here, they restart themselves on `connection:restored`.
const REPLAYABLE_TYPES = new Set(['dashboard', 'helm', 'metrics', 'cloud.discover']);

type MessageOp = 'start' | 'stop';
interface MessageInfo {
  key: string | null;
  op: MessageOp | null;
}

// What a message starts or stops. Two messages with the same key address the
// same subscription.
function classifyMessage(message: any): MessageInfo {
  const none: MessageInfo = { key: null, op: null };
  const type = message?.type;
  const p = message?.payload;
  if (typeof type !== 'string' || !p || typeof p !== 'object') return none;
  if (type === 'subscribe') {
    if (p.channel === 'items') {
      return { key: `items:${p.cluster}:${p.group || ''}:${p.version}:${p.kind}:${p.namespace || ''}`, op: 'start' };
    }
    if (typeof p.topic === 'string') return { key: p.topic, op: 'start' };
    return none;
  }
  if (type === 'unsubscribe') {
    return typeof p.topic === 'string' ? { key: p.topic, op: 'stop' } : none;
  }
  const op: MessageOp | null =
    p.action === 'start' || p.action === 'subscribe' ? 'start'
      : p.action === 'stop' || p.action === 'unsubscribe' ? 'stop'
        : null;
  if (!op) return none;
  let id: unknown;
  if (type === 'logs' || type === 'cloud.discover') id = p.key;
  else if (type === 'dashboard' || type === 'helm') id = p.cluster;
  else if (type === 'metrics') {
    // A stop carries only what identifies the stream; a start adds settings.
    const { action: _action, provider: _provider, streamingRate: _rate, ...identity } = p;
    id = JSON.stringify(identity, Object.keys(identity).sort());
  } else return none;
  if (id === undefined || id === null || id === '') return none;
  return { key: `${type}:${String(id)}`, op };
}

export class WebSocketManager {
  private ws?: WebSocket;
  private wsHandlers: Map<string, Set<MessageHandler>> = new Map();
  private wsFailureCount: number = 0;
  private wsConnecting: boolean = false;
  private sessionSecret: string | null = null;
  private backendReady: boolean = false;
  // Set once waitForBackend has given up on the backend.
  private backendWaitGaveUp: boolean = false;
  private activeClusters: Set<string> = new Set();
  private backendHealthInterval?: NodeJS.Timeout;
  private reconnectCountdownInterval?: NodeJS.Timeout;
  private connectWatchdog?: NodeJS.Timeout;
  private subscribeTimestamps: Map<string, number> = new Map();
  private topicSortPrefs: Map<string, { sortBy: string; sortOrder: string }> = new Map();
  private wasDisconnected: boolean = false;
  private lastBackendState: 'connected' | 'disconnected' = 'disconnected';
  private sessionSecretPromise: Promise<void>;
  private backendPortPromise: Promise<void>;
  private lastHeartbeat: number = 0;
  private heartbeatCheckInterval?: NodeJS.Timeout;
  private reconnectTimer?: ReturnType<typeof setTimeout>;
  private reconnectAttempt: number = 0;
  private sessionFailureNotified: boolean = false;
  // Messages waiting for the socket, by what they address (insertion order).
  private outbox: Map<string, any> = new Map();
  private outboxSeq: number = 0;
  // Start messages to send again after a reconnect, by what they address.
  private replayable: Map<string, any> = new Map();
  // What has been started on the current connection, so a topic that a
  // `connection:restored` listener already restarted is not asked for twice.
  private sentOnConnection: Set<string> = new Set();

  constructor() {
    this.backendPortPromise = this.initBackendPort();
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

  // The window opens before the backend has picked its port, so the URL's is
  // only a default: ask the main process for the real one (it answers once the
  // backend has printed it, or startup has given up on one) rather than rely on
  // a 'backend:port-changed' push sent before index.tsx subscribed. Browser
  // mode has no main process to ask and keeps the URL's port.
  private async initBackendPort() {
    const getPort = (window as any).electronAPI?.backend?.getPort;
    if (typeof getPort !== 'function') return;
    let timer: ReturnType<typeof setTimeout> | undefined;
    try {
      const answer = getPort() as Promise<number>;
      const port = await Promise.race([
        answer,
        new Promise<null>((resolve) => {
          timer = setTimeout(() => resolve(null), 30000);
        }),
      ]);
      if (port) setBackendPort(port);
      // Requests stop waiting after 30s, but a slow first start still
      // answers later: take its port then.
      else answer.then((p) => p && setBackendPort(p)).catch(() => {});
    } catch {
      // Keep the port from the URL.
    } finally {
      clearTimeout(timer);
    }
  }

  getSessionSecret(): string | null {
    return this.sessionSecret;
  }

  // Every request waits for this: it needs the session secret, and the
  // backend's real port (the theme loads before the backend is up).
  async waitForSessionSecret(): Promise<void> {
    await Promise.all([this.sessionSecretPromise, this.backendPortPromise]);
  }

  private startBackendHealthCheck() {
    if (this.backendHealthInterval) clearInterval(this.backendHealthInterval);
    this.backendHealthInterval = setInterval(async () => {
      if (!this.backendReady) {
        // waitForBackend gave up on a slow first start (the window opens
        // before the backend): keep asking, so the session comes up with
        // the backend instead of never connecting its socket.
        if (this.backendWaitGaveUp) await this.recoverBackend();
        return;
      }
      if (this.ws?.readyState === WebSocket.OPEN) {
        if (this.lastBackendState !== 'connected') {
          this.lastBackendState = 'connected';
          this.dispatchConnectionEvent('backend', 'connected', { timestamp: Date.now() });
        }
        return;
      }
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
    // Messages keep arriving while the window is hidden, so an open socket that
    // kept its heartbeat has every subscription current: showing the window
    // again needs no refresh. Only a dead or silent socket is replaced.
    document.addEventListener('visibilitychange', () => {
      if (document.visibilityState !== 'visible') return;
      if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
        console.log('[WS] Connection lost while hidden, reconnecting...');
        this.wasDisconnected = true;
        this.reconnectWebSocket();
      } else if (Date.now() - this.lastHeartbeat > HEARTBEAT_TIMEOUT_MS) {
        console.log('[WS] No heartbeat while hidden, reconnecting...');
        this.wasDisconnected = true;
        this.reconnectWebSocket();
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
      if (elapsed > HEARTBEAT_TIMEOUT_MS) {
        console.log(
          `[WS] No heartbeat for ${Math.round(elapsed / 1000)}s, reconnecting...`,
        );
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

  private nextReconnectDelay(): number {
    const attempt = this.reconnectAttempt++;
    if (attempt === 0) return RECONNECT_BASE_MS;
    const ceiling = Math.min(RECONNECT_MAX_MS, RECONNECT_BASE_MS * 2 ** Math.min(attempt, 10));
    return Math.round(ceiling / 2 + (Math.random() * ceiling) / 2);
  }

  // The one place a retry is scheduled, so there is never more than one timer.
  private scheduleReconnect(delayMs: number = this.nextReconnectDelay()) {
    if (this.reconnectTimer) clearTimeout(this.reconnectTimer);
    this.dispatchConnectionEvent('websocket', 'reconnecting', { countdown: Math.max(1, Math.ceil(delayMs / 1000)) });
    console.log(`[WS] Reconnecting in ${(delayMs / 1000).toFixed(1)} seconds...`);
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = undefined;
      void this.initWebSocket();
    }, delayMs);
  }

  reconnectWebSocket() {
    this.stopHeartbeatCheck();
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = undefined;
    }
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
    for (const [key, message] of [...this.replayable]) {
      if (message?.payload?.cluster === clusterId) this.replayable.delete(key);
    }
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
      this.scheduleReconnect();
      return;
    }
    if (!this.sessionFailureNotified) {
      this.sessionFailureNotified = true;
      window.dispatchEvent(new CustomEvent('toast:error', {
        detail: { message: 'WebSocket session failed. Restart Kanivet.' }
      }));
    }
    window.dispatchEvent(new CustomEvent('session:invalid', {
      detail: { message: 'WebSocket session failed. Please restart the application.' }
    }));
    // Keep trying slowly: the backend may come back with the same session.
    this.scheduleReconnect(RECONNECT_MAX_MS);
  }

  async waitForBackend(maxWaitMs: number = 30000): Promise<boolean> {
    this.dispatchConnectionEvent('backend', 'connecting');
    // Polled at the backend's real port, and for as long as before the window
    // opened ahead of the backend: the wait starts once the port is known.
    await this.backendPortPromise;
    const startTime = Date.now();
    const checkInterval = 500;

    while (Date.now() - startTime < maxWaitMs) {
      try {
        const response = await fetch(`${getApiBase()}/health`, {
          method: 'GET',
          signal: AbortSignal.timeout(2000)
        });
        if (response.ok) {
          console.log('[API] Backend is ready');
          await this.markBackendReady();
          return true;
        }
      } catch {
      }
      await new Promise(resolve => setTimeout(resolve, checkInterval));
    }
    console.warn('[API] Backend did not become ready within timeout');
    this.dispatchConnectionEvent('backend', 'disconnected');
    this.backendWaitGaveUp = true;
    return false;
  }

  private async markBackendReady() {
    await this.sessionSecretPromise;
    if (this.backendReady) return;
    this.backendReady = true;
    this.lastBackendState = 'connected';
    this.dispatchConnectionEvent('backend', 'connected', { timestamp: Date.now() });
    this.initWebSocket();
  }

  private async recoverBackend() {
    try {
      const response = await fetch(`${getApiBase()}/health`, { method: 'GET', signal: AbortSignal.timeout(2000) });
      if (!response.ok) return;
      console.log('[API] Backend is ready after a slow start');
      await this.markBackendReady();
    } catch {
      // Still starting.
    }
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
        this.scheduleReconnect();
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
        this.reconnectAttempt = 0;
        this.sessionFailureNotified = false;
        this.sentOnConnection.clear();
        this.lastHeartbeat = Date.now();
        this.startHeartbeatCheck();
        const reconnectedAfterDisconnect = this.wasDisconnected;
        this.wasDisconnected = false;
        this.dispatchConnectionEvent('websocket', 'connected');
        if (this.reconnectCountdownInterval) {
          clearInterval(this.reconnectCountdownInterval);
          this.reconnectCountdownInterval = undefined;
        }
        // Listeners go first: they clear caches and restart what is on screen,
        // and what they subscribe is not asked for again below. Each topic then
        // gets one subscribe per connection.
        if (reconnectedAfterDisconnect) {
          console.log('[WS] Connection restored after disconnect, triggering data refresh');
          window.dispatchEvent(new CustomEvent('connection:restored', { detail: { timestamp: Date.now() } }));
        }
        this.resubscribeAllTopics();
        this.replayStreams();
        this.flushOutbox();
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

        this.scheduleReconnect();
      };

      this.ws.onerror = (error) => {
        console.error('🚨 WebSocket error:', error);
      };

      this.ws.onmessage = (ev) => this.handleMessage(ev);
    } catch (e) {
      console.error('[WS] WebSocket initialization failed:', e);
      this.wsConnecting = false;
      this.scheduleReconnect();
    }
  }

  private handleMessage(ev: MessageEvent) {
    // Any traffic shows the socket is alive: a long main-thread stall on a big
    // snapshot must not read as a missed heartbeat.
    this.lastHeartbeat = Date.now();
    try {
      const msg = JSON.parse(ev.data);

      if (msg.type === 'heartbeat') return;

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

      if (msg.type === 'cloud_auth_changed') {
        window.dispatchEvent(new CustomEvent('cloud:auth-changed', {
          detail: { provider: msg.provider, local: false }
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

  private parseItemsTopic(topic: string): {
    cluster: string;
    group: string;
    version: string;
    kind: string;
    namespace: string;
  } | null {
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
      // Already subscribed on this connection (a restore listener restarted it).
      if (this.sentOnConnection.has(topic)) continue;
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

  // Asks again for the streams that are still wanted. The new connection has
  // none of them.
  private replayStreams() {
    for (const [key, message] of [...this.replayable]) {
      if (this.sentOnConnection.has(key)) continue;
      this.sendNow(message, classifyMessage(message));
    }
  }

  // Sends what queued up while the socket was down. Topic subscriptions come
  // from resubscribeAllTopics and replayStreams; what they already sent, or a
  // listener restarted, is not sent again.
  private flushOutbox() {
    if (this.outbox.size === 0) return;
    const queued = Array.from(this.outbox.values());
    this.outbox.clear();
    for (const message of queued) {
      const info = classifyMessage(message);
      if (info.op === 'start' && info.key && this.sentOnConnection.has(info.key)) continue;
      this.sendNow(message, info);
    }
  }

  private sendNow(message: any, info: MessageInfo) {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return;
    if (info.key && info.op === 'start') this.sentOnConnection.add(info.key);
    else if (info.key && info.op === 'stop') this.sentOnConnection.delete(info.key);
    this.ws.send(JSON.stringify(message));
  }

  private enqueue(message: any, info: MessageInfo) {
    if (info.key && info.op === 'stop') {
      // The connection it would stop is gone; a new one has nothing to stop,
      // and a start still waiting for it is cancelled.
      this.outbox.delete(info.key);
      return;
    }
    const key = info.key && info.op === 'start' ? info.key : `#${this.outboxSeq++}`;
    this.outbox.delete(key);
    this.outbox.set(key, message);
    while (this.outbox.size > OUTBOX_LIMIT) {
      this.outbox.delete(this.outbox.keys().next().value as string);
    }
  }

  // Stops asking for a replayable stream again after a reconnect, without
  // sending a stop (a finished discovery has nothing to stop).
  forgetReplay(message: any) {
    const info = classifyMessage(message);
    if (info.key) this.replayable.delete(info.key);
  }

  sendWS(payload: any) {
    this.ensureWSReady();
    const info = classifyMessage(payload);
    if (info.key && REPLAYABLE_TYPES.has(payload.type)) {
      if (info.op === 'start') this.replayable.set(info.key, payload);
      else if (info.op === 'stop') this.replayable.delete(info.key);
    }
    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      this.sendNow(payload, info);
    } else {
      this.enqueue(payload, info);
    }
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
