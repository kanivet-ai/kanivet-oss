const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const os = require('os');
const path = require('path');
const {
  LEGACY_HOSTED_IDENTITY_MIGRATION_KEY,
  runLegacyHostedIdentityCleanup,
} = require('./legacyHostedIdentityMigration');

class Store {
  constructor(values = {}) {
    this.values = { ...values };
  }

  get(key) {
    return this.values[key];
  }

  set(key, value) {
    this.values[key] = value;
  }
}

function createTempLayout() {
  const root = fs.mkdtempSync(
    path.join(os.tmpdir(), 'kanivet-hosted-identity-migration-'),
  );
  const userDataPath = path.join(root, 'kanivet');
  fs.mkdirSync(userDataPath, { recursive: true });
  return { root, userDataPath };
}

test('scrubs only the legacy hosted identity keys across all historical paths', () => {
  const { root, userDataPath } = createTempLayout();
  const sharedConfigDir = path.join(root, 'kanivet-frontend');
  const dedicatedConfigDir = path.join(root, 'kanivet-auth');

  fs.mkdirSync(sharedConfigDir, { recursive: true });
  fs.mkdirSync(dedicatedConfigDir, { recursive: true });

  fs.writeFileSync(
    path.join(userDataPath, 'kanivet-auth.json'),
    JSON.stringify({
      auth_tokens: '{"refresh_token":"secret"}',
      token_obtained_at: 1234,
      last_activity: 5678,
    }),
    'utf8',
  );
  fs.writeFileSync(
    path.join(sharedConfigDir, 'config.json'),
    JSON.stringify({
      auth_tokens: '{"access_token":"secret"}',
      token_obtained_at: 1111,
      last_activity: 2222,
      telemetryEnabled: false,
      ssoSessions: [{ startUrl: 'https://d-123.awsapps.com/start' }],
      selectedCluster: 'dev-cluster',
    }),
    'utf8',
  );
  fs.writeFileSync(
    path.join(dedicatedConfigDir, 'config.json'),
    JSON.stringify({
      auth_tokens: '{"id_token":"secret"}',
      last_activity: 3333,
    }),
    'utf8',
  );
  fs.writeFileSync(
    path.join(userDataPath, 'kanivet-settings.json'),
    JSON.stringify({
      telemetryEnabled: true,
      sentryLogsEnabled: false,
    }),
    'utf8',
  );

  const store = new Store();
  const result = runLegacyHostedIdentityCleanup({
    userDataPath,
    migrationStore: store,
    platform: 'linux',
    env: {},
  });

  assert.equal(result.deletedFiles, 2);
  assert.equal(result.updatedFiles, 1);
  assert.equal(result.removedKeys, 8);
  assert.equal(
    fs.existsSync(path.join(userDataPath, 'kanivet-auth.json')),
    false,
  );
  assert.equal(
    fs.existsSync(path.join(dedicatedConfigDir, 'config.json')),
    false,
  );

  const scrubbedSharedConfig = JSON.parse(
    fs.readFileSync(path.join(sharedConfigDir, 'config.json'), 'utf8'),
  );
  assert.deepEqual(scrubbedSharedConfig, {
    telemetryEnabled: false,
    ssoSessions: [{ startUrl: 'https://d-123.awsapps.com/start' }],
    selectedCluster: 'dev-cluster',
  });

  const untouchedSettings = JSON.parse(
    fs.readFileSync(path.join(userDataPath, 'kanivet-settings.json'), 'utf8'),
  );
  assert.deepEqual(untouchedSettings, {
    telemetryEnabled: true,
    sentryLogsEnabled: false,
  });
  assert.equal(
    store.get(LEGACY_HOSTED_IDENTITY_MIGRATION_KEY),
    true,
  );
});

test('records completion and skips future runs once the migration succeeds', () => {
  const { userDataPath } = createTempLayout();
  const store = new Store();

  fs.writeFileSync(
    path.join(userDataPath, 'kanivet-auth.json'),
    JSON.stringify({
      auth_tokens: '{"refresh_token":"secret"}',
      token_obtained_at: 1234,
    }),
    'utf8',
  );

  const firstRun = runLegacyHostedIdentityCleanup({
    userDataPath,
    migrationStore: store,
    platform: 'linux',
    env: {},
  });
  assert.equal(firstRun.deletedFiles, 1);

  fs.writeFileSync(
    path.join(userDataPath, 'kanivet-auth.json'),
    JSON.stringify({
      auth_tokens: '{"refresh_token":"secret"}',
      token_obtained_at: 1234,
    }),
    'utf8',
  );

  const secondRun = runLegacyHostedIdentityCleanup({
    userDataPath,
    migrationStore: store,
    platform: 'linux',
    env: {},
  });

  assert.equal(secondRun.alreadyRan, true);
  assert.equal(
    fs.existsSync(path.join(userDataPath, 'kanivet-auth.json')),
    true,
  );
});

test('preserves the original shared config when atomic replacement fails', () => {
  const { root, userDataPath } = createTempLayout();
  const sharedConfigDir = path.join(root, 'kanivet-frontend');
  const sharedConfigPath = path.join(sharedConfigDir, 'config.json');
  fs.mkdirSync(sharedConfigDir, { recursive: true });

  const originalConfig = JSON.stringify({
    auth_tokens: '{"refresh_token":"secret"}',
    telemetryEnabled: false,
    ssoSessions: [{ startUrl: 'https://d-123.awsapps.com/start' }],
  });
  fs.writeFileSync(sharedConfigPath, originalConfig, 'utf8');

  const failingFs = Object.create(fs);
  failingFs.renameSync = () => {
    throw new Error('simulated interrupted replacement');
  };
  const store = new Store();

  const result = runLegacyHostedIdentityCleanup({
    userDataPath,
    migrationStore: store,
    fsModule: failingFs,
    platform: 'linux',
    env: {},
  });

  assert.equal(result.errorCount, 1);
  assert.equal(fs.readFileSync(sharedConfigPath, 'utf8'), originalConfig);
  assert.equal(store.get(LEGACY_HOSTED_IDENTITY_MIGRATION_KEY), undefined);
  assert.deepEqual(
    fs.readdirSync(sharedConfigDir).filter((name) => name.endsWith('.tmp')),
    [],
  );
});
