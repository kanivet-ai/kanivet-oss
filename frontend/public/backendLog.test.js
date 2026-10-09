const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');

const { createBackendLog } = require('./backendLog');

function tempDir(t) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'kanivet-backend-log-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  return dir;
}

test('keeps what the backend printed, across restarts of the app', (t) => {
  const dir = path.join(tempDir(t), 'logs');

  const first = createBackendLog(dir);
  first.write('k8s watcher: list failed: Unauthorized\n');
  first.close();
  const second = createBackendLog(dir);
  second.write('k8s watcher: sent 108 items\n');
  second.close();

  assert.equal(
    fs.readFileSync(second.file, 'utf8'),
    'k8s watcher: list failed: Unauthorized\nk8s watcher: sent 108 items\n',
  );
});

test('never holds more than the file being written and the one before it', (t) => {
  const dir = tempDir(t);
  const log = createBackendLog(dir, { maxBytes: 100 });
  const line = (n) => `${String(n).padStart(3, '0')} ${'x'.repeat(35)}\n`; // 40 bytes

  for (let n = 1; n <= 7; n++) log.write(line(n));
  log.close();

  assert.deepEqual(fs.readdirSync(dir).sort(), ['backend.log', 'backend.log.1']);
  // The newest lines survive; the oldest file was replaced.
  assert.equal(fs.readFileSync(log.file, 'utf8'), line(7));
  assert.equal(fs.readFileSync(`${log.file}.1`, 'utf8'), line(5) + line(6));
  for (const name of fs.readdirSync(dir)) {
    assert.ok(fs.statSync(path.join(dir, name)).size <= 100, `${name} is over the limit`);
  }
});

test('does not put the session secret on disk, even split across chunks', (t) => {
  const log = createBackendLog(tempDir(t));

  log.write('[GIN] 200 | GET "/api/v1/ws?session_secret=0123456789ab');
  log.write('cdef&x=1"\n[GIN] 200 | GET "/api/v1/clusters"\n');
  log.close();

  assert.equal(
    fs.readFileSync(log.file, 'utf8'),
    '[GIN] 200 | GET "/api/v1/ws?session_secret=<redacted>&x=1"\n[GIN] 200 | GET "/api/v1/clusters"\n',
  );
});

test('writes an unfinished last line when closed', (t) => {
  const log = createBackendLog(tempDir(t));

  log.write('panic: runtime error');
  log.close();

  assert.equal(fs.readFileSync(log.file, 'utf8'), 'panic: runtime error\n');
});

test('gives up quietly when the log cannot be written', (t) => {
  const dir = tempDir(t);
  // A file where the log directory should be.
  const blocked = path.join(dir, 'logs');
  fs.writeFileSync(blocked, '');
  const log = createBackendLog(blocked);

  assert.doesNotThrow(() => {
    log.write('line one\n');
    log.write('line two\n');
    log.close();
  });
  assert.equal(fs.readFileSync(blocked, 'utf8'), '');
});
