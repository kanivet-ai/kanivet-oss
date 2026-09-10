# Privacy and data handling

Kanivet's basic cluster navigation runs from the user's desktop using local Kubernetes credentials. This document describes the client in this repository; services operated outside the repository have their own data handling.

## Local storage

| Location | Data |
| --- | --- |
| Electron's OS-specific application data directory | Preferences and Electron logs |
| `~/.kanivet/kanivet.db` and SQLite companion files | Application state, cached resource/event information, and cloud session metadata |
| Other directories under `~/.kanivet` | Search, themes, pricing caches, and cost-data files depending on enabled features |
| Kubeconfig and cloud CLI locations | Existing cluster configuration and credentials used by authentication workflows |

Local state can contain sensitive infrastructure information. The app does not promise encryption of its database or caches. Cache eviction and feature-specific cleanup are not a comprehensive retention policy. Uninstalling the application may leave these directories intact.

To remove local state, quit the application and backend, identify and back up only the data you need, then remove the relevant application data directories. This resets preferences and cached state and may remove user-managed theme or cost files. Manage kubeconfigs and cloud CLI credentials separately; deleting Kanivet data does not revoke cluster credentials.

## Installation heartbeat

Kanivet no longer sends installation heartbeats or generates installation UUIDs. The heartbeat setting and endpoint override have been removed. Older versions may still send heartbeats; upgrade to stop sending.

Existing settings files may retain an unused UUID, heartbeat preference, and cadence from an older version. Upgrading does not delete previously submitted server records. The project owners retain those UUIDs until deletion is requested.

### Request deletion

1. Upgrade to a version without the heartbeat, or disable **Anonymous usage heartbeat** in Settings on older versions, to stop future sending.
2. Find the `telemetryInstallationId` value in `kanivet-settings.json` in Electron's application data directory. Copy only that UUID; do not send the full settings file.
3. Email [nuno.mcvmorais@gmail.com](mailto:nuno.mcvmorais@gmail.com) with the subject `Kanivet heartbeat deletion` and include the UUID. The project owners can delete the matching record manually.

The owners cannot locate a heartbeat record from a name or email address alone. If you have already removed the local UUID, they cannot identify which record belonged to that installation. Uninstalling the application does not delete the stored server record. Sending a deletion request shares your email address and the supplied UUID with the project contact for handling that request.

## External connections

- Kubernetes API servers receive requests using the selected context's credentials. Cluster audit policies and retention are controlled by cluster operators.
- Enabled cloud discovery and authentication workflows contact the relevant cloud providers.
- Metrics and other integrations contact configured services as needed by their features.
- Theme discovery uses Open VSX (`open-vsx.org`); downloading a theme involves its distribution endpoints.
- Pricing features can request public cloud pricing data, including AWS and Azure endpoints.
- Update checks and downloads contact the configured release provider. Official CI builds use GitHub Releases; local packaging defaults may use `releases.kanivet.io`.

This list describes identified service categories, not a packet-level audit of all dependencies.

## Questions and reports

Send private data-handling questions or suspected disclosure to [Nuno Morais](mailto:nuno.mcvmorais@gmail.com). Use [SECURITY.md](../SECURITY.md) for security reports. Redact kubeconfigs, tokens, resource contents, user identifiers, and session-secret query parameters before sharing diagnostics.
