# Development setup

## Prerequisites

- Git, Node.js 22 (see [frontend/.nvmrc](frontend/.nvmrc)), and npm. Use Node 22.12 or newer for the Vite 7 dependency.
- Go with automatic toolchain selection enabled. [backend/go.mod](backend/go.mod) declares Go 1.24.4 and toolchain `go1.24.11`; the Go command may select a newer toolchain required by dependencies.
- A desktop session for Electron. Platform packaging also needs the platform's build tools; signed macOS releases require Apple credentials.
- A disposable Kubernetes cluster and a working kubeconfig for interactive testing. Unit tests do not require production access.
- GNU Make for the `make` shortcuts. On Windows, use a compatible shell/toolchain or the equivalent commands below; the Makefile contains POSIX commands.

Authentication plugins referenced by your kubeconfig, such as a cloud CLI, must be installed and accessible on `PATH`. See [the quickstart](docs/QUICKSTART.md) for cluster configuration.

## Install dependencies

From the repository root:

```bash
cd backend
go mod download
cd ../frontend
npm ci
cd ..
```

Use the committed lockfile. Electron's installation scripts download its runtime, so `npm ci --ignore-scripts` alone does not prepare a runnable desktop app. The root npm package contains additional lint tooling; install it with `npm ci` at the root only when you need those tools.

## Run locally

Start the Go backend in one terminal:

```bash
cd backend
go run ./cmd/main.go
```

In a second terminal, from the repository root:

```bash
make dev
```

The equivalent frontend command is `cd frontend` followed by `npm run dev`. It starts Vite on port 5173 and Electron in development mode. **It does not start or reload the Go backend.** The development frontend expects the backend on port 53727. If that port is occupied, resolve the conflict before starting development; the standalone backend's fallback port is not automatically wired into the development frontend.

For optional Go hot reload, install a version of [Air](https://github.com/air-verse/air) compatible with your Go toolchain, then run `make dev-backend` instead of `go run`. Configuration is in [backend/.air.toml](backend/.air.toml). Restarting `go run` manually is sufficient without Air.

Use a test kubeconfig and limited credentials. Without `KANIVET_SESSION_SECRET`, the standalone development backend does not enforce session-secret authentication. Keep it on loopback; see [the security model](docs/SECURITY_MODEL.md).

## Test and build

From the root:

```bash
make test
make build
node --test scripts/*.test.cjs
```

`make test` runs `go test ./...` in `backend` and `npm test` in `frontend`. The frontend suite includes Node tests and Vitest. `make build` compiles the backend into `frontend/resources/kanivet-backend` and builds the UI into `frontend/build`.

For focused checks:

```bash
cd backend
go test ./internal/localsecurity/...
cd ../frontend
npm test
npm run typecheck
```

Report failures honestly in your PR; a successful build does not imply every optional check passed. Do not point tests or exploratory commands at a production cluster.

## Packaging

From `frontend`, `npm run dist:mac`, `npm run dist:win`, and `npm run dist:linux` build and package the application. Outputs go to `frontend/dist`. Use the appropriate native platform for reproducible packaging. The macOS configuration requests signing/notarization; it requires credentials and is not a credential-free local smoke test.

The official release workflow configures GitHub Releases as the update source. Local package defaults still refer to `releases.kanivet.io`; review or override the publish configuration for a fork. See [RELEASING.md](docs/RELEASING.md).

## Troubleshooting

| Symptom | Check |
| --- | --- |
| Electron cannot start | Use a graphical desktop session and install dependencies with scripts enabled. |
| UI cannot reach the backend | Start the backend separately; check ports 53727 and 5173 and backend output. |
| Go requests another toolchain | Allow toolchain download or install the requested toolchain; do not lower module requirements. |
| No clusters or authentication fails | Check your kubeconfig files and refresh any expired cloud login. Ensure authentication plugins referenced by the kubeconfig are installed and available on `PATH`. |
| Packaging fails on signing | Use the release guide and supply signing credentials privately. |

Electron writes logs below its OS-specific application data directory. Development backend logs are also available on stdout and under `backend/logs` when launched there. `make logs` references historical fixed log paths; prefer process output and the actual Electron application data logs when those files do not exist.
