const { contextBridge, ipcRenderer } = require('electron');

contextBridge.exposeInMainWorld('electronAPI', {
  writeLog: (message) => ipcRenderer.invoke('write-log', message),
  island: {
    notify: (opts) => ipcRenderer.invoke('island:notify', opts),
  },
  telemetry: {
    getEnabled: () => ipcRenderer.invoke('telemetry:getEnabled'),
    setEnabled: (enabled) => ipcRenderer.invoke('telemetry:setEnabled', enabled),
  },
  backend: {
    onPortChanged: (callback) => {
      const listener = (_, port) => callback(port);
      ipcRenderer.on('backend:port-changed', listener);
      return () => ipcRenderer.removeListener('backend:port-changed', listener);
    },
  },
  onClearCache: (callback) => {
    ipcRenderer.on('clear-cache', callback);
    return () => ipcRenderer.removeListener('clear-cache', callback);
  },
  security: {
    getSessionSecret: () => ipcRenderer.invoke('security:getSessionSecret'),
  },

  updater: {
    getAppVersion: () => ipcRenderer.invoke('updater:getAppVersion'),
    checkForUpdates: () => ipcRenderer.invoke('updater:checkForUpdates'),
    downloadUpdate: () => ipcRenderer.invoke('updater:downloadUpdate'),
    installUpdate: () => ipcRenderer.invoke('updater:installUpdate'),
    getDownloadProgress: () => ipcRenderer.invoke('updater:getDownloadProgress'),
    onUpdateAvailable: (callback) => ipcRenderer.on('updater:update-available', (_, info) => callback(info)),
    onUpdateDownloaded: (callback) => ipcRenderer.on('updater:update-downloaded', (_, info) => callback(info)),
    onDownloadProgress: (callback) => ipcRenderer.on('updater:download-progress', (_, progress) => callback(progress)),
    onError: (callback) => ipcRenderer.on('updater:error', (_, error) => callback(error)),
    onMenuCheckForUpdates: (callback) => {
      ipcRenderer.on('menu:check-for-updates', callback);
      return () => ipcRenderer.removeListener('menu:check-for-updates', callback);
    },
    removeAllListeners: () => {
      ipcRenderer.removeAllListeners('updater:update-available');
      ipcRenderer.removeAllListeners('updater:update-downloaded');
      ipcRenderer.removeAllListeners('updater:download-progress');
      ipcRenderer.removeAllListeners('updater:error');
      ipcRenderer.removeAllListeners('menu:check-for-updates');
    },
  },
  tray: {
    updateTabs: (tabs) => ipcRenderer.invoke('tray:updateTabs', tabs),
    updateSSOSessions: (sessions) => ipcRenderer.invoke('tray:updateSSOSessions', sessions),
    onSwitchTab: (callback) => {
      ipcRenderer.on('tray:switchTab', (_, tabId) => callback(tabId));
      return () => ipcRenderer.removeListener('tray:switchTab', callback);
    },
    onOpenSettings: (callback) => {
      ipcRenderer.on('tray:openSettings', callback);
      return () => ipcRenderer.removeListener('tray:openSettings', callback);
    },

    onRefreshSSO: (callback) => {
      ipcRenderer.on('tray:refreshSSO', (_, startUrl) => callback(startUrl));
      return () => ipcRenderer.removeListener('tray:refreshSSO', callback);
    },
    onAddSSO: (callback) => {
      ipcRenderer.on('tray:addSSO', callback);
      return () => ipcRenderer.removeListener('tray:addSSO', callback);
    },
    updateSSOAccounts: (accounts, active) => ipcRenderer.invoke('tray:updateSSOAccounts', accounts, active),
    onActivateAccount: (callback) => {
      ipcRenderer.on('tray:activateAccount', (_, account) => callback(account));
      return () => ipcRenderer.removeListener('tray:activateAccount', callback);
    },
  },
});
