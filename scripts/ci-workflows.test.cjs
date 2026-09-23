const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

// Windows runners check files out with CRLF line endings; normalize so the
// newline-anchored patterns below match on every platform.
const readWorkflow = (name) => fs.readFileSync(path.join(__dirname, '../.github/workflows', name), 'utf8').replace(/\r\n/g, '\n');

const workflow = readWorkflow('ci.yml');
const releaseWorkflow = readWorkflow('release-please.yml');

test('CI validates every PR, including title edits, and can be dispatched or reused by release publication', () => {
  assert.match(workflow, /pull_request:\n    types: \[opened, synchronize, reopened, edited\]/);
  assert.doesNotMatch(workflow, /pull_request:\n    branches:/);
  assert.match(workflow, /workflow_dispatch:/);
  assert.match(workflow, /workflow_call:/);
});

test('CI is read-only, Ubuntu-hosted, and only cancels superseded PR runs', () => {
  assert.match(workflow, /permissions:\n  contents: read/);
  assert.match(workflow, /runs-on: ubuntu-24\.04/g);
  assert.match(workflow, /cancel-in-progress: \$\{\{ github\.event_name == 'pull_request' \}\}/);
  assert.doesNotMatch(workflow, /pull_request_target|secrets:|contents: write|publish/i);
  assert.equal((workflow.match(/persist-credentials: false/g) || []).length, 4);
});

test('CI runs the requested application and release-tooling checks', () => {
  for (const command of ['npm ci', 'npm run typecheck', 'npm test', 'npm run build', 'go vet ./...', 'go test -race ./...', 'go build -tags production', 'node --test scripts/*.test.cjs', 'python3 scripts/collect-release.test.py']) {
    assert.ok(workflow.includes(command), `missing ${command}`);
  }
  assert.match(workflow, /go-version-file: backend\/go\.mod/);
});

test('title validation safely passes PR metadata and skips a reusable main push', () => {
  assert.match(workflow, /if: github\.event_name == 'pull_request' \|\| github\.event_name == 'workflow_dispatch'/);
  assert.doesNotMatch(workflow, /event_name != 'workflow_call'/);
  assert.match(workflow, /PR_TITLE: \$\{\{ github\.event\.pull_request\.title \|\| steps\.release-pr\.outputs\.title \}\}/);
  assert.match(workflow, /PR_BODY: \$\{\{ github\.event\.pull_request\.body \|\| steps\.release-pr\.outputs\.body \|\| '' \}\}/);
});

test('manual dispatch is restricted to the current fixed same-repository release-please PR', () => {
  assert.match(workflow, /const branch = 'release-please--branches--main';/);
  assert.match(workflow, /workflow_dispatch is reserved/);
  assert.match(workflow, /No current same-repository pending release-please PR was found/);
});

test('main release publication waits for reusable CI and only prepare can dispatch CI', () => {
  assert.match(releaseWorkflow, /validation:\n    uses: \.\/\.github\/workflows\/ci\.yml/);
  assert.match(releaseWorkflow, /prepare:\n    needs: validation/);
  assert.match(releaseWorkflow, /actions: write/);
  assert.match(releaseWorkflow, /steps\.plan\.outputs\.channel == 'rc' && steps\.plan\.outputs\.pr != ''/);
  assert.match(releaseWorkflow, /dispatchPendingReleaseCi\(\{ github, context, pr: process\.env\.RELEASE_PR \}\)/);
});
