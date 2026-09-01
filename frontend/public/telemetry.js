const crypto = require('crypto');

const DEFAULT_ENDPOINT = 'https://heartbeat.kanivet.io/v1/heartbeat';
const INSTALLATION_ID_KEY = 'telemetryInstallationId';
const ENABLED_KEY = 'telemetryEnabled';
const LAST_HEARTBEAT_DAY_KEY = 'telemetryLastHeartbeatUtcDay';
const LEGACY_ENABLED_KEY = ['sen', 'tryLogsEnabled'].join('');
const LEGACY_FORCE_KEY = ['analytics', 'ForceEnabled.2026-05'].join('');

const utcDay = (value) => new Date(value).toISOString().slice(0, 10);

class InstallationTelemetry {
  constructor({
    store,
    fetchImpl = globalThis.fetch,
    endpoint = process.env.KANIVET_HEARTBEAT_URL ?? DEFAULT_ENDPOINT,
    now = Date.now,
    randomUUID = crypto.randomUUID,
    setTimeoutImpl = setTimeout,
    clearTimeoutImpl = clearTimeout,
    random = Math.random,
    log = () => {},
  }) {
    this.store = store;
    this.fetch = fetchImpl;
    this.endpoint = endpoint;
    this.now = now;
    this.randomUUID = randomUUID;
    this.setTimeout = setTimeoutImpl;
    this.clearTimeout = clearTimeoutImpl;
    this.random = random;
    this.log = log;
    this.timer = null;
    this.operation = Promise.resolve();
    this.migratePreference();
  }

  migratePreference() {
    if (!this.store.has(ENABLED_KEY)) {
      this.store.set(ENABLED_KEY, this.store.get(LEGACY_ENABLED_KEY) !== false);
    }
    this.store.delete(LEGACY_ENABLED_KEY);
    this.store.delete(LEGACY_FORCE_KEY);
  }

  isEnabled() {
    return this.store.get(ENABLED_KEY) !== false;
  }

  getInstallationId() {
    let id = this.store.get(INSTALLATION_ID_KEY);
    if (!id) {
      id = this.randomUUID();
      this.store.set(INSTALLATION_ID_KEY, id);
    }
    return id;
  }

  initialize() {
    return this.enqueue(async () => {
      if (this.isEnabled()) this.scheduleHeartbeat();
      return true;
    });
  }

  setEnabled(enabled) {
    const next = enabled !== false;
    this.store.set(ENABLED_KEY, next);
    if (this.timer) {
      this.clearTimeout(this.timer);
      this.timer = null;
    }
    return this.enqueue(async () => {
      if (next) this.scheduleHeartbeat(0, 1000);
      return true;
    });
  }

  shouldSendToday(force = false) {
    if (force) return true;
    return this.store.get(LAST_HEARTBEAT_DAY_KEY) !== utcDay(this.now());
  }

  scheduleHeartbeat(minDelayMs = 2000, maxDelayMs = 15000) {
    if (!this.isEnabled() || typeof this.fetch !== 'function' || !this.endpoint || this.timer) {
      return false;
    }
    const delay = minDelayMs + Math.floor(this.random() * Math.max(0, maxDelayMs - minDelayMs));
    this.timer = this.setTimeout(async () => {
      this.timer = null;
      await this.heartbeat();
    }, delay);
    return true;
  }

  heartbeat(force = false) {
    return this.enqueue(() => this.performHeartbeat(force));
  }

  async performHeartbeat(force) {
    if (!this.isEnabled() || typeof this.fetch !== 'function' || !this.endpoint || !this.shouldSendToday(force)) {
      return false;
    }
    const installation_id = this.getInstallationId();
    const response = await this.request(this.endpoint, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ installation_id }),
    });
    if (!response?.ok) return false;
    this.store.set(LAST_HEARTBEAT_DAY_KEY, utcDay(this.now()));
    return true;
  }

  enqueue(operation) {
    const result = this.operation.then(operation, operation);
    this.operation = result.catch(() => {});
    return result;
  }

  async request(url, options) {
    const controller = new AbortController();
    const timer = this.setTimeout(() => controller.abort(), 3000);
    try {
      return await this.fetch(url, { ...options, signal: controller.signal });
    } catch (error) {
      this.log(`[Telemetry] ${error.message}`);
      return null;
    } finally {
      this.clearTimeout(timer);
    }
  }
}

module.exports = {
  DEFAULT_ENDPOINT,
  ENABLED_KEY,
  INSTALLATION_ID_KEY,
  LAST_HEARTBEAT_DAY_KEY,
  InstallationTelemetry,
  utcDay,
};
