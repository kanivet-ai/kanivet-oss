# Kanivet

Kanivet is a standalone Electron application for navigating, troubleshooting, and operating Kubernetes clusters from your local environment.

## Download

- Latest build: [GitHub Releases](https://github.com/kanivet-ai/kanivet/releases/latest)

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

Desktop auto-updates continue to use `electron-updater` and `https://releases.kanivet.io`.
