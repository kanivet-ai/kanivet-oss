# Security model

## Intended deployment

Kanivet runs on an individual user's desktop. The trusted boundary includes that OS account, the Electron main process, bundled backend, and credentials available to local tools. It is not a multi-user service or an authorization gateway for untrusted clients.

## Local interfaces

The Go backend defaults to `127.0.0.1:53727`. If the default port is busy, it can select another loopback port. Setting `PORT` to an explicit address can change the interface; do not expose the backend to a network or reverse proxy.

Packaged Electron generates a random session secret and passes it to the backend in `KANIVET_SESSION_SECRET`. API clients send it using `X-Session-Secret` or the streaming endpoint's supported transport. The middleware also accepts a query parameter, so request URLs may contain a secret: redact them from reports and logs.

Without a configured secret, the middleware permits requests. This supports the separate development processes but makes the development backend inappropriate for network exposure. CORS and loopback binding are not substitutes for authentication or protection against other software running as your user.

The Electron renderer has Node integration disabled and context isolation enabled. The preload/IPC API still exposes privileged operations, so changes to IPC, navigation, URL handling, or external content need explicit security review. These observations describe implementation, not an independent security audit.

## Credentials and cluster actions

The backend reads local kubeconfigs and uses Kubernetes client libraries and authentication plugins. Kubeconfigs can refer to executable authentication helpers; only import trusted configurations. Cloud discovery can use local cloud credentials and sessions. Kanivet inherits their effective permissions.

Kubernetes RBAC authorizes cluster operations. Log access can reveal application secrets. Terminal/exec can run code in containers or local sessions. Port-forward can make a remote service accessible locally. Editing, scaling, Helm operations, Argo operations, and deletion can modify cluster state. Review the context and namespace before each action and use least-privilege identities.

## Local data

SQLite state and caches can include cluster resource metadata, event data, search data, and cloud session metadata. Do not assume they are encrypted by Kanivet. Protect the OS account, home directory, backups, and disk. Removing the app does not necessarily remove its data or cloud CLI credentials. See [PRIVACY.md](PRIVACY.md).

## Distribution and dependencies

Official macOS CI packaging requests code signing and notarization. Other platform outputs and local builds should not be assumed to have identical signing guarantees. Updates trust the configured release provider and the release credentials. The current repository does not establish a complete, independently verified supply-chain attestation system.

Review bundled dependencies, update feeds, downloaded themes, archive extraction, and subprocess execution when changing these areas. [DEPENDENCIES.md](DEPENDENCIES.md) describes licensing review; vulnerability scanning is a separate check.

## Review priorities

The initial threat review should cover cross-origin and IPC access, session-secret scope and leakage, local file permissions, hostile kubeconfig helpers, terminal/exec boundaries, unsafe resource actions, malicious theme archives, and compromised update credentials. Track findings through [SECURITY.md](../SECURITY.md), keeping exploitable details private until coordinated disclosure.
