# Architecture

Kanivet is a desktop application with a React interface in Electron and a local Go backend. The backend connects to Kubernetes using the user's kubeconfigs. The basic resource workflow does not require deploying a Kanivet service into the cluster.

## Request and update flow

The UI asks the backend for resources from a selected context. The backend builds Kubernetes clients from local credentials and uses shared watching and caching infrastructure for resource updates. WebSocket and other streaming endpoints deliver changes without requiring every view to poll independently. Search and event workflows can persist data locally.

When extending a resource workflow, review the existing [service architecture](backend/internal/core/services/ARCHITECTURE.md), [service guide](backend/internal/core/services/README.md), [WebSocket guide](backend/internal/websocket/README.md), and [search guide](backend/internal/search/README.md). Verify implementation details before treating older component notes as current behavior.

## Process and trust boundaries

Packaged Electron starts a bundled backend and supplies a random per-run session secret. The UI obtains the secret through preload IPC. The backend's session middleware checks it on protected requests. The default listener is loopback port 53727, with an ephemeral fallback if the default port is busy. Explicit `PORT` values can override the bind address.

In development, Electron and the backend are started separately; the session check is bypassed when no backend secret is configured. See [SETUP.md](SETUP.md) and [the security model](docs/SECURITY_MODEL.md) for the implications.

Persistent state includes Electron application data and `~/.kanivet/kanivet.db`, plus search, theme, and pricing caches under `~/.kanivet`. Credentials, cached resource data, and local terminals share the local user's trust boundary. See [privacy](docs/PRIVACY.md) for external services and retention limits.

## Releases

The frontend is built with Vite and packaged using electron-builder alongside a Go executable. GitHub Actions builds platform artifacts and release-please manages stable version proposals. [RELEASING.md](docs/RELEASING.md) explains the release candidate and stable paths.
