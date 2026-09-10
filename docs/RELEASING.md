# Releases and support

## Version policy

Kanivet uses `MAJOR.MINOR.PATCH`: major for breaking changes, minor for compatible features, and patch for compatible fixes. Describe breaking changes and migration instructions in the release notes, including during the current 0.x series. Release candidates use an `-rc.` suffix and are previews.

The [security policy](../SECURITY.md#supported-versions) prioritizes the latest stable release. There is no long-term-support or older-release backport commitment.

## Automated release flow

1. Reviewed changes merge into `main` with Conventional Commits-style PR titles.
2. [release-please.yml](../.github/workflows/release-please.yml) updates the pending version PR and builds release candidates linked from it.
3. A founder or delegated release coordinator reviews the proposed version and changelog, including breaking changes, licensing, and security disclosures.
4. Merging the release-please PR builds the stable artifacts. The release is published after the required platform jobs succeed.

[publish.yml](../.github/workflows/publish.yml) runs application tests, builds the frontend and production Go backend, packages macOS, Windows, and Linux artifacts, and collects update metadata. macOS packaging requires signing/notarization credentials. Windows builds produce a combined x64/ARM64 installer; Linux and macOS use architecture-specific artifacts.

Use [test-release.yml](../.github/workflows/test-release.yml) for a packaging validation run with publication disabled. It still requires the configured build environment and macOS secrets. Consult [SETUP.md](../SETUP.md) for local builds.

## Maintainer checklist

- Ensure independent review and required checks pass. Review notable user-facing changes and migration instructions.
- Smoke-test install, launch, cluster selection, a read-only resource view, logs, and update behavior on supported target platforms with a test cluster.
- Complete [dependency and asset review](DEPENDENCIES.md), retain project and third-party license notices, and inspect packaged outputs. Record SBOM/attribution evidence when available.
- Verify release channel, version, artifact architectures, signing status, and update provider. `configure-release.cjs` sets GitHub Releases for CI builds; local package defaults use a different provider.
- Coordinate public security notes with the reporter and private advisory process.
- Publish the version, user-facing changes, known limitations, and tested platforms in the release notes.

## Failed or withdrawn releases

Investigate failures before retrying publication. The workflow preserves already published release assets; do not silently replace a published binary with different contents. For a defective published release, explain the issue and publish a corrective version. If an artifact must be withdrawn for security, coordinate an advisory and make the withdrawal explicit. Users should not assume automatic downgrade support.

## Credentials and operational setup

Store signing and publishing credentials in repository or organization secrets with limited access. Rotate them after suspected compromise. Review repository permissions, contribution sign-offs, and validation results before publishing a release. Keep credentials out of workflows that execute untrusted pull request code.
