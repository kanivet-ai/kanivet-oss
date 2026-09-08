const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawnSync } = require('node:child_process');

for (const [channel, version] of [['stable', '0.2.0'], ['rc', '0.2.0-rc.42.1']]) {
  test(`${channel} build embeds its version, GitHub feed, and signing settings`, t => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'kanivet-release-'));
    t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
    fs.mkdirSync(path.join(dir, 'frontend'));
    fs.copyFileSync(path.join(__dirname, '../frontend/package.json'), path.join(dir, 'frontend/package.json'));
    const result = spawnSync(process.execPath, [path.join(__dirname, 'configure-release.cjs')], {
      cwd: dir, encoding: 'utf8',
      env: { ...process.env, RELEASE_VERSION: version, RELEASE_CHANNEL: channel, GITHUB_REPOSITORY: 'kanivet-ai/kanivet-oss' },
    });
    assert.equal(result.status, 0, result.stderr);
    const config = JSON.parse(fs.readFileSync(path.join(dir, 'frontend/release-builder.json')));
    assert.equal(JSON.parse(fs.readFileSync(path.join(dir, 'frontend/package.json'))).version, version);
    assert.deepEqual(config.publish, { provider: 'github', owner: 'kanivet-ai', repo: 'kanivet-oss', channel: channel === 'rc' ? 'rc' : 'latest', releaseType: channel === 'rc' ? 'prerelease' : 'release' });
    assert.equal(config.generateUpdatesFilesForAllChannels, false);
    assert.equal(config.mac.notarize, true);
    assert.match(config.nsis.artifactName, /x64-arm64/);
  });
}

test('an RC version cannot be published on the stable channel', () => {
  const result = spawnSync(process.execPath, [path.join(__dirname, 'configure-release.cjs')], {
    encoding: 'utf8', env: { ...process.env, RELEASE_VERSION: '0.2.0-rc.42.1', RELEASE_CHANNEL: 'stable' },
  });
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /Version does not match channel/);
});
