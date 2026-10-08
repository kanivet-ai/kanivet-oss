const test = require('node:test');
const assert = require('node:assert/strict');
const EventEmitter = require('node:events');
const Module = require('node:module');
const path = require('node:path');

const preloadPath = path.join(__dirname, 'preload.js');

// Runs preload.js against a fake electron and returns what it exposes.
function loadPreload() {
  delete require.cache[require.resolve(preloadPath)];
  const ipcRenderer = new EventEmitter();
  const invoked = [];
  ipcRenderer.invoke = async (channel, ...args) => {
    invoked.push([channel, ...args]);
  };
  let electronAPI;
  const originalLoad = Module._load;
  Module._load = function patchedLoad(request, parent, isMain) {
    if (request === 'electron') {
      return {
        ipcRenderer,
        contextBridge: {
          exposeInMainWorld: (_key, api) => {
            electronAPI = api;
          },
        },
      };
    }
    return originalLoad.call(this, request, parent, isMain);
  };
  try {
    require(preloadPath);
  } finally {
    Module._load = originalLoad;
  }
  return { electronAPI, ipcRenderer, invoked };
}

test('tray.onSwitchTab unsubscribes the listener it registered', () => {
  const { electronAPI, ipcRenderer } = loadPreload();
  let switches = 0;

  // Layout re-subscribes whenever its SSO sessions change.
  for (let i = 0; i < 12; i++) {
    const unsubscribe = electronAPI.tray.onSwitchTab(() => {
      switches++;
    });
    unsubscribe();
  }
  electronAPI.tray.onSwitchTab(() => {
    switches++;
  });
  ipcRenderer.emit('tray:switchTab', {}, 'c1');

  assert.equal(ipcRenderer.listenerCount('tray:switchTab'), 1);
  assert.equal(switches, 1);
});

test('backend.getPort asks the main process for the port', async () => {
  const { electronAPI, invoked } = loadPreload();

  await electronAPI.backend.getPort();

  assert.deepEqual(invoked, [['backend:getPort']]);
});
