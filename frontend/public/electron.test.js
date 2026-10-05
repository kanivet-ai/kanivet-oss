const test = require('node:test');
const assert = require('node:assert/strict');
const EventEmitter = require('node:events');
const Module = require('node:module');
const path = require('node:path');

const electronMainPath = path.join(__dirname, 'electron.js');

function createDeferred() {
  let resolve;
  let reject;
  const promise = new Promise((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

// Loads electron.js against fakes. The fakes stay installed until the test
// ends, so modules electron.js requires lazily resolve to them too.
function loadElectronMain(t) {
  delete require.cache[require.resolve(electronMainPath)];

  class FakeBrowserWindow extends EventEmitter {
    static instances = [];

    static getAllWindows() {
      return FakeBrowserWindow.instances.filter((window) => !window.isDestroyed());
    }

    constructor(options) {
      super();
      this.options = options;
      this.minimized = false;
      this.destroyed = false;
      this.loadedUrls = [];
      this.webContents = new EventEmitter();
      this.webContents.openDevTools = () => {};
      this.webContents.setWindowOpenHandler = () => ({ action: 'deny' });
      this.webContents.send = () => {};
      FakeBrowserWindow.instances.push(this);
    }

    loadURL(url) {
      this.loadedUrls.push(url);
    }

    isDestroyed() {
      return this.destroyed;
    }

    isMinimized() {
      return this.minimized;
    }

    restore() {
      this.minimized = false;
    }

    show() {}

    focus() {}

    destroy() {
      if (this.destroyed) return;
      this.destroyed = true;
      this.emit('closed');
    }
  }

  class FakeTray extends EventEmitter {
    setToolTip() {}
    setContextMenu() {}
    setImage() {}
    setTitle() {}
  }

  class FakeStore {
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

  const app = new EventEmitter();
  app.isPackaged = true;
  app.requestSingleInstanceLock = () => true;
  app.whenReady = () => new Promise(() => {});
  app.quit = () => {};
  app.exit = () => {};
  app.relaunch = () => {};
  app.getAppPath = () => '/Applications/Kanivet.app/Contents/Resources/app.asar';
  app.getPath = () => '/tmp';
  app.getVersion = () => '0.0.0-test';

  const autoUpdater = new EventEmitter();
  autoUpdater.checkForUpdates = async () => ({ updateInfo: null });
  const ipcHandlers = new Map();
  const requested = [];

  const icon = {
    resize() {
      return this;
    },
    setTemplateImage() {},
  };

  const mocks = {
    electron: {
      app,
      BrowserWindow: FakeBrowserWindow,
      ipcMain: {
        handle(channel, handler) {
          ipcHandlers.set(channel, handler);
        },
      },
      shell: { openExternal() {} },
      Menu: {
        buildFromTemplate: () => ({}),
        setApplicationMenu() {},
      },
      Tray: FakeTray,
      nativeImage: {
        createFromPath: () => icon,
      },
      dialog: {
        showErrorBox() {},
        showMessageBoxSync: () => 1,
      },
    },
    'electron-store': FakeStore,
    'electron-updater': { autoUpdater },
    './legacyHostedIdentityMigration': {
      runLegacyHostedIdentityCleanup: () => ({ errorCount: 0 }),
    },
  };

  const originalLoad = Module._load;
  Module._load = function patchedLoad(request, parent, isMain) {
    if (Object.hasOwn(mocks, request)) {
      requested.push(request);
      return mocks[request];
    }
    return originalLoad.call(this, request, parent, isMain);
  };
  t.after(() => {
    Module._load = originalLoad;
  });

  const electronMain = require(electronMainPath);
  return {
    electronMain,
    BrowserWindow: FakeBrowserWindow,
    FakeStore,
    autoUpdater,
    ipcHandlers,
    requested,
  };
}

test('startup activate race does not create a duplicate frontend window', async (t) => {
  const { electronMain, BrowserWindow } = loadElectronMain(t);
  const backendStartup = createDeferred();

  const appReady = electronMain.handleAppReady({
    runLegacyHostedIdentityCleanupImpl: () => ({ errorCount: 0 }),
    startBackendImpl: () => backendStartup.promise,
    createTrayImpl: () => {},
    initDynamicIslandImpl: () => {},
    startPeriodicUpdateCheckImpl: () => {},
  });

  electronMain.handleActivate();
  assert.equal(BrowserWindow.instances.length, 1);

  backendStartup.resolve();
  await appReady;

  assert.equal(BrowserWindow.instances.length, 1);
});

test('createWindow still recreates the frontend after the prior window is destroyed', (t) => {
  const { electronMain, BrowserWindow } = loadElectronMain(t);

  const firstWindow = electronMain.createWindow();
  firstWindow.destroy();

  const replacementWindow = electronMain.createWindow();

  assert.notEqual(replacementWindow, firstWindow);
  assert.equal(BrowserWindow.instances.length, 2);
  assert.equal(BrowserWindow.getAllWindows().length, 1);
});

test('loading the main process requires neither electron-store nor electron-updater', async (t) => {
  const { ipcHandlers, autoUpdater, requested } = loadElectronMain(t);

  assert.ok(!requested.includes('electron-store'));
  assert.ok(!requested.includes('electron-updater'));

  // The updater loads, configured, on first use. (Fake timers: the check
  // arms a 15 s timeout that would otherwise hold the test process open.)
  t.mock.timers.enable({ apis: ['setTimeout'] });
  await ipcHandlers.get('updater:checkForUpdates')();
  assert.ok(requested.includes('electron-updater'));
  assert.equal(autoUpdater.autoDownload, false);
  assert.equal(autoUpdater.listenerCount('update-available'), 1);
});

test('the window opens while the backend starts, and the renderer can pull the port', async (t) => {
  const { electronMain, BrowserWindow, FakeStore, ipcHandlers } = loadElectronMain(t);
  const backendStartup = createDeferred();

  const appReady = electronMain.handleAppReady({
    runLegacyHostedIdentityCleanupImpl: () => ({ errorCount: 0 }),
    settingsStoreImpl: new FakeStore(),
    startBackendImpl: () => backendStartup.promise,
    createTrayImpl: () => {},
    initDynamicIslandImpl: () => {},
    startPeriodicUpdateCheckImpl: () => {},
  });
  assert.equal(BrowserWindow.instances.length, 1);

  // Asked before the backend has printed its port: the answer waits for it.
  let port = null;
  const portAnswered = ipcHandlers.get('backend:getPort')().then((value) => {
    port = value;
  });
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(port, null);

  electronMain.setBackendPort(61234);
  backendStartup.resolve(61234);
  await appReady;
  await portAnswered;
  assert.equal(port, 61234);
});

test('the renderer still gets an answer when the backend never reports a port', async (t) => {
  const { electronMain, FakeStore, ipcHandlers } = loadElectronMain(t);

  await assert.rejects(
    electronMain.handleAppReady({
      runLegacyHostedIdentityCleanupImpl: () => ({ errorCount: 0 }),
      settingsStoreImpl: new FakeStore(),
      startBackendImpl: async () => {
        throw new Error('spawn EACCES');
      },
      createTrayImpl: () => {},
      initDynamicIslandImpl: () => {},
      startPeriodicUpdateCheckImpl: () => {},
    }),
  );

  assert.equal(await ipcHandlers.get('backend:getPort')(), 53727);
});

test('the renderer gets the port even when the rest of startup fails', async (t) => {
  const { electronMain, FakeStore, ipcHandlers } = loadElectronMain(t);

  await assert.rejects(
    electronMain.handleAppReady({
      runLegacyHostedIdentityCleanupImpl: () => ({ errorCount: 0 }),
      settingsStoreImpl: new FakeStore(),
      startBackendImpl: async () => {
        electronMain.setBackendPort(61234);
        return 61234;
      },
      createTrayImpl: () => {
        throw new Error('tray unavailable');
      },
      initDynamicIslandImpl: () => {},
      startPeriodicUpdateCheckImpl: () => {},
    }),
  );

  let timer;
  const unanswered = new Promise((resolve) => {
    timer = setTimeout(resolve, 1000, 'unanswered');
  });
  const port = await Promise.race([ipcHandlers.get('backend:getPort')(), unanswered]);
  clearTimeout(timer);
  assert.equal(port, 61234);
});
