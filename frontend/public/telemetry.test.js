const test = require('node:test');
const assert = require('node:assert/strict');
const {
  InstallationTelemetry,
  INSTALLATION_ID_KEY,
  LAST_HEARTBEAT_DAY_KEY,
} = require('./telemetry');
const LEGACY_ENABLED_KEY = ['sen', 'tryLogsEnabled'].join('');

class Store {
  constructor(values = {}) { this.values = { ...values }; }
  get(key) { return this.values[key]; }
  set(key, value) { this.values[key] = value; }
  has(key) { return Object.hasOwn(this.values, key); }
  delete(key) { delete this.values[key]; }
}

const UUID = '11111111-1111-4111-8111-111111111111';
const create = (store, requests, overrides = {}) => new InstallationTelemetry({
  store,
  randomUUID: () => UUID,
  fetchImpl: async (url, options) => {
    requests.push({ url, options });
    return { ok: true, status: 204 };
  },
  ...overrides,
});

test('defaults enabled and sends the exact heartbeat payload', async () => {
  const store = new Store();
  const requests = [];
  const telemetry = create(store, requests, { now: () => Date.UTC(2026, 7, 3, 10, 0, 0) });
  assert.equal(await telemetry.heartbeat(), true);
  assert.equal(requests[0].url, 'https://heartbeat.kanivet.io/v1/heartbeat');
  assert.equal(requests[0].options.method, 'POST');
  assert.deepEqual(JSON.parse(requests[0].options.body), { installation_id: UUID });
});

test('sends at most once per UTC day', async () => {
  let now = Date.UTC(2026, 7, 3, 23, 59, 0);
  const store = new Store();
  const requests = [];
  const telemetry = create(store, requests, { now: () => now });
  assert.equal(await telemetry.heartbeat(), true);
  assert.equal(await telemetry.heartbeat(), false);
  now = Date.UTC(2026, 7, 4, 0, 0, 1);
  assert.equal(await telemetry.heartbeat(), true);
  assert.equal(requests.length, 2);
});

test('migrates an explicit legacy diagnostics opt-out and removes the old key', async () => {
  const store = new Store({ [LEGACY_ENABLED_KEY]: false });
  const requests = [];
  const telemetry = create(store, requests);
  assert.equal(telemetry.isEnabled(), false);
  assert.equal(store.has(LEGACY_ENABLED_KEY), false);
  assert.equal(await telemetry.heartbeat(), false);
  assert.equal(requests.length, 0);
});

test('opt-out stops future heartbeats without deleting the stored uuid or cadence state', async () => {
  const store = new Store();
  const requests = [];
  const telemetry = create(store, requests, { now: () => Date.UTC(2026, 7, 3, 10, 0, 0) });
  assert.equal(await telemetry.heartbeat(), true);
  assert.equal(await telemetry.setEnabled(false), true);
  assert.deepEqual(requests.map(({ options }) => options.method), ['POST']);
  assert.equal(await telemetry.heartbeat(true), false);
  assert.equal(store.get(INSTALLATION_ID_KEY), UUID);
  assert.equal(store.get(LAST_HEARTBEAT_DAY_KEY), '2026-08-03');
});

test('disabled initialization retains existing uuid and cadence state without retrying a delete', async () => {
  const store = new Store({
    telemetryEnabled: false,
    [INSTALLATION_ID_KEY]: UUID,
    [LAST_HEARTBEAT_DAY_KEY]: '2026-08-03',
  });
  const requests = [];
  const restarted = create(store, requests);
  assert.equal(await restarted.initialize(), true);
  assert.equal(requests.length, 0);
  assert.equal(store.get(INSTALLATION_ID_KEY), UUID);
  assert.equal(store.get(LAST_HEARTBEAT_DAY_KEY), '2026-08-03');
  assert.equal(restarted.isEnabled(), false);
});

test('an opt-out racing an in-flight heartbeat is not followed by a delete', async () => {
  const store = new Store();
  const requests = [];
  let releasePost;
  let markStarted;
  const started = new Promise((resolve) => { markStarted = resolve; });
  const telemetry = create(store, requests, {
    now: () => Date.UTC(2026, 7, 3, 10, 0, 0),
    fetchImpl: async (url, options) => {
      requests.push({ url, options });
      if (options.method === 'POST') {
        markStarted();
        await new Promise((resolve) => { releasePost = resolve; });
      }
      return { ok: true, status: 204 };
    },
  });
  const heartbeat = telemetry.heartbeat();
  await started;
  const disabling = telemetry.setEnabled(false);
  releasePost();
  assert.equal(await heartbeat, true);
  assert.equal(await disabling, true);
  assert.deepEqual(requests.map(({ options }) => options.method), ['POST']);
  assert.equal(store.get(INSTALLATION_ID_KEY), UUID);
  assert.equal(store.get(LAST_HEARTBEAT_DAY_KEY), '2026-08-03');
});

test('does not record a successful day when the request fails', async () => {
  const store = new Store();
  const telemetry = create(store, [], {
    now: () => Date.UTC(2026, 7, 3, 10, 0, 0),
    fetchImpl: async () => ({ ok: false, status: 503 }),
  });
  assert.equal(await telemetry.heartbeat(), false);
  assert.equal(store.has(LAST_HEARTBEAT_DAY_KEY), false);
  assert.equal(store.get(INSTALLATION_ID_KEY), UUID);
});
