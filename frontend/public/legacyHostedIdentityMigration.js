const fs = require('fs');
const path = require('path');

const LEGACY_HOSTED_IDENTITY_MIGRATION_KEY =
  'migrations.removeHostedIdentityData.2026-08';
const LEGACY_HOSTED_IDENTITY_KEYS = Object.freeze([
  'auth_tokens',
  'token_obtained_at',
  'last_activity',
]);
const LEGACY_APP_DATA_DIR = 'kanivet-frontend';
const LEGACY_AUTH_STORE_DIR = 'kanivet-auth';

function resolvePlatformConfigDir({
  userDataPath,
  platform = process.platform,
  env = process.env,
  homeDir,
  pathModule = path,
}) {
  if (userDataPath) {
    return pathModule.dirname(userDataPath);
  }

  const home = homeDir || env.HOME || env.USERPROFILE;
  if (!home) return null;

  if (platform === 'darwin') {
    return pathModule.join(home, 'Library', 'Application Support');
  }

  if (platform === 'win32') {
    return env.APPDATA || pathModule.join(home, 'AppData', 'Roaming');
  }

  return env.XDG_CONFIG_HOME || pathModule.join(home, '.config');
}

function resolveLegacyHostedIdentityTargets({
  userDataPath,
  platform = process.platform,
  env = process.env,
  homeDir,
  pathModule = path,
}) {
  const targets = new Map();
  const configRoot = resolvePlatformConfigDir({
    userDataPath,
    platform,
    env,
    homeDir,
    pathModule,
  });

  const addTarget = (filePath, kind) => {
    if (!filePath) return;
    const resolvedPath = pathModule.resolve(filePath);
    if (!targets.has(resolvedPath)) {
      targets.set(resolvedPath, { filePath: resolvedPath, kind });
    }
  };

  if (userDataPath) {
    addTarget(
      pathModule.join(userDataPath, `${LEGACY_AUTH_STORE_DIR}.json`),
      'dedicated',
    );

    const currentUserDataName = pathModule.basename(userDataPath).toLowerCase();
    if (
      currentUserDataName === LEGACY_APP_DATA_DIR ||
      currentUserDataName === LEGACY_AUTH_STORE_DIR
    ) {
      addTarget(
        pathModule.join(userDataPath, 'config.json'),
        currentUserDataName === LEGACY_AUTH_STORE_DIR ? 'dedicated' : 'shared',
      );
    }
  }

  if (configRoot) {
    addTarget(
      pathModule.join(configRoot, LEGACY_APP_DATA_DIR, 'config.json'),
      'shared',
    );
    addTarget(
      pathModule.join(
        configRoot,
        LEGACY_APP_DATA_DIR,
        `${LEGACY_AUTH_STORE_DIR}.json`,
      ),
      'dedicated',
    );
    addTarget(
      pathModule.join(configRoot, LEGACY_AUTH_STORE_DIR, 'config.json'),
      'dedicated',
    );
  }

  return [...targets.values()];
}

function scrubLegacyHostedIdentityRecord(record) {
  const nextRecord = { ...record };
  const removedKeys = [];

  for (const key of LEGACY_HOSTED_IDENTITY_KEYS) {
    if (Object.prototype.hasOwnProperty.call(nextRecord, key)) {
      delete nextRecord[key];
      removedKeys.push(key);
    }
  }

  return {
    changed: removedKeys.length > 0,
    removedKeys,
    nextRecord,
  };
}

function replaceJsonFileAtomically({
  filePath,
  record,
  fsModule = fs,
  pathModule = path,
}) {
  const stats = fsModule.statSync(filePath);
  const tempPath = pathModule.join(
    pathModule.dirname(filePath),
    `.${pathModule.basename(filePath)}.${process.pid}.${Date.now()}.tmp`,
  );

  try {
    fsModule.writeFileSync(tempPath, `${JSON.stringify(record, null, 2)}\n`, {
      encoding: 'utf8',
      mode: stats.mode,
    });
    fsModule.chmodSync(tempPath, stats.mode);
    fsModule.renameSync(tempPath, filePath);
  } catch (error) {
    try {
      fsModule.unlinkSync(tempPath);
    } catch {}
    throw error;
  }
}

function runLegacyHostedIdentityCleanup({
  userDataPath,
  migrationStore,
  fsModule = fs,
  pathModule = path,
  platform = process.platform,
  env = process.env,
  homeDir,
  log = () => {},
}) {
  if (
    migrationStore?.get &&
    migrationStore.get(LEGACY_HOSTED_IDENTITY_MIGRATION_KEY) === true
  ) {
    return {
      alreadyRan: true,
      updatedFiles: 0,
      deletedFiles: 0,
      removedKeys: 0,
      scannedFiles: 0,
      errorCount: 0,
    };
  }

  const targets = resolveLegacyHostedIdentityTargets({
    userDataPath,
    platform,
    env,
    homeDir,
    pathModule,
  });

  const result = {
    alreadyRan: false,
    updatedFiles: 0,
    deletedFiles: 0,
    removedKeys: 0,
    scannedFiles: 0,
    errorCount: 0,
  };

  for (const target of targets) {
    if (!fsModule.existsSync(target.filePath)) continue;
    result.scannedFiles += 1;

    let parsed;
    try {
      parsed = JSON.parse(fsModule.readFileSync(target.filePath, 'utf8'));
    } catch (error) {
      result.errorCount += 1;
      log(
        `[Migration] Skipped unreadable legacy store ${target.filePath}: ${error.message}`,
      );
      continue;
    }

    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      continue;
    }

    const { changed, removedKeys, nextRecord } =
      scrubLegacyHostedIdentityRecord(parsed);
    if (!changed) continue;

    result.removedKeys += removedKeys.length;
    const remainingKeyCount = Object.keys(nextRecord).length;

    try {
      if (remainingKeyCount === 0) {
        fsModule.unlinkSync(target.filePath);
        result.deletedFiles += 1;
      } else {
        replaceJsonFileAtomically({
          filePath: target.filePath,
          record: nextRecord,
          fsModule,
          pathModule,
        });
        result.updatedFiles += 1;
      }
    } catch (error) {
      result.errorCount += 1;
      log(
        `[Migration] Failed to update legacy store ${target.filePath}: ${error.message}`,
      );
      continue;
    }

    const action = remainingKeyCount === 0 ? 'deleted' : 'scrubbed';
    log(`[Migration] ${action} legacy hosted identity store ${target.filePath}`);
  }

  if (migrationStore?.set && result.errorCount === 0) {
    migrationStore.set(LEGACY_HOSTED_IDENTITY_MIGRATION_KEY, true);
  }

  return result;
}

module.exports = {
  LEGACY_HOSTED_IDENTITY_KEYS,
  LEGACY_HOSTED_IDENTITY_MIGRATION_KEY,
  replaceJsonFileAtomically,
  resolveLegacyHostedIdentityTargets,
  runLegacyHostedIdentityCleanup,
};
