const fs = require('node:fs');
const path = require('node:path');

function releaseConfig(configPath = path.join(__dirname, '..', 'release-please-config.json')) {
  const config = JSON.parse(fs.readFileSync(configPath, 'utf8'));
  const packageConfig = config.packages?.['.'];
  if (!packageConfig || !Array.isArray(packageConfig['changelog-sections'])) {
    throw new Error('release-please-config.json must configure changelog sections for the root package');
  }
  return packageConfig;
}

function validateBody(body) {
  if (typeof body !== 'string') {
    throw new Error('PR body must be text');
  }
  if (/\bBEGIN_COMMIT_OVERRIDE\b/i.test(body)) {
    throw new Error('PR body cannot use BEGIN_COMMIT_OVERRIDE: commit overrides are reserved for maintainer-approved historical repairs, not ordinary open PRs');
  }
  if (/(?:^|\n)Release-As\s*:/im.test(body)) {
    throw new Error('PR body cannot use a Release-As directive: forced versions are reserved for maintainer-approved historical repairs, not ordinary open PRs');
  }
  if (/(?:^|\n)BREAKING(?: |-)CHANGE(?:S)?\s*:/im.test(body)) {
    throw new Error('PR body cannot use a BREAKING CHANGE footer; declare breaking releases with ! in the PR title');
  }
}

function validateTitle(title, config = releaseConfig(), body = '') {
  if (typeof title !== 'string' || title.trim() !== title) {
    throw new Error('PR title must be a non-empty Conventional Commit header without surrounding whitespace');
  }
  validateBody(body);

  // This matches the header shape consumed by release-please 17.3.0's
  // conventional-commits parser: type(scope)!: description.
  const match = /^(?<type>[a-z][a-z0-9-]*)(?:\((?<scope>[^()\r\n]+)\))?(?<breaking>!)?: (?<description>[^\r\n]+)$/.exec(title);
  if (!match) {
    throw new Error('PR title must use Conventional Commits, for example "feat(frontend): add namespace filtering"');
  }

  const section = config['changelog-sections'].find(candidate => candidate.type === match.groups.type);
  if (!section || section.hidden === true || typeof section.section !== 'string' || section.section.length === 0) {
    throw new Error(`PR title type "${match.groups.type}" is not configured to produce a visible release-please changelog entry`);
  }

  // release-please's default strategy makes breaking changes major, feat
  // minor, and every other visible conventional commit patch. Returning this
  // keeps the test and user-facing result tied to the configured behavior.
  return {
    type: match.groups.type,
    bump: match.groups.breaking ? 'major' : match.groups.type === 'feat' ? 'minor' : 'patch',
  };
}

if (require.main === module) {
  try {
    const result = validateTitle(process.env.PR_TITLE, releaseConfig(), process.env.PR_BODY);
    process.stdout.write(`Release-please will include this PR as a ${result.bump} release change (${result.type}).\n`);
  } catch (error) {
    process.stderr.write(`${error.message}\n`);
    process.exitCode = 1;
  }
}

module.exports = { releaseConfig, validateBody, validateTitle };
