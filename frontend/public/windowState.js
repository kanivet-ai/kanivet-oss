const fs = require('fs');
const path = require('path');

const STATE_FILE = 'window-state.json';
const SAVE_DELAY_MS = 500;
// How much of the window has to sit on a display for its saved position to be
// kept; less than this and it could be out of reach (a display that is gone).
const MIN_VISIBLE_WIDTH = 120;
const MIN_VISIBLE_HEIGHT = 60;

const isFiniteNumber = (value) => typeof value === 'number' && Number.isFinite(value);

function readWindowState(file, fsImpl = fs) {
  try {
    const raw = JSON.parse(fsImpl.readFileSync(file, 'utf8'));
    if (!raw || typeof raw !== 'object') return null;
    if (!isFiniteNumber(raw.width) || !isFiniteNumber(raw.height)) return null;
    return {
      x: isFiniteNumber(raw.x) ? raw.x : undefined,
      y: isFiniteNumber(raw.y) ? raw.y : undefined,
      width: raw.width,
      height: raw.height,
      isMaximized: raw.isMaximized === true,
      isFullScreen: raw.isFullScreen === true,
    };
  } catch {
    return null;
  }
}

function writeWindowState(file, state, fsImpl = fs) {
  // Written beside the target and renamed over it, so a quit in the middle of
  // the write cannot leave a half-written file.
  const tmp = `${file}.tmp`;
  try {
    fsImpl.mkdirSync(path.dirname(file), { recursive: true });
    fsImpl.writeFileSync(tmp, JSON.stringify(state));
    fsImpl.renameSync(tmp, file);
    return true;
  } catch {
    return false;
  }
}

function isVisibleOnSomeDisplay(bounds, displays) {
  return displays.some((display) => {
    const area = display.workArea || display.bounds;
    if (!area) return false;
    const width = Math.min(bounds.x + bounds.width, area.x + area.width) - Math.max(bounds.x, area.x);
    const height = Math.min(bounds.y + bounds.height, area.y + area.height) - Math.max(bounds.y, area.y);
    return width >= MIN_VISIBLE_WIDTH && height >= MIN_VISIBLE_HEIGHT;
  });
}

/**
 * The BrowserWindow options to open with: the saved size, and the saved
 * position when it is still on a connected display (otherwise the window is
 * centred by the OS), never smaller than the window's minimum.
 */
function resolveWindowOptions({ saved, displays, defaults, minWidth, minHeight }) {
  if (!saved) return { options: { ...defaults }, maximize: false, fullScreen: false };
  const options = {
    width: Math.max(minWidth, saved.width),
    height: Math.max(minHeight, saved.height),
  };
  if (saved.x !== undefined && saved.y !== undefined && isVisibleOnSomeDisplay({ x: saved.x, y: saved.y, ...options }, displays)) {
    options.x = saved.x;
    options.y = saved.y;
  }
  return { options, maximize: saved.isMaximized, fullScreen: saved.isFullScreen };
}

/**
 * Keeps the file up to date as the window moves, resizes, maximizes or closes.
 * The size and position saved are the window's normal ones, so leaving a
 * maximized or full-screen window restores to them.
 */
function trackWindowState(win, { file, fsImpl = fs, setTimeoutImpl = setTimeout, clearTimeoutImpl = clearTimeout }) {
  let timer = null;
  const save = () => {
    timer = null;
    if (win.isDestroyed()) return;
    const bounds = typeof win.getNormalBounds === 'function' ? win.getNormalBounds() : win.getBounds();
    writeWindowState(
      file,
      {
        x: bounds.x,
        y: bounds.y,
        width: bounds.width,
        height: bounds.height,
        isMaximized: win.isMaximized(),
        isFullScreen: win.isFullScreen(),
      },
      fsImpl,
    );
  };
  const schedule = () => {
    if (timer) clearTimeoutImpl(timer);
    timer = setTimeoutImpl(save, SAVE_DELAY_MS);
  };
  const saveNow = () => {
    if (timer) clearTimeoutImpl(timer);
    save();
  };
  ['resize', 'move', 'maximize', 'unmaximize', 'enter-full-screen', 'leave-full-screen'].forEach((event) => win.on(event, schedule));
  win.on('close', saveNow);
  return saveNow;
}

module.exports = {
  STATE_FILE,
  readWindowState,
  writeWindowState,
  isVisibleOnSomeDisplay,
  resolveWindowOptions,
  trackWindowState,
};
