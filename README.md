# Kanivet

Kanivet is a standalone Electron application for navigating, troubleshooting, and operating Kubernetes clusters from your local environment.

Browse resources and live updates, inspect logs and incident timelines, search across cluster data, and work with terminals, Helm releases, and Argo applications using your existing Kubernetes credentials. Available workflows depend on your permissions and cluster components.

## Download

- Latest build: [GitHub Releases](https://github.com/kanivet-ai/kanivet-oss/releases/latest)
- [Get started with Kanivet](docs/QUICKSTART.md)

## Documentation and community

- [Development setup](SETUP.md) and [architecture](ARCHITECTURE.md)
- [Contributing](CONTRIBUTING.md), [maintainers](MAINTAINERS.md), and [governance](GOVERNANCE.md)
- [Adopters](ADOPTERS.md) and [ecosystem fit](docs/PROJECT.md)
- [Security reporting](SECURITY.md), [security model](docs/SECURITY_MODEL.md), and [privacy](docs/PRIVACY.md)
- [Release process](docs/RELEASING.md) and [licensing](LICENSING.md)

Kanivet adopts the [CNCF Community Code of Conduct](https://github.com/cncf/foundation/blob/main/code-of-conduct.md) with the project reporting process in [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md). Public questions and proposals belong in [GitHub issues](https://github.com/kanivet-ai/kanivet-oss/issues).

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

The project owners operate the heartbeat service and retain installation UUIDs indefinitely unless deletion is requested. To request manual deletion, disable the heartbeat and email your installation UUID to [nuno.mcvmorais@gmail.com](mailto:nuno.mcvmorais@gmail.com). See [Privacy and data handling](docs/PRIVACY.md#request-deletion) for instructions, local storage, and other external connections.

## Build from source

See [SETUP.md](SETUP.md) for prerequisites, dependency installation, and packaging details.

Common commands:

- `make dev` runs Electron and the frontend development server; start the backend separately
- `make dev-backend` runs backend hot reload when Air is installed
- `make build` builds frontend and backend
- `make test` runs the project test suite

After installing dependencies, start the backend in one terminal:

```bash
cd backend
go run ./cmd/main.go
```

Start Electron and the frontend in another terminal:

```bash
cd frontend
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

Read [CONTRIBUTING.md](CONTRIBUTING.md), including the required [DCO sign-off](CONTRIBUTING.md#commit-sign-off-and-licensing), before submitting changes.

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

## License

Original project code is licensed under [Apache-2.0](LICENSE) and documentation prose under [CC-BY-4.0](LICENSES/CC-BY-4.0.txt). Third-party material retains its own licenses and notices as described in [LICENSING.md](LICENSING.md).

## Contributors

- [Fábio Araújo (@fabioaraujopt)](https://github.com/fabioaraujopt)
- [João Soares (@jasoares)](https://github.com/jasoares)
- [Jorge Soares (@jorgensoares)](https://github.com/jorgensoares)
- [Miguel de Oliveira Guerreiro (@mdguerreiro)](https://github.com/mdguerreiro)
- [Nuno Morais (@nm-morais)](https://github.com/nm-morais)
