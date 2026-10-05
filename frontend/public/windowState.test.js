const test = require('node:test');
const assert = require('node:assert/strict');
const EventEmitter = require('node:events');
const {
  readWindowState,
  writeWindowState,
  resolveWindowOptions,
  trackWindowState,
} = require('./windowState');

const defaults = { width: 1440, height: 900 };
const display = { workArea: { x: 0, y: 0, width: 1920, height: 1080 } };
const resolve = (saved, displays = [display]) =>
  resolveWindowOptions({ saved, displays, defaults, minWidth: 900, minHeight: 560 });

function memoryFs(initial = {}) {
  const files = new Map(Object.entries(initial));
  return {
    files,
    readFileSync: (file) => {
      if (!files.has(file)) throw new Error('ENOENT');
      return files.get(file);
    },
    writeFileSync: (file, data) => files.set(file, data),
    renameSync: (from, to) => {
      files.set(to, files.get(from));
      files.delete(from);
    },
    mkdirSync: () => {},
  };
}

test('opens with the defaults when nothing was saved', () => {
  assert.deepEqual(resolve(null), { options: defaults, maximize: false, fullScreen: false });
});

test('restores size and position on a connected display', () => {
  const result = resolve({ x: 100, y: 50, width: 1200, height: 800, isMaximized: true, isFullScreen: false });
  assert.deepEqual(result.options, { x: 100, y: 50, width: 1200, height: 800 });
  assert.equal(result.maximize, true);
});

test('drops a position that is on a display no longer connected', () => {
  const result = resolve({ x: 4000, y: 100, width: 1200, height: 800, isMaximized: false, isFullScreen: false });
  assert.deepEqual(result.options, { width: 1200, height: 800 });
});

test('never opens smaller than the minimum size', () => {
  const result = resolve({ x: 0, y: 0, width: 100, height: 100, isMaximized: false, isFullScreen: false });
  assert.equal(result.options.width, 900);
  assert.equal(result.options.height, 560);
});

test('ignores a corrupt state file', () => {
  const fsImpl = memoryFs({ '/s.json': '{not json' });
  assert.equal(readWindowState('/s.json', fsImpl), null);
  assert.equal(readWindowState('/missing.json', fsImpl), null);
});

test('writes state atomically and reads it back', () => {
  const fsImpl = memoryFs();
  const state = { x: 1, y: 2, width: 1000, height: 700, isMaximized: false, isFullScreen: true };
  assert.equal(writeWindowState('/s.json', state, fsImpl), true);
  assert.deepEqual(readWindowState('/s.json', fsImpl), state);
  assert.equal(fsImpl.files.has('/s.json.tmp'), false);
});

test('tracking saves the normal bounds, debounced, and at once on close', () => {
  const fsImpl = memoryFs();
  const win = new EventEmitter();
  win.isDestroyed = () => false;
  win.getNormalBounds = () => ({ x: 10, y: 20, width: 1300, height: 850 });
  win.isMaximized = () => true;
  win.isFullScreen = () => false;
  const timers = [];
  trackWindowState(win, {
    file: '/s.json',
    fsImpl,
    setTimeoutImpl: (fn) => (timers.push(fn), timers.length),
    clearTimeoutImpl: () => {},
  });

  win.emit('resize');
  assert.equal(fsImpl.files.has('/s.json'), false);
  timers[0]();
  assert.deepEqual(readWindowState('/s.json', fsImpl), {
    x: 10, y: 20, width: 1300, height: 850, isMaximized: true, isFullScreen: false,
  });

  fsImpl.files.delete('/s.json');
  win.emit('close');
  assert.equal(fsImpl.files.has('/s.json'), true);
});
