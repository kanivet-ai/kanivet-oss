const { app, BrowserWindow, ipcMain, shell, Menu, Tray, nativeImage } = require('electron');
const path = require('path');
const fs = require('fs');
const crypto = require('crypto');
const { spawn, execSync } = require('child_process');
const Store = require('electron-store');
const { autoUpdater } = require('electron-updater');
const {
  runLegacyHostedIdentityCleanup,
} = require('./legacyHostedIdentityMigration');
const isDev = !app.isPackaged && process.argv.includes('--dev');

if (!app.requestSingleInstanceLock()) {
  app.quit();
  process.exit(0);
}

const SESSION_SECRET = crypto.randomBytes(32).toString('hex');
const settingsStore = new Store({ name: 'kanivet-settings' });

// Detect if app is running from macOS App Translocation (read-only sandbox)
function isAppTranslocated() {
  if (process.platform !== 'darwin') return false;
  const appPath = app.getAppPath();
  return appPath.includes('/AppTranslocation/') || 
         appPath.includes('/private/var/folders/');
}

// Check if app is in /Applications folder
function isInApplicationsFolder() {
  if (process.platform !== 'darwin') return true;
  const appPath = app.getAppPath();
  return appPath.startsWith('/Applications/') || 
         appPath.includes('/Applications/');
}

autoUpdater.autoDownload = false;
autoUpdater.autoInstallOnAppQuit = true;
autoUpdater.disableDifferentialDownload = true;

let dynamicIsland = null;
const ISLAND_TYPES = new Set(['success', 'error', 'info', 'warning']);
const ISLAND_ICONS = new Set(['check', 'x', 'warning', 'info', 'spin', 'bluetooth', 'usb-c']);

// The library's hasNotch() only recognizes the 2021 "MacBookPro18/19,x"
// identifiers; Apple's unified "Mac14,x+" scheme (every notched MacBook since
// 2022) fails it, so the island silently never initializes. Detect the notch
// ourselves: known-good legacy models, else new-scheme models whose internal
// display reserves a tall (>=30dip) top inset - the same offset the library
// uses for notch height. Auto-hidden menu bars defeat the geometry check.
function macHasNotch() {
  try {
    const model = require('child_process').execSync('sysctl -n hw.model').toString().trim();
    if (/^MacBookPro(1[8-9]|[2-9]\d),/.test(model)) return true;
    if (!/^Mac\d+,/.test(model)) return false;
    const { screen } = require('electron');
    const display = screen.getAllDisplays().find((d) => d.internal) || screen.getPrimaryDisplay();
    return !!display && display.workArea.y - display.bounds.y >= 30;
  } catch (e) {
    return false;
  }
}

function initDynamicIsland() {
  if (process.platform !== 'darwin') return;
  try {
    const { DynamicIsland } = require('electron-dynamic-island');
    const island = new DynamicIsland({ enableSounds: false });
    island.hasNotch = macHasNotch;
    island.init();
    dynamicIsland = island;
    writeLog(`[Island] initialized (supported: ${island.isSupported ? island.isSupported() : 'unknown'})`);
  } catch (e) {
    writeLog(`[Island] init failed: ${e.message}`);
  }
}

function showIsland(opts) {
  if (!dynamicIsland || !opts || typeof opts.message !== 'string' || !opts.message.trim()) return false;
  try {
    if (dynamicIsland.isSupported && !dynamicIsland.isSupported()) return false;
    const safe = {
      type: ISLAND_TYPES.has(opts.type) ? opts.type : 'info',
      message: opts.message.slice(0, 120),
    };
    if (typeof opts.icon === 'string' && ISLAND_ICONS.has(opts.icon)) safe.icon = opts.icon;
    if (Number.isFinite(opts.duration)) safe.duration = Math.min(Math.max(opts.duration, 1500), 10000);
    dynamicIsland.show(safe);
    return true;
  } catch (e) {
    writeLog(`[Island] show failed: ${e.message}`);
    return false;
  }
}

const UPDATE_CHECK_INTERVAL = 4 * 60 * 60 * 1000;
let updateCheckTimer = null;

if (isDev) {
  require('electron-reload')(__dirname, {
    electron: path.join(__dirname, '..', 'node_modules', '.bin', 'electron'),
    hardResetMethod: 'exit',
  });
}

let mainWindow;
let logStream = null;
let backendProcess;
let backendPort = 53727;
let tray = null;
let activeTabs = [];
let ssoSessions = [];
let ssoAccounts = [];
let activeSSOAccount = null;
let ssoStatusBlinkInterval = null;
let ssoStatusBlinkState = true;
let backendRestartCount = 0;
let isAppQuitting = false;
const backendStderrRing = [];
const BACKEND_STDERR_RING_MAX = 50;
const MAX_BACKEND_RESTARTS = 10;
const RESTART_BACKOFF_MS = [1000, 2000, 4000, 8000, 16000];
const RESTART_BACKOFF_MAX_MS = 16000;

function pushBackendStderr(line) {
  if (!line) return;
  const text = line.toString().trim();
  if (!text) return;
  backendStderrRing.push(`[${new Date().toISOString()}] ${text}`);
  while (backendStderrRing.length > BACKEND_STDERR_RING_MAX) backendStderrRing.shift();
}

function getBackendCrashSignature() {
  const line = [...backendStderrRing].reverse().find((entry) => entry.replace(/^\[[^\]]+\]\s*/, '').trim());
  if (!line) return 'no-stderr';
  return line
    .replace(/^\[[^\]]+\]\s*/, '')
    .replace(/\b\d{4}-\d{2}-\d{2}[^\s]*/g, '<time>')
    .replace(/\bPID \d+\b/gi, 'PID <pid>')
    .replace(/\bpid=\d+\b/gi, 'pid=<pid>')
    .replace(/\/Users\/[^\s:]+/g, '/Users/<user>')
    .replace(/\/home\/[^\s:]+/g, '/home/<user>')
    .slice(0, 200);
}

function getRestartDelay(attempt) {
  return RESTART_BACKOFF_MS[Math.min(attempt, RESTART_BACKOFF_MS.length - 1)] || RESTART_BACKOFF_MAX_MS;
}

function setBackendPort(port) {
  if (!port || Number(port) <= 0) return;
  backendPort = Number(port);
  if (mainWindow && !mainWindow.isDestroyed()) {
    mainWindow.webContents.send('backend:port-changed', backendPort);
  }
}

function getRendererUrl() {
  const base = isDev ? 'http://localhost:5173' : `file://${path.join(__dirname, '../build/index.html')}`;
  const url = new URL(base);
  url.searchParams.set('backendPort', String(backendPort));
  return url.toString();
}

// Only set up file logging in development mode
if (isDev) {
  const logsDir = path.join(app.getPath('userData'), 'logs');
  const logFile = path.join(logsDir, 'frontend-server.log');

  if (!fs.existsSync(logsDir)) {
    fs.mkdirSync(logsDir, { recursive: true });
  }

  if (fs.existsSync(logFile)) {
    fs.unlinkSync(logFile);
  }

  logStream = fs.createWriteStream(logFile, { flags: 'a' });
}

// writeLog only writes to file in development mode
// In production, it's a no-op to avoid filling customer disk space
function writeLog(message) {
  if (!isDev || !logStream) return;
  const timestamp = new Date().toISOString();
  logStream.write(`[${timestamp}] ${message}\n`);
}

function writeErrorLog(message) {
  if (isDev && logStream) {
    const timestamp = new Date().toISOString();
    logStream.write(`[${timestamp}] [ERROR] ${message}\n`);
  }
}

function computeFileHash(filePath) {
  try {
    const data = fs.readFileSync(filePath);
    return crypto.createHash('sha256').update(data).digest('hex');
  } catch (error) {
    writeLog(`[Security] Failed to compute hash for ${filePath}: ${error.message}`);
    return null;
  }
}

function verifyBackendIntegrity(backendPath) {
  // NOTE: Backend integrity check is disabled for now because:
  // 1. macOS code signing modifies the binary, invalidating pre-computed hashes
  // 2. Code signing + notarization already provides integrity verification
  // 3. The app is distributed as a signed DMG which verifies integrity
  // 
  // TODO: For enhanced security, we could:
  // - Verify the binary is properly code-signed using codesign command
  // - Use a different integrity mechanism that survives code signing
  writeLog('[Security] Backend integrity check skipped (code signing provides integrity verification)');
  return true;
}

ipcMain.handle('island:notify', (event, opts) => showIsland(opts));

ipcMain.handle('write-log', (event, message) => {
  writeLog(message);
});

ipcMain.handle('security:getSessionSecret', () => {
  return SESSION_SECRET;
});



let updateInfo = null;
let downloadProgress = null;
let isUpdateAvailable = false;
let isCheckingForUpdate = false;
let isDownloadingUpdate = false;
let isUpdateDownloaded = false;

ipcMain.handle('updater:getAppVersion', () => {
  if (isDev) return '999.0.0';
  return app.getVersion();
});

ipcMain.handle('updater:checkForUpdates', async () => {
  if (isDev) {
    writeLog('[Updater] Skipping update check in dev mode');
    return { updateAvailable: false };
  }

  if (isDownloadingUpdate) {
    writeLog('[Updater] Skipping update check while download is in progress');
    return {
      updateAvailable: true,
      version: updateInfo?.version,
      releaseNotes: updateInfo?.releaseNotes,
      downloading: true,
    };
  }

  if (isUpdateDownloaded) {
    writeLog('[Updater] Skipping update check because update is already downloaded');
    return {
      updateAvailable: true,
      version: updateInfo?.version,
      releaseNotes: updateInfo?.releaseNotes,
      downloaded: true,
    };
  }

  if (isCheckingForUpdate) {
    writeLog('[Updater] Skipping duplicate update check');
    return {
      updateAvailable: isUpdateAvailable,
      version: updateInfo?.version,
      releaseNotes: updateInfo?.releaseNotes,
      checking: true,
    };
  }

  try {
    writeLog('[Updater] Checking for updates...');
    isCheckingForUpdate = true;
    isUpdateAvailable = false;
    const timeoutMs = 15000;
    const result = await Promise.race([
      autoUpdater.checkForUpdates(),
      new Promise((_, reject) => setTimeout(() => reject(new Error('Update check timed out')), timeoutMs))
    ]);
    if (isUpdateAvailable) {
      updateInfo = result?.updateInfo;
    } else {
      updateInfo = null;
    }
    return {
      updateAvailable: isUpdateAvailable,
      version: updateInfo?.version,
      releaseNotes: updateInfo?.releaseNotes,
    };
  } catch (error) {
    writeLog(`[Updater] Error checking for updates: ${error.message}`);
    return { updateAvailable: false, error: error.message };
  } finally {
    isCheckingForUpdate = false;
  }
});

ipcMain.handle('updater:downloadUpdate', async () => {
  if (!updateInfo) {
    throw new Error('No update available');
  }

  if (isDownloadingUpdate) {
    writeLog('[Updater] Download already in progress');
    return { success: true, downloading: true };
  }

  if (isUpdateDownloaded) {
    writeLog('[Updater] Update already downloaded');
    return { success: true, downloaded: true };
  }

  isDownloadingUpdate = true;
  isUpdateDownloaded = false;

  // Clear any stale cached download from a previous attempt. electron-updater's
  // differential-download path is sensitive to corrupted/half-finished caches
  // and can fail SHA-512 verification in a "download → delete → re-download" loop.
  try {
    const cacheDir = autoUpdater.downloadedUpdateHelper?.cacheDir;
    if (cacheDir) {
      const fsSync = require('fs');
      if (fsSync.existsSync(cacheDir)) {
        const entries = fsSync.readdirSync(cacheDir);
        for (const entry of entries) {
          const full = path.join(cacheDir, entry);
          try {
            const stat = fsSync.statSync(full);
            if (stat.isDirectory()) {
              fsSync.rmSync(full, { recursive: true, force: true });
            } else {
              fsSync.unlinkSync(full);
            }
            writeLog(`[Updater] Cleared stale cache entry: ${entry}`);
          } catch (e) {
            writeLog(`[Updater] Failed to clear cache entry ${entry}: ${e.message}`);
          }
        }
      }
    }
  } catch (e) {
    writeLog(`[Updater] Cache cleanup error (non-fatal): ${e.message}`);
  }

  try {
    writeLog('[Updater] Downloading update...');
    await autoUpdater.downloadUpdate();
    return { success: true };
  } catch (error) {
    writeLog(`[Updater] Error downloading update: ${error.message}`);
    isDownloadingUpdate = false;
    throw error;
  }
});

ipcMain.handle('updater:installUpdate', () => {
  writeLog('[Updater] Installing update and restarting...');
  setImmediate(() => {
    app.removeAllListeners('window-all-closed');
    BrowserWindow.getAllWindows().forEach((win) => win.removeAllListeners('close'));
    autoUpdater.autoInstallOnAppQuit = false;
    autoUpdater.quitAndInstall(false, true);
  });
});

ipcMain.handle('updater:getDownloadProgress', () => {
  return downloadProgress;
});

autoUpdater.on('checking-for-update', () => {
  writeLog('[Updater] Checking for update...');
});

autoUpdater.on('update-available', (info) => {
  writeLog(`[Updater] Update available: ${info.version}`);
  isUpdateAvailable = true;
  updateInfo = info;
  if (isDownloadingUpdate || isUpdateDownloaded) {
    writeLog('[Updater] Suppressing update-available notification while update is already in progress');
    return;
  }
  if (mainWindow && !mainWindow.isDestroyed()) {
    mainWindow.webContents.send('updater:update-available', info);
  }
});

autoUpdater.on('update-not-available', (info) => {
  writeLog('[Updater] No update available');
  isUpdateAvailable = false;
  updateInfo = null;
  isUpdateDownloaded = false;
});

autoUpdater.on('error', (err) => {
  writeLog(`[Updater] Error: ${err.message}`);
  isCheckingForUpdate = false;
  isDownloadingUpdate = false;
  if (mainWindow && !mainWindow.isDestroyed()) {
    mainWindow.webContents.send('updater:error', err.message);
  }
});

autoUpdater.on('download-progress', (progressObj) => {
  isDownloadingUpdate = true;
  downloadProgress = progressObj;
  writeLog(`[Updater] Download progress: ${progressObj.percent.toFixed(1)}%`);
  if (mainWindow && !mainWindow.isDestroyed()) {
    mainWindow.webContents.send('updater:download-progress', progressObj);
  }
});

autoUpdater.on('update-downloaded', (info) => {
  writeLog(`[Updater] Update downloaded: ${info.version}`);
  showIsland({ type: 'info', message: `Kanivet ${info.version} ready — restart to apply`, icon: 'info', duration: 6000 });
  downloadProgress = null;
  isDownloadingUpdate = false;
  isUpdateDownloaded = true;
  updateInfo = info;
  if (mainWindow && !mainWindow.isDestroyed()) {
    mainWindow.webContents.send('updater:update-downloaded', info);
  }
});

function checkForUpdatesAutomatically() {
  if (isDev) {
    writeLog('[Updater] Skipping automatic update check in dev mode');
    return;
  }

  if (isCheckingForUpdate || isDownloadingUpdate || isUpdateDownloaded) {
    writeLog('[Updater] Skipping automatic update check because updater is busy');
    return;
  }

  writeLog('[Updater] Performing automatic update check...');
  isCheckingForUpdate = true;
  const timeoutMs = 15000;
  Promise.race([
    autoUpdater.checkForUpdates(),
    new Promise((_, reject) => setTimeout(() => reject(new Error('Update check timed out')), timeoutMs))
  ]).catch((err) => {
    writeLog(`[Updater] Automatic update check failed: ${err.message}`);
  }).finally(() => {
    isCheckingForUpdate = false;
  });
}

function startPeriodicUpdateCheck() {
  if (isDev) return;
  
  setTimeout(() => {
    checkForUpdatesAutomatically();
  }, 10000);
  
  updateCheckTimer = setInterval(() => {
    checkForUpdatesAutomatically();
  }, UPDATE_CHECK_INTERVAL);
  
  writeLog(`[Updater] Periodic update check scheduled every ${UPDATE_CHECK_INTERVAL / 1000 / 60 / 60} hours`);
}

ipcMain.handle('tray:updateTabs', (event, tabs) => {
  activeTabs = tabs || [];
  updateTrayMenu();
});

ipcMain.handle('tray:updateSSOSessions', (event, sessions) => {
  ssoSessions = sessions || [];
  updateTrayMenu();
});

ipcMain.handle('tray:updateSSOAccounts', (event, accounts, active) => {
  ssoAccounts = accounts || [];
  activeSSOAccount = active || null;
  writeLog(`[Tray] SSO accounts updated: ${accounts?.length || 0} accounts, active: ${active?.accountName || 'none'}, expiresAt: ${active?.expiresAt || 'N/A'}`);
  updateTrayMenu();
  updateTrayIcon();
});

function showAndFocusWindow() {
  // Check both that mainWindow exists AND hasn't been destroyed
  // This prevents "Object has been destroyed" errors after long-running sessions
  if (mainWindow && !mainWindow.isDestroyed()) {
    if (mainWindow.isMinimized()) mainWindow.restore();
    mainWindow.show();
    mainWindow.focus();
  } else if (!mainWindow || mainWindow.isDestroyed()) {
    // Window was destroyed (e.g., after sleep/wake), recreate it
    writeLog('[Window] Window was destroyed, recreating...');
    createWindow();
  }
}

function updateTrayMenu() {
  if (!tray) return;

  const clusterItems = activeTabs.length > 0
    ? activeTabs.map((tab, index) => ({
        label: tab.name || tab.id,
        click: () => {
          showAndFocusWindow();
          mainWindow?.webContents.send('tray:switchTab', tab.id);
        },
        accelerator: index < 9 ? `CmdOrCtrl+${index + 1}` : undefined,
      }))
    : [{ label: 'No clusters open', enabled: false }];

  const ssoAccountItems = ssoAccounts.length > 0
    ? ssoAccounts.map((account) => {
        const isActive = activeSSOAccount &&
          activeSSOAccount.startUrl === account.startUrl &&
          activeSSOAccount.accountId === account.accountId;
        return {
          label: `${isActive ? '● ' : ''}${account.accountName}`,
          click: () => {
            mainWindow?.webContents.send('tray:activateAccount', account);
          },
          enabled: !isActive,
        };
      })
    : [{ label: 'No accounts available', enabled: false }];

  const contextMenu = Menu.buildFromTemplate([
    { label: 'Clusters', enabled: false },
    ...clusterItems,
    { type: 'separator' },
    { label: 'AWS SSO Accounts', enabled: false },
    ...ssoAccountItems,
    {
      label: 'Manage SSO...',
      click: () => {
        showAndFocusWindow();
        mainWindow?.webContents.send('tray:addSSO');
      }
    },
    { type: 'separator' },
    {
      label: 'Settings',
      accelerator: 'CmdOrCtrl+,',
      click: () => {
        showAndFocusWindow();
        mainWindow?.webContents.send('tray:openSettings');
      }
    },
    { type: 'separator' },
    { role: 'quit' }
  ]);

  tray.setContextMenu(contextMenu);
}

function getSSOStatus() {
  if (!activeSSOAccount) return 'none';
  const now = Date.now();
  const thirtyMinutes = 30 * 60 * 1000;
  if (activeSSOAccount.expiresAt && now > activeSSOAccount.expiresAt) return 'expired';
  if (activeSSOAccount.expiresAt && activeSSOAccount.expiresAt - now < thirtyMinutes) return 'expiring';
  return 'active';
}

function getBaseIcon() {
  let icon;
  if (process.platform === 'darwin') {
    const templateIconPath = isDev
      ? path.join(__dirname, 'trayIconTemplate.png')
      : path.join(process.resourcesPath, 'resources', 'trayIconTemplate.png');
    if (fs.existsSync(templateIconPath)) {
      icon = nativeImage.createFromPath(templateIconPath);
    } else {
      const fallbackPath = isDev
        ? path.join(__dirname, 'kanivet-icon.png')
        : path.join(process.resourcesPath, 'resources', 'kanivet-icon.png');
      icon = nativeImage.createFromPath(fallbackPath);
    }
    icon = icon.resize({ width: 18, height: 18 });
  } else {
    const iconPath = isDev
      ? path.join(__dirname, 'kanivet-icon.png')
      : path.join(process.resourcesPath, 'resources', 'kanivet-icon.png');
    icon = nativeImage.createFromPath(iconPath);
    icon = icon.resize({ width: 16, height: 16 });
  }
  return icon;
}

function updateTrayIcon() {
  if (!tray) return;
  const status = getSSOStatus();

  if (ssoStatusBlinkInterval) {
    clearInterval(ssoStatusBlinkInterval);
    ssoStatusBlinkInterval = null;
  }

  const icon = getBaseIcon();
  if (process.platform === 'darwin') icon.setTemplateImage(true);
  tray.setImage(icon);

  const statusIndicators = { none: '', active: '•', expiring: '◦', expired: '◦' };
  const tooltips = {
    none: 'Kanivet',
    active: `Kanivet - SSO: ${activeSSOAccount?.accountName || 'Active'}`,
    expiring: `Kanivet - SSO Expiring Soon: ${activeSSOAccount?.accountName || ''}`,
    expired: `Kanivet - SSO Expired: ${activeSSOAccount?.accountName || ''}`,
  };

  tray.setToolTip(tooltips[status] || 'Kanivet');

  if (process.platform === 'darwin' && status !== 'none') {
    tray.setTitle(statusIndicators[status], { fontType: 'monospacedDigit' });
  } else {
    tray.setTitle('');
  }
}

function createTray() {
  const icon = getBaseIcon();
  if (process.platform === 'darwin') icon.setTemplateImage(true);

  writeLog(`[Tray] Creating tray icon`);

  tray = new Tray(icon);
  tray.setToolTip('Kanivet');

  tray.on('click', () => {
    showAndFocusWindow();
  });

  updateTrayMenu();
  updateTrayIcon();
  writeLog('[Tray] Tray created successfully');
}

function createWindow() {
  if (mainWindow && !mainWindow.isDestroyed()) {
    return mainWindow;
  }

  const existingWindow = BrowserWindow.getAllWindows().find((window) => !window.isDestroyed());
  if (existingWindow) {
    mainWindow = existingWindow;
    return mainWindow;
  }

  const preloadPath = path.join(__dirname, 'preload.js');

  mainWindow = new BrowserWindow({
    width: 1400,
    height: 900,
    webPreferences: {
      nodeIntegration: false,
      contextIsolation: true,
      preload: preloadPath,
      devTools: isDev || process.env.KANIVET_DEVTOOLS === '1',
    },
  });

  mainWindow.loadURL(getRendererUrl());

  if (isDev) {
    mainWindow.webContents.openDevTools();
  }

  mainWindow.webContents.on('will-navigate', (event, url) => {
    const allowedOrigins = ['http://localhost:5173', 'http://localhost:3000', 'file://'];
    const isAllowed = allowedOrigins.some(origin => url.startsWith(origin));
    if (!isAllowed) {
      event.preventDefault();
      writeLog(`[Navigation] Blocked internal navigation to: ${url}, opening externally`);
      shell.openExternal(url);
    }
  });

  mainWindow.webContents.setWindowOpenHandler(({ url }) => {
    writeLog(`[Navigation] New window request for: ${url}, opening externally`);
    shell.openExternal(url);
    return { action: 'deny' };
  });

  if (!isDev) {
    mainWindow.webContents.on('before-input-event', (event, input) => {
      if (input.key === 'F12' || 
          (input.control && input.shift && input.key.toLowerCase() === 'i') ||
          (input.meta && input.alt && input.key.toLowerCase() === 'i') ||
          (input.control && input.shift && input.key.toLowerCase() === 'j') ||
          (input.meta && input.alt && input.key.toLowerCase() === 'j')) {
        event.preventDefault();
        writeLog('[Security] Blocked DevTools keyboard shortcut');
      }
    });
  }

  // Clean up reference when window is closed to prevent "Object has been destroyed" errors
  mainWindow.on('closed', () => {
    writeLog('[Window] Window closed, clearing reference');
    mainWindow = null;
  });

  createMenu();
  return mainWindow;
}

function createMenu() {
  const viewSubmenu = [
    { role: 'reload' },
    { role: 'forceReload' },
    { type: 'separator' },
    { role: 'resetZoom' },
    { role: 'zoomIn' },
    { role: 'zoomOut' },
    { type: 'separator' },
    { role: 'togglefullscreen' }
  ];

  if (isDev) {
    viewSubmenu.splice(2, 0, { role: 'toggleDevTools' });
  }

  const template = [
    {
      label: 'File',
      submenu: [
        { role: 'quit' }
      ]
    },
    {
      label: 'Edit',
      submenu: [
        { role: 'undo' },
        { role: 'redo' },
        { type: 'separator' },
        { role: 'cut' },
        { role: 'copy' },
        { role: 'paste' },
        { role: 'selectAll' }
      ]
    },
    {
      label: 'View',
      submenu: viewSubmenu
    },
    {
      label: 'Preferences',
      submenu: [
        {
          label: 'Clear Cache',
          click: () => {
            if (mainWindow && !mainWindow.isDestroyed()) {
              mainWindow.webContents.send('clear-cache');
            }
          }
        }
      ]
    }
  ];

  if (process.platform === 'darwin') {
    template.unshift({
      label: app.name,
      submenu: [
        { role: 'about' },
        {
          label: 'Check for Updates...',
          click: () => {
            mainWindow?.webContents.send('menu:check-for-updates');
          }
        },
        { type: 'separator' },
        {
          label: 'Clear Cache',
          accelerator: 'Command+Shift+Delete',
          click: () => {
            if (mainWindow && !mainWindow.isDestroyed()) {
              mainWindow.webContents.send('clear-cache');
            }
          }
        },
        { type: 'separator' },
        { role: 'services' },
        { type: 'separator' },
        { role: 'hide' },
        { role: 'hideOthers' },
        { role: 'unhide' },
        { type: 'separator' },
        { role: 'quit' }
      ]
    });

    template.splice(1, 1);
  } else {
    template.push({
      label: 'Help',
      submenu: [
        {
          label: 'Check for Updates...',
          click: () => {
            mainWindow?.webContents.send('menu:check-for-updates');
          }
        }
      ]
    });
  }

  const menu = Menu.buildFromTemplate(template);
  Menu.setApplicationMenu(menu);
}

function showAppTranslocationWarning() {
  const { dialog } = require('electron');
  
  const result = dialog.showMessageBoxSync({
    type: 'warning',
    title: 'Move Kanivet to Applications',
    message: 'Kanivet needs to be moved to your Applications folder',
    detail: 'macOS security prevents the app from running properly in its current location.\n\n' +
            'To fix this:\n' +
            '1. Close this dialog\n' +
            '2. Drag Kanivet.app to your Applications folder\n' +
            '3. Open Kanivet from Applications\n\n' +
            'This is a one-time setup step required by macOS.',
    buttons: ['Open Applications Folder', 'Quit'],
    defaultId: 0,
    cancelId: 1,
  });
  
  if (result === 0) {
    // Open Finder at /Applications
    shell.openPath('/Applications');
  }
  
  app.quit();
}

function showBackendGiveUpDialog(stderrTail) {
  try {
    const { dialog } = require('electron');
    const detail = stderrTail ? `Last backend output:\n\n${stderrTail.slice(-2000)}` : 'No backend output captured.';
    const result = dialog.showMessageBoxSync({
      type: 'error',
      title: 'Kanivet Backend Failed',
      message: 'The Kanivet backend crashed repeatedly and could not recover.',
      detail,
      buttons: ['Restart App', 'Quit'],
      defaultId: 0,
      cancelId: 1,
    });
    if (result === 0) {
      app.relaunch();
    }
    isAppQuitting = true;
    killBackendProcess();
    if (logStream) logStream.end();
    if (updateCheckTimer) clearInterval(updateCheckTimer);
    if (ssoStatusBlinkInterval) clearInterval(ssoStatusBlinkInterval);
    app.exit(1);
  } catch (e) {
    writeLog(`[Backend] Failed to show give-up dialog: ${e.message}`);
  }
}

function killExistingBackend(port) {
  const ownPid = backendProcess && backendProcess.pid;
  let portHolderInfo = null;
  try {
    const platform = process.platform;
    if (platform === 'darwin' || platform === 'linux') {
      const result = execSync(`lsof -ti:${port} 2>/dev/null || true`, { encoding: 'utf8' }).trim();
      if (!result) return null;
      const pids = result.split('\n').filter(Boolean);
      pids.forEach(pid => {
        try {
          const procName = execSync(`ps -p ${pid} -o comm= 2>/dev/null || true`, { encoding: 'utf8' }).trim();
          const numericPid = Number(pid);
          if (ownPid && numericPid === ownPid) {
            execSync(`kill -9 ${pid} 2>/dev/null || true`);
            writeLog(`[Backend] Killed our own backend (PID ${pid}) on port ${port}`);
          } else {
            portHolderInfo = { pid: numericPid, name: procName };
            writeLog(`[Backend] Port ${port} in use by ${procName} (PID ${pid}), not killing (not our child)`);
          }
        } catch (e) {
          writeLog(`[Backend] Failed to check/kill PID ${pid}: ${e.message}`);
        }
      });
    } else if (platform === 'win32') {
      const result = execSync(`netstat -aon | findstr :${port} | findstr LISTENING`, { encoding: 'utf8', shell: true }).trim();
      if (!result) return null;
      const pid = result.split(/\s+/).pop();
      const numericPid = Number(pid);
      const taskInfo = execSync(`tasklist /FI "PID eq ${pid}" /FO CSV /NH`, { encoding: 'utf8', shell: true });
      if (ownPid && numericPid === ownPid) {
        execSync(`taskkill /F /PID ${pid}`, { shell: true });
        writeLog(`[Backend] Killed our own backend (PID ${pid}) on port ${port}`);
      } else {
        portHolderInfo = { pid: numericPid, name: taskInfo.split(',')[0]?.replace(/"/g, '') || 'unknown' };
        writeLog(`[Backend] Port ${port} in use by external process (PID ${pid}), not killing`);
      }
    }
  } catch (e) {
    writeLog(`[Backend] Backend cleanup error: ${e.message || 'No existing backend'}`);
  }
  return portHolderInfo;
}

function sleep(ms) {
  return new Promise(resolve => setTimeout(resolve, ms));
}

function getBackendArch(arch = process.arch) {
  return arch === 'x64' ? 'amd64' : arch;
}

function getBackendCandidates(platform = process.platform, arch = process.arch) {
  const normalizedArch = getBackendArch(arch);
  const candidates = platform === 'win32'
    ? ['kanivet-backend.exe', `kanivet-backend-windows-${normalizedArch}.exe`]
    : ['kanivet-backend', `kanivet-backend-${platform}-${normalizedArch}`];

  return candidates.map(candidate => path.join(process.resourcesPath, 'resources', candidate));
}

async function startBackend() {
  if (isDev) {
    killExistingBackend(53727);
    await sleep(500);
    return backendPort;
  }
  {
    const platform = process.platform;
    const backendCandidates = getBackendCandidates(platform, process.arch);
    const backendPath = backendCandidates.find(candidate => fs.existsSync(candidate)) || backendCandidates[0];

    const isTranslocated = isAppTranslocated();
    const inApplications = isInApplicationsFolder();
    writeLog(`[Backend] App location diagnostics: translocated=${isTranslocated}, inApplications=${inApplications}`);
    writeLog(`[Backend] App path: ${app.getAppPath()}`);
    writeLog(`[Backend] Backend candidates: ${backendCandidates.join(', ')}`);
    writeLog(`[Backend] Looking for backend at: ${backendPath}`);


    if (fs.existsSync(backendPath)) {
      if (!verifyBackendIntegrity(backendPath)) {
        const { dialog } = require('electron');
        dialog.showErrorBox('Security Error',
          'Application integrity check failed.\n\n' +
          'The backend binary appears to have been modified.\n' +
          'Please reinstall Kanivet from the official source.');
        app.quit();
        return;
      }

      writeLog(`[Backend] Starting backend from: ${backendPath}`);
      
      // Try to set execute permissions, but don't fail if it doesn't work
      // The binary should already have correct permissions from code signing
      // Even in translocated locations, the binary may work if permissions were set at build time
      try {
        fs.chmodSync(backendPath, '755');
        writeLog('[Backend] Set execute permissions successfully');
      } catch (chmodError) {
        writeLog(`[Backend] chmod failed (this is OK if app is signed): ${chmodError.message}`);
        writeLog('[Backend] Proceeding anyway - binary should have correct permissions from build');
      }

      const delimiter = path.delimiter;
      const currentPath = process.env.PATH || '';
      const pathParts = currentPath.split(delimiter).filter(Boolean);
      const extraPaths = [];
      const homeDir = process.env.HOME || '';

      if (platform === 'darwin') {
        extraPaths.push('/opt/homebrew/bin', '/usr/local/bin');
        if (homeDir) {
          extraPaths.push(
            `${homeDir}/google-cloud-sdk/bin`,
            `${homeDir}/.local/bin`
          );
        }
        extraPaths.push(
          // Homebrew cask installations
          '/usr/local/Caskroom/google-cloud-sdk/latest/google-cloud-sdk/bin',
          '/opt/homebrew/Caskroom/google-cloud-sdk/latest/google-cloud-sdk/bin',
          // Homebrew formula installations (brew install google-cloud-sdk)
          '/opt/homebrew/share/google-cloud-sdk/bin',
          '/usr/local/share/google-cloud-sdk/bin'
        );
      } else if (platform === 'linux') {
        extraPaths.push('/usr/local/bin', '/usr/bin', '/snap/bin');
        if (homeDir) {
          extraPaths.push(
            `${homeDir}/google-cloud-sdk/bin`,
            `${homeDir}/.local/bin`
          );
        }
        // Azure CLI on Debian/Ubuntu
        extraPaths.push('/opt/az/bin');
      }

      const mergedPath = Array.from(
        new Set([...extraPaths, ...pathParts]),
      ).join(delimiter);
      writeLog(`[Backend] Using PATH: ${mergedPath}`);

      backendProcess = spawn(backendPath, [], {
        stdio: ['pipe', 'pipe', 'pipe'],
        env: {
          ...process.env,
          PATH: mergedPath,
          PORT: '0',
          KANIVET_SESSION_SECRET: SESSION_SECRET,
        },
        cwd: process.resourcesPath,
      });

      let stdoutBuffer = '';
      let startupResolved = false;
      const resolveStartup = (port) => {
        if (startupResolved) return;
        startupResolved = true;
        if (port) setBackendPort(port);
      };

      backendProcess.stdout.on('data', (data) => {
        const text = data.toString();
        writeLog(`[Backend] ${text}`);
        stdoutBuffer += text.replace(/\r/g, '');
        const lines = stdoutBuffer.split('\n');
        stdoutBuffer = lines.pop() || '';
        lines.forEach((line) => {
          const match = line.trim().match(/^KANIVET_PORT=(\d+)$/);
          if (match) resolveStartup(Number(match[1]));
        });
      });

      backendProcess.stderr.on('data', (data) => {
        writeLog(`[Backend Error] ${data}`);
        data.toString().split(/\r?\n/).forEach(pushBackendStderr);
      });

      backendProcess.on('error', (error) => {
        resolveStartup();
        writeLog(`[Backend Error] Failed to start: ${error.message}`);
        

        // If backend fails to start due to permissions and we're translocated, show helpful message
        if ((error.code === 'EACCES' || error.code === 'EPERM') && isTranslocated) {
          showAppTranslocationWarning();
        } else if (error.code === 'EACCES' || error.code === 'EPERM') {
          const { dialog } = require('electron');
          dialog.showErrorBox('Permission Error',
            'Kanivet could not start the backend service due to permission issues.\n\n' +
            'Please try:\n' +
            '1. Moving Kanivet to your Applications folder\n' +
            '2. Or right-click the app and select "Open" to bypass Gatekeeper');
        }
      });

      const startTime = Date.now();
      const spawnedProcess = backendProcess;
      const stableTimer = setTimeout(() => {
        if (backendProcess === spawnedProcess && spawnedProcess.exitCode === null) {
          backendRestartCount = 0;
        }
      }, 30000);
      backendProcess.on('exit', (code, signal) => {
        clearTimeout(stableTimer);
        resolveStartup();
        const runTime = Date.now() - startTime;
        writeLog(`[Backend] Exited code=${code} signal=${signal} runtime=${runTime}ms`);

        if (isAppQuitting) return;

        if (code === 0) {
          backendRestartCount = 0;
          return;
        }

        if (runTime >= 30000) backendRestartCount = 0;

        const exitDescriptor = code !== null ? `exit code ${code}` : `signal ${signal || 'unknown'}`;
        const stderrTail = backendStderrRing.slice(-50).join('\n');

        if (backendRestartCount >= MAX_BACKEND_RESTARTS) {
          writeLog('[Backend] Max restarts reached, not restarting');
          showBackendGiveUpDialog(stderrTail);
          return;
        }

        backendRestartCount++;
        const delay = getRestartDelay(backendRestartCount - 1);
        writeLog(`[Backend] Restarting (attempt ${backendRestartCount}/${MAX_BACKEND_RESTARTS}) in ${delay}ms...`);

        setTimeout(() => {
          startBackend();
        }, delay);
      });

      return new Promise((resolve) => {
        const wait = () => startupResolved ? resolve(backendPort) : setTimeout(wait, 25);
        wait();
      });
    } else {
      const errorMsg = `Backend binary not found at: ${backendPath}`;
      writeLog(`[Backend Error] ${errorMsg}`);
      writeLog(`[Backend Error] Attempted locations: ${backendCandidates.join(', ')}`);
      writeLog(`[Backend Error] Resources path: ${process.resourcesPath}`);
      writeLog(`[Backend Error] Directory contents:`);

      try {
        const resourcesDir = path.join(process.resourcesPath, 'resources');
        if (fs.existsSync(resourcesDir)) {
          const files = fs.readdirSync(resourcesDir);
          files.forEach(file => {
            writeLog(`[Backend Error]   - ${file}`);
          });
        } else {
          writeLog(`[Backend Error] Resources directory does not exist: ${resourcesDir}`);
        }
      } catch (err) {
        writeLog(`[Backend Error] Failed to read resources directory: ${err.message}`);
      }

      const { dialog } = require('electron');
      dialog.showErrorBox('Backend Error',
        'The backend service could not be started.\n\n' +
        `Tried: ${backendCandidates.join(', ')}\n\n` +
        'The application may not function properly.');
    }
  }
}

app.on('second-instance', () => {
  if (mainWindow && !mainWindow.isDestroyed()) {
    if (mainWindow.isMinimized()) mainWindow.restore();
    mainWindow.show();
    mainWindow.focus();
  }
});

async function handleAppReady({
  runLegacyHostedIdentityCleanupImpl = runLegacyHostedIdentityCleanup,
  settingsStoreImpl = settingsStore,
  startBackendImpl = startBackend,
  createWindowImpl = createWindow,
  createTrayImpl = createTray,
  initDynamicIslandImpl = initDynamicIsland,
  startPeriodicUpdateCheckImpl = startPeriodicUpdateCheck,
} = {}) {
  try {
    const migrationResult = runLegacyHostedIdentityCleanupImpl({
      userDataPath: app.getPath('userData'),
      migrationStore: settingsStoreImpl,
      log: (message) => writeLog(message),
    });
    if (migrationResult.errorCount > 0) {
      writeErrorLog(
        `[Migration] Legacy hosted identity cleanup encountered ${migrationResult.errorCount} error(s)`,
      );
    }
  } catch (error) {
    writeErrorLog(
      `[Migration] Legacy hosted identity cleanup failed: ${error.message}`,
    );
  }

  await startBackendImpl();
  createWindowImpl();
  createTrayImpl();
  initDynamicIslandImpl();
  startPeriodicUpdateCheckImpl();
}

function handleActivate({
  browserWindowImpl = BrowserWindow,
  createWindowImpl = createWindow,
} = {}) {
  if (browserWindowImpl.getAllWindows().length === 0) {
    createWindowImpl();
  }
}

app.whenReady().then(() => handleAppReady());

app.on('window-all-closed', () => {
  if (process.platform !== 'darwin') {
    app.quit();
  }
});

app.on('activate', () => {
  handleActivate();
});

function killBackendProcess() {
  if (!backendProcess) return;
  try {
    if (process.platform === 'win32') {
      execSync(`taskkill /F /T /PID ${backendProcess.pid}`, { stdio: 'ignore' });
    } else {
      process.kill(backendProcess.pid, 'SIGKILL');
    }
    writeLog(`[Backend] Killed backend process (PID ${backendProcess.pid})`);
  } catch (e) {
    writeLog(`[Backend] Kill failed: ${e.message}`);
  }
  backendProcess = null;
}

app.on('before-quit', () => {
  isAppQuitting = true;
  killBackendProcess();
  if (logStream) {
    logStream.end();
  }
  if (updateCheckTimer) {
    clearInterval(updateCheckTimer);
  }
  if (ssoStatusBlinkInterval) {
    clearInterval(ssoStatusBlinkInterval);
  }
});

app.on('will-quit', () => {
  killBackendProcess();
});

process.on('uncaughtException', (error) => {
  writeLog(
    `[${new Date().toISOString()}] [ERROR] Uncaught Exception: ${
      error.message
    }\n${error.stack}`,
  );
});

process.on('unhandledRejection', (reason, promise) => {
  writeLog(
    `[${new Date().toISOString()}] [ERROR] Unhandled Rejection at: ${promise}, reason: ${reason}`,
  );
});

module.exports = {
  createWindow,
  handleActivate,
  handleAppReady,
};
