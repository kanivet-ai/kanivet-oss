# Development setup

## Prerequisites

- Git and [devbox](https://www.jetify.com/docs/devbox). Devbox provides pinned Go, Node.js, golangci-lint, and gh versions from [devbox.json](devbox.json) — no manual Go/Node/golangci-lint/gh install needed.
- A desktop session for Electron. Platform packaging also needs the platform's build tools; signed macOS releases require Apple credentials.
- A disposable Kubernetes cluster and a working kubeconfig for interactive testing. Unit tests do not require production access.

Authentication plugins referenced by your kubeconfig, such as a cloud CLI, must be installed and accessible on `PATH`. See [the quickstart](docs/QUICKSTART.md) for cluster configuration.

## Enter the devbox shell

From the repository root:

```bash
devbox shell
```

Run this once per terminal; it puts the pinned toolchain on `PATH` for the rest of the session. Everything below assumes you're inside it. (To run a single command without entering the shell, use `devbox run <script>` instead — see below.)

## Install dependencies

```bash
devbox run install
```

Or individually: `devbox run install:backend` (`go mod download`) and `devbox run install:frontend` (`npm install`). Use the committed lockfile. Electron's installation scripts download its runtime, so skipping them does not prepare a runnable desktop app.

## Run locally

Start the Go backend in one terminal:

```bash
devbox run run:backend
```

In a second terminal:

```bash
devbox run dev
```

This starts Vite on port 5173 and Electron in development mode. **It does not start or reload the Go backend.** The development frontend expects the backend on port 53727. If that port is occupied, resolve the conflict before starting development; the standalone backend's fallback port is not automatically wired into the development frontend.

For Go hot reload, install a version of [Air](https://github.com/air-verse/air) compatible with your Go toolchain (Air is not pinned by devbox), then run `devbox run dev:backend` instead of `run:backend`. Configuration is in [backend/.air.toml](backend/.air.toml). Restarting `run:backend` manually is sufficient without Air.

Use a test kubeconfig and limited credentials. Without `KANIVET_SESSION_SECRET`, the standalone development backend does not enforce session-secret authentication. Keep it on loopback; see [the security model](docs/SECURITY_MODEL.md).

## Test and build

```bash
devbox run test
devbox run build
node --test scripts/*.test.cjs
```

`devbox run test` runs `go test ./...` in `backend` and `npm test` in `frontend`. The frontend suite includes Node tests and Vitest. `devbox run build` compiles the backend into `frontend/resources/kanivet-backend` and builds the UI into `frontend/build`. Each also has `:backend` / `:frontend` variants, e.g. `devbox run test:backend`.

For focused checks, from inside the devbox shell:

```bash
cd backend
go test ./internal/localsecurity/...
cd ../frontend
npm test
npm run typecheck
```

Report failures honestly in your PR; a successful build does not imply every optional check passed. Do not point tests or exploratory commands at a production cluster.

## Lint

```bash
devbox run lint
```

Or `lint:backend` / `lint:frontend`. Auto-fix with `devbox run lint:fix`.

## Packaging

```bash
devbox run dist:mac
devbox run dist:win
devbox run dist:linux
```

Outputs go to `frontend/dist`. Use the appropriate native platform for reproducible packaging. The macOS configuration requests signing/notarization; it requires credentials and is not a credential-free local smoke test.

The official release workflow configures GitHub Releases as the update source. Local package defaults still refer to `releases.kanivet.io`; review or override the publish configuration for a fork. See [RELEASING.md](docs/RELEASING.md).

## Cleaning up

```bash
devbox run clean
```

## Troubleshooting

| Symptom | Check |
| --- | --- |
| Electron cannot start | Use a graphical desktop session and install dependencies with scripts enabled. |
| UI cannot reach the backend | Start the backend separately; check ports 53727 and 5173 and backend output. |
| Go requests another toolchain | Allow toolchain download; do not lower module requirements. `backend/go.mod` declares the required Go version. |
| No clusters or authentication fails | Check your kubeconfig files and refresh any expired cloud login. Ensure authentication plugins referenced by the kubeconfig are installed and available on `PATH`. |
| Packaging fails on signing | Use the release guide and supply signing credentials privately. |

Electron writes logs below its OS-specific application data directory. Development backend logs are also available on stdout and under `backend/logs` when launched there.
