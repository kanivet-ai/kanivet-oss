const PENDING_RELEASE_BRANCH = 'release-please--branches--main';

// Called only from the trusted main-branch release workflow after release-plan
// selected a current same-repository pending release PR.
async function dispatchPendingReleaseCi({ github, context, pr, ref = PENDING_RELEASE_BRANCH }) {
  if (!/^\d+$/.test(String(pr)) || Number(pr) < 1) {
    throw new Error('A current pending release PR number is required before dispatching CI');
  }
  if (ref !== PENDING_RELEASE_BRANCH) {
    throw new Error(`CI dispatch ref must be ${PENDING_RELEASE_BRANCH}`);
  }
  await github.rest.actions.createWorkflowDispatch({
    ...context.repo,
    workflow_id: 'ci.yml',
    ref,
  });
}

module.exports = { PENDING_RELEASE_BRANCH, dispatchPendingReleaseCi };
