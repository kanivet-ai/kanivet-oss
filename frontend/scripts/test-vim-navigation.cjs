// Runs real panel components in Electron with synthetic cluster data.
// No backend, kubeconfig or personal application profile is used.
const path = require('node:path');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');

async function runRendererTests() {
  const { app, BrowserWindow } = require('electron');
  app.setPath('userData', process.env.NAVIGATION_TEST_PROFILE);
  await app.whenReady();
  const win = new BrowserWindow({
    show: false,
    width: 1500,
    height: 950,
    webPreferences: { backgroundThrottling: false },
  });
  const evaluate = (source) => win.webContents.executeJavaScript(source);
  const snapshot = () => evaluate('window.navigationTest.snapshot()');
  const waitFor = async (check, message) => {
    const deadline = Date.now() + 10000;
    while (Date.now() < deadline) {
      if (await check()) return;
      await new Promise((resolve) => setTimeout(resolve, 25));
    }
    throw new Error(`${message}: ${JSON.stringify(await snapshot())}`);
  };
  const focus = async (area) =>
    waitFor(async () => {
      const s = await snapshot();
      return (
        s.focus === area &&
        (area === 'tree'
          ? s.treeFocused
          : area === 'list'
            ? s.listsFocused === 1
            : s.detailFocused)
      );
    }, `Expected ${area} focus`);
  const key = async (keyCode) => {
    win.webContents.sendInputEvent({ type: 'keyDown', keyCode });
    win.webContents.sendInputEvent({ type: 'keyUp', keyCode });
    // Allow React's keyboard-handler effects to commit before the next key.
    await new Promise((resolve) => setTimeout(resolve, 50));
  };
  const click = async (selector) => {
    const point = await evaluate(`(() => {
      const rect = document.querySelector(${JSON.stringify(selector)}).getBoundingClientRect();
      return { x: Math.round(rect.x + rect.width / 2), y: Math.round(rect.y + rect.height / 2) };
    })()`);
    win.webContents.sendInputEvent({
      type: 'mouseDown',
      ...point,
      button: 'left',
      clickCount: 1,
    });
    win.webContents.sendInputEvent({
      type: 'mouseUp',
      ...point,
      button: 'left',
      clickCount: 1,
    });
  };
  const reset = async (options = {}) => {
    await evaluate(`window.navigationTest.reset(${JSON.stringify(options)})`);
    await evaluate(
      'new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))',
    );
    await focus('tree');
  };
  try {
    await win.loadURL(process.env.NAVIGATION_TEST_URL);
    await focus('tree');
    await key('l');
    await focus('list');
    assert.equal((await snapshot()).selected, 'pod-a');
    assert.equal(
      (await snapshot()).listLoads,
      0,
      'Returning to the selected list must not reload it',
    );
    await key('h');
    await focus('tree');
    await key('l');
    await focus('list');
    await key('j');
    assert.equal(
      (await snapshot()).selected,
      'pod-b',
      'List navigation must work after L',
    );
    await key('l');
    await focus('detail');
    assert.equal((await snapshot()).detail, 'pod-b');
    assert.equal((await snapshot()).collapsed, false);
    // A row click stops bubbling; the panel must claim focus during capture.
    await click('.tree-node-selected');
    await focus('tree');
    await key('l');
    await focus('list');
    assert.equal((await snapshot()).selected, 'pod-a');
    await key('l');
    await focus('detail');
    await click('.resource-list tr.resource-row td:nth-child(2)');
    await focus('list');
    assert.equal((await snapshot()).selected, 'pod-a');
    await key('j');
    assert.equal((await snapshot()).selected, 'pod-b');
    await key('l');
    await focus('detail');
    console.log(
      'PASS: mouse-selected tree and list rows take focus from details',
    );
    await key('h');
    await focus('list');
    assert.equal((await snapshot()).selected, 'pod-b');
    assert.equal((await snapshot()).collapsed, true, 'H must collapse details');
    await evaluate('window.navigationTest.finishDetails()');
    await waitFor(async () => (await snapshot()).detailLoaded, 'Details should finish loading');
    await focus('list');
    assert.equal((await snapshot()).collapsed, true, 'Late details must not reopen the panel');
    await key('h');
    await focus('tree');
    await key('l');
    await focus('list');
    assert.equal(
      (await snapshot()).selected,
      'pod-a',
      'Returning from the selected sidebar item must select the first visible row',
    );
    console.log(
      'PASS: sidebar L, list H, list L opens selected detail, detail H (including slow details)',
    );

    await evaluate('window.navigationTest.collapseDetails()');
    await key('l');
    await focus('detail');
    assert.equal((await snapshot()).collapsed, false, 'L reopens collapsed details');
    await click('.resource-list tr.resource-row td:nth-child(2)');
    await focus('list');
    const point = await evaluate(
      `(() => { const r = document.querySelector('.detail-view').getBoundingClientRect(); return { x: Math.round(r.x + r.width / 2), y: Math.round(r.y + 100) }; })()`,
    );
    win.webContents.sendInputEvent({
      type: 'mouseDown',
      ...point,
      button: 'left',
      clickCount: 1,
    });
    win.webContents.sendInputEvent({
      type: 'mouseUp',
      ...point,
      button: 'left',
      clickCount: 1,
    });
    await focus('detail');
    await key('h');
    await focus('list');
    assert.equal((await snapshot()).collapsed, true, 'H collapses mouse-focused details');
    console.log(
      'PASS: reopening collapsed details and collapsing mouse-focused details with H',
    );

    await reset({ descending: true });
    await key('l');
    await focus('list');
    assert.equal(
      (await snapshot()).selected,
      'pod-b',
      'Select the first row in displayed sort order',
    );
    console.log('PASS: sidebar L respects the visible sort order');

    await reset({ empty: true });
    await key('l');
    await focus('list');
    await key('l');
    await focus('list');
    await key('h');
    await focus('tree');
    console.log(
      'PASS: empty lists remain reachable and L without a row is a no-op',
    );

    await reset();
    await key('j');
    await key('l');
    await focus('tree');
    assert.equal((await snapshot()).selectedNode, 'services');
    assert.equal((await snapshot()).activeResource, 'services');
    await key('l');
    await focus('list');
    await key('h');
    await focus('tree');
    await key('k');
    await key('l');
    await focus('tree');
    assert.equal((await snapshot()).activeResource, 'pods');
    await key('l');
    await focus('list');
    assert.equal((await snapshot()).selected, 'pod-a');
    console.log('PASS: first L opens a different list in the sidebar; second L enters its first row');

    await reset({ split: true });
    await key('l');
    await focus('list');
    await key('l');
    await focus('detail');
    assert.equal(
      (await snapshot()).detail,
      'pod-a',
      'An inactive split pane must not open its row',
    );
    await key('h');
    await focus('list');
    await evaluate("window.navigationTest.focusPane('other')");
    await focus('list');
    await key('l');
    await focus('detail');
    assert.equal(
      (await snapshot()).detail,
      'pod-b',
      'Changing the focused split pane must transfer its shortcuts',
    );
    await key('h');
    await focus('list');
    console.log('PASS: only the focused split list handles navigation');

    await evaluate(`document.getElementById('tree-search').focus()`);
    await key('h');
    await key('l');
    await focus('list');
    console.log('PASS: typing H/L in search does not navigate panels');
    if (process.env.NAVIGATION_TEST_SCREENSHOT) {
      fs.writeFileSync(
        process.env.NAVIGATION_TEST_SCREENSHOT,
        (await win.webContents.capturePage()).toPNG(),
      );
    }
    app.exit(0);
  } catch (error) {
    console.error(error);
    app.exit(1);
  }
}

async function main() {
  const { createServer } = await import('vite');
  const { spawn } = require('node:child_process');
  const server = await createServer({
    root: path.resolve(__dirname, '..'),
    server: { host: '127.0.0.1', port: 0 },
  });
  await server.listen();
  const url = `${server.resolvedUrls.local[0]}tests/navigation/`;
  const profile = fs.mkdtempSync(path.join(os.tmpdir(), 'navigation-test-'));
  try {
    const env = {
      ...process.env,
      NAVIGATION_TEST_URL: url,
      NAVIGATION_TEST_PROFILE: profile,
    };
    delete env.ELECTRON_RUN_AS_NODE;
    const child = spawn(require('electron'), [__filename], {
      stdio: 'inherit',
      env,
    });
    const code = await new Promise((resolve, reject) => {
      child.on('error', reject);
      child.on('exit', resolve);
    });
    process.exitCode = code ?? 1;
  } finally {
    await server.close();
    fs.rmSync(profile, { recursive: true, force: true });
  }
}

(process.versions.electron ? runRendererTests() : main()).catch((error) => {
  console.error(error);
  process.exit(1);
});
