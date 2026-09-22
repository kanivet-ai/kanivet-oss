const { test } = require('node:test');
const assert = require('node:assert/strict');
const { PENDING_RELEASE_BRANCH, dispatchPendingReleaseCi } = require('./dispatch-ci.cjs');

test('dispatches the read-only CI workflow only on the fixed pending release branch', async () => {
  let request;
  await dispatchPendingReleaseCi({
    context: { repo: { owner: 'kanivet-ai', repo: 'kanivet-oss' } },
    github: { rest: { actions: { createWorkflowDispatch: async args => { request = args; } } } },
    pr: '7',
  });
  assert.deepEqual(request, { owner: 'kanivet-ai', repo: 'kanivet-oss', workflow_id: 'ci.yml', ref: PENDING_RELEASE_BRANCH });
});

test('refuses a missing plan PR or a non-release branch', async () => {
  const args = { context: { repo: {} }, github: { rest: { actions: { createWorkflowDispatch() {} } } } };
  await assert.rejects(dispatchPendingReleaseCi({ ...args, pr: '' }), /pending release PR number/);
  await assert.rejects(dispatchPendingReleaseCi({ ...args, pr: '7', ref: 'feature' }), /CI dispatch ref/);
});
