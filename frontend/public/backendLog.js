const fs = require('fs');
const path = require('path');

const LOG_FILE = 'backend.log';
// The file being written and the one before it: twice this at most on disk.
const MAX_BYTES = 16 * 1024 * 1024;
// A line is written once it is complete. Output without a line break is not
// held back beyond this.
const MAX_PENDING_CHARS = 64 * 1024;

// The backend prints every request with its query string, and the window's
// websocket carries the session secret in it.
const redact = (text) => text.replace(/(session_secret=)[^&\s"']+/gi, '$1<redacted>');

/**
 * Keeps what the backend prints in <dir>/backend.log. Without it, what the
 * backend did while nobody was looking (a night of expired credentials, a
 * watch that stopped) is gone by the time someone asks.
 *
 * The log is bounded: on reaching maxBytes the file becomes backend.log.1,
 * replacing the one before, so it never holds more than twice maxBytes. A log
 * that cannot be written is given up on; it must not cost the app anything.
 */
function createBackendLog(dir, { maxBytes = MAX_BYTES, fsImpl = fs } = {}) {
  const file = path.join(dir, LOG_FILE);
  let fd = null;
  let size = 0;
  let pending = '';
  let failed = false;

  const append = (text) => {
    if (failed || !text) return;
    try {
      if (fd === null) {
        fsImpl.mkdirSync(dir, { recursive: true });
        fd = fsImpl.openSync(file, 'a');
        size = fsImpl.fstatSync(fd).size;
      }
      const data = Buffer.from(redact(text));
      if (size > 0 && size + data.length > maxBytes) {
        fsImpl.closeSync(fd);
        fd = null;
        fsImpl.renameSync(file, `${file}.1`);
        fd = fsImpl.openSync(file, 'a');
        size = 0;
      }
      fsImpl.writeSync(fd, data);
      size += data.length;
    } catch {
      failed = true;
    }
  };

  return {
    file,
    /** Adds output as it arrives; chunks need not end on a line. */
    write(chunk) {
      pending += chunk.toString();
      const end = pending.lastIndexOf('\n');
      if (end !== -1) {
        append(pending.slice(0, end + 1));
        pending = pending.slice(end + 1);
      }
      if (pending.length > MAX_PENDING_CHARS) {
        append(`${pending}\n`);
        pending = '';
      }
    },
    /** Writes what is left of an unfinished line and closes the file. */
    close() {
      if (pending) append(`${pending}\n`);
      pending = '';
      if (fd === null) return;
      try {
        fsImpl.closeSync(fd);
      } catch {}
      fd = null;
    },
  };
}

module.exports = { LOG_FILE, createBackendLog };
