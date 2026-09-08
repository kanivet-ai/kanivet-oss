# Kanivet

Kanivet is a standalone Electron application for navigating, troubleshooting, and operating Kubernetes clusters from your local environment.

## Download

- Latest build: [GitHub Releases](https://github.com/kanivet-ai/kanivet-oss/releases/latest)

## Anonymous usage heartbeat

Kanivet sends an anonymous installation heartbeat from the Electron main process by default.

- Endpoint: `https://heartbeat.kanivet.io/v1/heartbeat`
- Payload: exactly `{"installation_id":"<uuid-v4>"}`
- Identifier: a persistent `crypto.randomUUID()` generated on first run
- Cadence: at most once per UTC calendar day
- Timing: sent after a short random startup delay, with a short timeout, without blocking startup, and without aggressive retries
- Disable: Settings > Theme > `Anonymous usage heartbeat`
- Opt-out behavior: turning it off stops future heartbeats and preserves the local UUID and existing cadence state
- Upgrade behavior: an explicit opt-out from the previous diagnostics setting is honored and migrated to disabled
- While disabled: no heartbeat is sent and no deletion request is made
- Fork override: set `KANIVET_HEARTBEAT_URL` to another compatible endpoint, or set it to an empty value to disable sending in a forked build

The request body does not include app version, platform, architecture, IP address, user agent, account identity, cluster names, or operational telemetry.

Traffic to `heartbeat.kanivet.io` is delivered through Cloudflare, which transiently processes source IP addresses at the network edge to serve the request. The heartbeat payload itself contains only the installation UUID above, and Kanivet does not intentionally store IP addresses, user agents, or additional operational data as part of the heartbeat record.

## Build from source

See `SETUP.md` for local setup and packaging details.

Common commands:

- `make dev` runs Electron with frontend and backend hot reload
- `make build` builds frontend and backend
- `make test` runs the project test suite
- `make logs` tails frontend and backend logs

Manual development:

```bash
cd backend
air
```

```bash
cd frontend
npm install --ignore-scripts
npm run dev
```

## Release notes

Kanivet follows Semantic Versioning (`MAJOR.MINOR.PATCH`):

- **MAJOR** for breaking changes.
- **MINOR** for backward-compatible new features.
- **PATCH** for backward-compatible bug fixes.

Merges to `main` build GitHub release candidates linked from the pending release-please PR. Merging that PR publishes the stable release after all builds succeed. Both channels cover macOS, Windows, and Linux on x64 and ARM64.

CI builds use `electron-updater` with GitHub Releases.

## Contributing

Contributions are welcome, including bug reports, feature ideas, documentation improvements, and code changes.

- Search [existing issues](https://github.com/kanivet-ai/kanivet-oss/issues) before opening a new one. For bugs, include steps to reproduce, expected and actual behavior, and your operating system and Kanivet version.
- For substantial changes, open an issue first to discuss the approach.
- Fork the repository, create a branch for your change, and follow the [build from source](#build-from-source) instructions to run the app locally.
- Keep changes focused, follow the existing code style, and add or update tests when changing behavior. Run `make test` and `make build` before submitting code changes.
- Open a pull request describing the problem, your solution, and how you tested it. Link any related issues and include screenshots for UI changes.

### Pull request conventions

Use a Conventional Commits-style PR title: `type(scope): short description`. The scope is optional; use it to identify the affected area, such as `frontend` or `backend`.

- `feat`: a new feature, corresponding to a MINOR release.
- `fix`: a bug fix, corresponding to a PATCH release.
- `perf`: a performance improvement.
- `docs`, `refactor`, `test`, `build`, `ci`, or `chore`: documentation, code restructuring, tests, build changes, CI changes, or maintenance.
- Add `!` before the colon for breaking changes, such as `feat(backend)!: change connection configuration`. Explain the breaking change and migration steps in the PR description; breaking changes correspond to a MAJOR release.

Examples: `feat(frontend): add namespace filtering`, `fix(backend): handle expired credentials`, and `docs: clarify local setup`.

Keep each PR focused on one change. Include a summary, related issues, validation results, and screenshots when relevant. Submit changes through a PR rather than pushing directly to `main` or `master`.

## Authors

- [Fábio Araújo (@fabioaraujopt)](https://github.com/fabioaraujopt)
- [João Soares (@jasoares)](https://github.com/jasoares)
- [João Pereira (@joaoafonsopereira)](https://github.com/joaoafonsopereira)
- [Jorge Soares (@jorgensoares)](https://github.com/jorgensoares)
- [Miguel de Oliveira Guerreiro (@mdguerreiro)](https://github.com/mdguerreiro)
- [Nuno Morais (@nm-morais)](https://github.com/nm-morais)
