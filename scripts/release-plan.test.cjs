const { test } = require('node:test');
const assert = require('node:assert/strict');
const plan = require('./release-plan.cjs');

async function run({ created = false, releases = [], pulls = [], version = '0.2.0' } = {}) {
  process.env.RELEASE_CREATED = String(created);
  process.env.RELEASE_VERSION = '0.2.0';
  process.env.RELEASE_SHA = 'release-sha';
  process.env.GITHUB_RUN_NUMBER = '42';
  process.env.GITHUB_RUN_ATTEMPT = '2';
  const outputs = {};
  await plan({
    context: { repo: { owner: 'kanivet-ai', repo: 'kanivet-oss' }, sha: 'main-sha' },
    core: { setOutput: (k, v) => { outputs[k] = v; }, notice() {} },
    github: {
      rest: { repos: { listReleases: 'releases', getContent: async args => {
        assert.equal(args.ref, 'pr-sha');
        return { data: { content: Buffer.from(version).toString('base64') } };
      } }, pulls: { list: 'pulls' } },
      paginate: async method => method === 'releases' ? releases : pulls,
    },
  });
  return outputs;
}

const pr = { number: 7, head: { sha: 'pr-sha', ref: 'release-please--branches--main', repo: { full_name: 'kanivet-ai/kanivet-oss' } }, labels: [{ name: 'autorelease: pending' }] };

test('a merged release PR selects the stable release commit', async () => {
  assert.deepEqual(await run({ created: true }), { channel: 'stable', version: '0.2.0', sha: 'release-sha' });
});
test('RC uses the proposed version but builds the main commit', async () => {
  assert.deepEqual(await run({ pulls: [pr] }), { channel: 'rc', version: '0.2.0-rc.42.2', sha: 'main-sha', pr: '7' });
});
test('a failed stable draft can be retried', async () => {
  assert.deepEqual(await run({ releases: [{ draft: true, tag_name: 'v0.2.0', target_commitish: 'main-sha' }] }), { channel: 'stable', version: '0.2.0', sha: 'main-sha' });
});
test('unrelated drafts do not trigger stable publication', async () => {
  assert.deepEqual(await run({ releases: [{ draft: true, tag_name: 'v0.2.0', target_commitish: 'other-sha' }] }), {});
});
test('no releasable changes means no build', async () => {
  assert.deepEqual(await run(), {});
});
test('a similarly labelled user PR is not used as a release PR', async () => {
  assert.deepEqual(await run({ pulls: [{ ...pr, head: { ...pr.head, ref: 'feature' } }] }), {});
});
test('invalid PR versions are rejected', async () => {
  await assert.rejects(run({ pulls: [pr], version: '0.2.0\ninjected' }), /Invalid version/);
});
