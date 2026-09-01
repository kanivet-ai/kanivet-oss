export const LEGACY_CHAT_SESSION_PREFIX = 'kanivet.chatSessions.';
export const LEGACY_CHAT_SESSION_MIGRATION_KEY =
  'kanivet.migrations.removeChatSessions.2026-08';

export function runLegacyChatSessionMigration(storage) {
  if (!storage) {
    return {
      alreadyRan: false,
      removedCount: 0,
      removedKeys: [],
    };
  }

  try {
    if (storage.getItem(LEGACY_CHAT_SESSION_MIGRATION_KEY) === 'true') {
      return {
        alreadyRan: true,
        removedCount: 0,
        removedKeys: [],
      };
    }

    const removedKeys = [];
    for (let index = 0; index < storage.length; index += 1) {
      const key = storage.key(index);
      if (key && key.startsWith(LEGACY_CHAT_SESSION_PREFIX)) {
        removedKeys.push(key);
      }
    }

    for (const key of removedKeys) {
      storage.removeItem(key);
    }

    storage.setItem(LEGACY_CHAT_SESSION_MIGRATION_KEY, 'true');

    return {
      alreadyRan: false,
      removedCount: removedKeys.length,
      removedKeys,
    };
  } catch {
    return {
      alreadyRan: false,
      removedCount: 0,
      removedKeys: [],
      failed: true,
    };
  }
}
