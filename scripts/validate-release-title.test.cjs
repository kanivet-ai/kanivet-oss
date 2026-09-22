const { test } = require('node:test');
const assert = require('node:assert/strict');
const { validateTitle } = require('./validate-release-title.cjs');

test('rejects the unparseable historical PR 25 squash title', () => {
  assert.throws(
    () => validateTitle('Add GitHub star prompt on launch (#25)'),
    /must use Conventional Commits/
  );
});

test('accepts a feature title and reports its release-please bump', () => {
  assert.deepEqual(validateTitle('feat(frontend): add GitHub star prompt on launch'), { type: 'feat', bump: 'minor' });
});

test('accepts every configured visible changelog type', () => {
  for (const [type, bump] of [
    ['fix', 'patch'], ['perf', 'patch'], ['docs', 'patch'], ['refactor', 'patch'],
    ['test', 'patch'], ['build', 'patch'], ['ci', 'patch'], ['chore', 'patch'], ['revert', 'patch'],
  ]) {
    assert.deepEqual(validateTitle(`${type}: describe the change`), { type, bump });
  }
});

test('rejects a conventional-looking type that cannot create this changelog', () => {
  assert.throws(() => validateTitle('style: adjust whitespace'), /not configured to produce a visible/);
});

test('recognizes breaking changes as major releases', () => {
  assert.deepEqual(validateTitle('fix(backend)!: remove legacy endpoint'), { type: 'fix', bump: 'major' });
});

test('rejects a PR-body commit override even when the title is valid', () => {
  assert.throws(
    () => validateTitle('fix: preserve title validation', undefined, 'BEGIN_COMMIT_OVERRIDE\ninvalid historical input\nEND_COMMIT_OVERRIDE'),
    /commit overrides are reserved for maintainer-approved historical repairs/
  );
});

test('rejects Release-As directives in ordinary PR bodies', () => {
  assert.throws(
    () => validateTitle('fix: preserve title validation', undefined, 'Release-As: 99.0.0'),
    /forced versions are reserved for maintainer-approved historical repairs/
  );
});

test('rejects body breaking-change footers so the bump is controlled by the title', () => {
  assert.throws(
    () => validateTitle('fix: preserve title validation', undefined, 'BREAKING CHANGE: silently changes the bump'),
    /declare breaking releases with ! in the PR title/
  );
});
