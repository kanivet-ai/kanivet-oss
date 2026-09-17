<p align="center">
  <img src="docs/assets/kanivet-logo.svg" alt="Kanivet logo" width="144" height="144">
</p>

# Kanivet — Open-source Kubernetes desktop GUI

Kanivet is an open-source Kubernetes desktop application for macOS, Windows, and Linux. Browse Kubernetes resources, troubleshoot pods, stream container logs, trace Crossplane resources, and manage Helm releases and Argo CD applications from a graphical interface using your existing kubeconfig credentials.

Built for developers, platform engineers, and SREs, Kanivet brings Kubernetes cluster navigation and troubleshooting into one local workspace. Switch between clusters and namespaces, inspect live resource updates, and investigate workload issues without deploying an additional service into your cluster for basic resource browsing.

[Download Kanivet](https://github.com/kanivet-ai/kanivet-oss/releases/latest) · [Getting started](docs/QUICKSTART.md) · [Development setup](SETUP.md) · [Report a bug](https://github.com/kanivet-ai/kanivet-oss/issues)

## Kubernetes management and troubleshooting features

- **Browse Kubernetes resources:** navigate pods, deployments, services, configuration, storage, RBAC, and custom resources with live updates.
- **Work across clusters and namespaces:** find and open clusters from local kubeconfigs, then switch contexts within the desktop app.
- **Troubleshoot pods and workloads:** inspect resource status, container logs, Kubernetes events, and incident timelines.
- **Search cluster resources:** find resources across your cluster data to move from an issue to the relevant object.
- **Edit Kubernetes YAML:** inspect, create, and update resource definitions in the built-in editor.
- **Use terminals and port forwarding:** open pod shells and forward pod or service ports to your local machine.

### Crossplane, GitOps, and cloud integrations

- **Trace Crossplane resources:** follow relationships between claims, composite resources, and composed resources in a visual tree. Inspect Ready and Synced conditions to investigate infrastructure provisioning issues.
- **Manage Helm releases:** inspect release details and values, and upgrade releases from the app.
- **Work with Argo CD:** inspect application topology and diffs, trigger syncs, and roll back to earlier revisions when Argo CD is installed.
- **Discover cloud Kubernetes clusters:** find Amazon EKS, Azure AKS, and Google GKE clusters through cloud discovery and provider authentication.
- **Connect to virtual clusters:** discover and connect to vCluster instances from their host cluster.
- **Inspect Kubernetes metrics and costs:** view CPU and memory usage alongside resource requests and limits, and explore estimated costs, resource efficiency, and idle costs in the FinOps dashboard.

Available actions depend on your Kubernetes permissions and the components installed in each cluster. Metrics and cost views use optional integrations; they are not required for basic resource browsing. See [project scope and ecosystem fit](docs/PROJECT.md) for more detail.

## Download and install Kanivet

Get the installer for your operating system from [GitHub Releases](https://github.com/kanivet-ai/kanivet-oss/releases/latest):

- **macOS:** choose the DMG for Apple Silicon (ARM64) or Intel (x64), then drag Kanivet into Applications.
- **Windows:** download and run the EXE installer.
- **Linux:** download the AppImage for your architecture, make it executable, and launch it.

### Connect your first Kubernetes cluster

1. Make sure you have a working kubeconfig, such as `~/.kube/config`, and network access to your cluster.
2. Launch Kanivet, open the cluster selector, and use **Quick Find** to select a cluster.
3. Choose a namespace and open a pod or workload to inspect its status, logs, and events.

If your kubeconfig uses a cloud authentication plugin, install the required CLI and make sure your login is current. For kubeconfig refresh, cloud discovery, and connection troubleshooting, follow the [Kubernetes desktop quickstart](docs/QUICKSTART.md).

## Frequently asked questions

### What is Kanivet used for?

Kanivet is a Kubernetes GUI for everyday cluster navigation, resource management, and troubleshooting. Use it to investigate pod failures, read container logs, inspect events, edit YAML, trace Crossplane infrastructure, and work with Helm releases or Argo CD applications.

### Does Kanivet require an in-cluster installation?

Basic resource browsing connects to Kubernetes APIs from your desktop using your existing kubeconfig and cluster permissions. It does not require deploying a Kanivet service into the cluster. Read the [architecture](ARCHITECTURE.md) and [security model](docs/SECURITY_MODEL.md) for connection and trust boundaries.

### Is Kanivet free and open source?

Yes. Kanivet's original application code is licensed under Apache-2.0, and its documentation is licensed under CC-BY-4.0. You can download the desktop app or build it from source. See [licensing](LICENSING.md) and [privacy and data handling](docs/PRIVACY.md) for details.

## Documentation and community

- [Development setup](SETUP.md) and [architecture](ARCHITECTURE.md)
- [Contributing](CONTRIBUTING.md), [maintainers](MAINTAINERS.md), and [governance](GOVERNANCE.md)
- [Adopters](ADOPTERS.md) and [ecosystem fit](docs/PROJECT.md)
- [Security reporting](SECURITY.md), [security model](docs/SECURITY_MODEL.md), and [privacy](docs/PRIVACY.md)
- [Release process](docs/RELEASING.md) and [licensing](LICENSING.md)

Kanivet adopts the [CNCF Community Code of Conduct](https://github.com/cncf/foundation/blob/main/code-of-conduct.md) with the project reporting process in [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md). Public questions and proposals belong in [GitHub issues](https://github.com/kanivet-ai/kanivet-oss/issues).

## Build from source

See [SETUP.md](SETUP.md) for prerequisites, dependency installation, and packaging details.

Fastest path: run `devbox shell` to get pinned Go, Node.js, golangci-lint, and gh versions (see [devbox.json](devbox.json)), then use the commands below.

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
