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

function loadElectronMain() {
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
      ipcMain: { handle() {} },
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
      return mocks[request];
    }
    return originalLoad.call(this, request, parent, isMain);
  };

  try {
    const electronMain = require(electronMainPath);
    return {
      electronMain,
      BrowserWindow: FakeBrowserWindow,
    };
  } finally {
    Module._load = originalLoad;
  }
}

test('startup activate race does not create a duplicate frontend window', async () => {
  const { electronMain, BrowserWindow } = loadElectronMain();
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

test('createWindow still recreates the frontend after the prior window is destroyed', () => {
  const { electronMain, BrowserWindow } = loadElectronMain();

  const firstWindow = electronMain.createWindow();
  firstWindow.destroy();

  const replacementWindow = electronMain.createWindow();

  assert.notEqual(replacementWindow, firstWindow);
  assert.equal(BrowserWindow.instances.length, 2);
  assert.equal(BrowserWindow.getAllWindows().length, 1);
});
