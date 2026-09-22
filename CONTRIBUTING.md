# Contributing to Kanivet

Bug reports, documentation, design feedback, testing, and code are welcome. Participation follows our [code of conduct](CODE_OF_CONDUCT.md). See [governance](GOVERNANCE.md) for decision-making and [maintainers](MAINTAINERS.md) for project stewardship.

## Getting started

1. Search [existing issues](https://github.com/kanivet-ai/kanivet-oss/issues). Open an issue to discuss substantial changes before implementation.
2. Follow [SETUP.md](SETUP.md) to install dependencies and start both application processes. Use a test cluster with limited permissions.
3. Create a focused branch in your fork and follow the surrounding code style.
4. Add or update tests for behavior changes. Run `make test` and `make build` for code changes; for documentation changes, check links and commands against the repository.
5. Submit a PR explaining the problem, changes, and validation. Include relevant issue links and screenshots for UI changes. Tell reviewers which checks you could not run.

Use [ARCHITECTURE.md](ARCHITECTURE.md) to find the relevant component. Do not attach credentials, unredacted kubeconfigs, customer data, or access tokens to issues or tests. Report suspected vulnerabilities through [SECURITY.md](SECURITY.md).

## Commit sign-off and licensing

Contributions must comply with the [Developer Certificate of Origin 1.1](https://developercertificate.org/). Read it before signing off. Add a sign-off to each contribution commit:

```bash
git commit -s
```

The resulting `Signed-off-by: Your Name <your-email>` trailer certifies your right to submit the work. It is different from a cryptographic commit signature. Your name and email become part of the public Git history. Do not sign for someone else. For an unsigned commit on your own unpublished branch, use `git commit --amend --signoff`; coordinate before rewriting any shared history.

Submit code under Apache-2.0 and documentation prose under CC-BY-4.0, as scoped in [LICENSING.md](LICENSING.md). Preserve third-party notices. The project does not require a separate CLA under this policy. DCO sign-off does not replace permission from a rights holder when permission is needed.

For dependencies and copied assets, identify the upstream source, exact version, license, and notices in your PR. See [the dependency review process](docs/DEPENDENCIES.md). Do not copy code with unknown provenance.

## Pull request conventions

Use a Conventional Commits-style PR title: `type(scope): short description`. The scope is optional. Squash merging uses this title as the commit message, so it is the release-please input; individual WIP commits do not all need Conventional Commit messages.

| Type | Intended change |
| --- | --- |
| `feat` | A new feature; minor release |
| `fix` | A bug fix; patch release |
| `perf` | A performance improvement |
| `docs`, `refactor`, `test`, `build`, `ci`, `chore` | Documentation, maintenance, or supporting changes |

Use `!` before the colon for a breaking change, such as `feat(backend)!: change connection configuration`, and describe the migration. See [release policy](docs/RELEASING.md) for versioning.

The PR title check accepts only the types configured for visible release-please changelog entries. In the current release-please configuration, `feat` produces a minor release, `type!` produces a major release, and the other listed types produce a patch release. This release-bump decision is deliberately limited to the checked title and release-please configuration: do not add `BEGIN_COMMIT_OVERRIDE`, `Release-As:`, or `BREAKING CHANGE:` directives to an ordinary PR body. Those mechanisms are for maintainer-approved historical repairs only. Edit the title (not every WIP commit) if the check fails.

Repository administrators must configure these controls; this repository does not claim they are already enabled:

- Allow only squash merges. Set the squash commit title to **Pull request title** (`PR_TITLE`), and do not modify the final squash title during merge; it is the release-please input validated on the PR.
- Protect `main` with review and required status checks. Require the CI jobs named `release-eligibility`, `frontend`, `backend`, and `release-tooling` (with GitHub's workflow prefix as displayed). The release-please PR receives its checks from its trusted workflow dispatch.

Keep PRs focused and seek review from someone other than the author. A founder or a maintainer responsible for the affected area can merge routine changes; the founding group has final authority on escalated decisions under [governance](GOVERNANCE.md#decisions-and-reviews). Address feedback in the PR. Reviewers check DCO sign-offs before merging contributions.
