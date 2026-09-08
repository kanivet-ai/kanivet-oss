// Called by actions/github-script; only main is built, never PR-supplied code.
module.exports = async function releasePlan({ github, context, core }) {
  const repo = context.repo;
  if (process.env.RELEASE_CREATED === 'true') {
    core.setOutput('channel', 'stable');
    core.setOutput('version', process.env.RELEASE_VERSION);
    core.setOutput('sha', process.env.RELEASE_SHA);
    return;
  }

  // Recover a draft left behind by a failed build when the workflow is rerun.
  const releases = await github.paginate(github.rest.repos.listReleases, { ...repo, per_page: 100 });
  const draft = releases.find(r => r.draft && /^v\d+\.\d+\.\d+$/.test(r.tag_name) && r.target_commitish === context.sha);
  if (draft) {
    core.setOutput('channel', 'stable');
    core.setOutput('version', draft.tag_name.slice(1));
    core.setOutput('sha', context.sha);
    return;
  }

  const pulls = await github.paginate(github.rest.pulls.list, { ...repo, state: 'open', base: 'main', per_page: 100 });
  const pr = pulls.find(p => p.head.repo.full_name === `${repo.owner}/${repo.repo}` &&
    p.head.ref === 'release-please--branches--main' && p.labels.some(l => l.name === 'autorelease: pending'));
  if (!pr) {
    core.notice('No pending release PR: there are no releasable changes yet.');
    return;
  }
  const { data } = await github.rest.repos.getContent({ ...repo, path: 'version.txt', ref: pr.head.sha });
  const nextVersion = Buffer.from(data.content, 'base64').toString('utf8').trim();
  if (!/^\d+\.\d+\.\d+$/.test(nextVersion)) throw new Error('Invalid version in release PR');
  core.setOutput('channel', 'rc');
  core.setOutput('version', `${nextVersion}-rc.${process.env.GITHUB_RUN_NUMBER}.${process.env.GITHUB_RUN_ATTEMPT}`);
  core.setOutput('sha', context.sha);
  core.setOutput('pr', String(pr.number));
};
