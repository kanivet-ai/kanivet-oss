const test = require('node:test');
const assert = require('node:assert/strict');

class Storage {
  constructor(initialValues = {}) {
    this.values = new Map(Object.entries(initialValues));
  }

  get length() {
    return this.values.size;
  }

  key(index) {
    return [...this.values.keys()][index] ?? null;
  }

  getItem(key) {
    return this.values.has(key) ? this.values.get(key) : null;
  }

  setItem(key, value) {
    this.values.set(key, String(value));
  }

  removeItem(key) {
    this.values.delete(key);
  }
}

test('removes only legacy chat-session keys and preserves unrelated localStorage state', () => {
  return import('./legacyChatSessionMigration.mjs').then(
    ({ LEGACY_CHAT_SESSION_MIGRATION_KEY, runLegacyChatSessionMigration }) => {
      const storage = new Storage({
        'kanivet.chatSessions.cluster-a': '[{"id":"1"}]',
        'kanivet.chatSessions.cluster-b': '[{"id":"2"}]',
        'kanivet.currentTab': 'cluster-a',
        'kanivet.telemetryEnabled': 'false',
      });

      const result = runLegacyChatSessionMigration(storage);

      assert.equal(result.alreadyRan, false);
      assert.equal(result.removedCount, 2);
      assert.equal(storage.getItem('kanivet.chatSessions.cluster-a'), null);
      assert.equal(storage.getItem('kanivet.chatSessions.cluster-b'), null);
      assert.equal(storage.getItem('kanivet.currentTab'), 'cluster-a');
      assert.equal(storage.getItem('kanivet.telemetryEnabled'), 'false');
      assert.equal(storage.getItem(LEGACY_CHAT_SESSION_MIGRATION_KEY), 'true');
    },
  );
});

test('becomes a no-op after the migration marker is written', () => {
  return import('./legacyChatSessionMigration.mjs').then(
    ({ LEGACY_CHAT_SESSION_MIGRATION_KEY, runLegacyChatSessionMigration }) => {
      const storage = new Storage({
        [LEGACY_CHAT_SESSION_MIGRATION_KEY]: 'true',
        'kanivet.chatSessions.cluster-a': '[{"id":"1"}]',
        'kanivet.currentTab': 'cluster-a',
      });

      const result = runLegacyChatSessionMigration(storage);

      assert.equal(result.alreadyRan, true);
      assert.equal(result.removedCount, 0);
      assert.equal(
        storage.getItem('kanivet.chatSessions.cluster-a'),
        '[{"id":"1"}]',
      );
      assert.equal(storage.getItem('kanivet.currentTab'), 'cluster-a');
    },
  );
});
