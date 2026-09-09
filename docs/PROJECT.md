# Project scope and ecosystem fit

## Purpose

Kanivet is a reusable desktop tool for navigating, troubleshooting, and operating Kubernetes clusters. Its React/Electron interface and Go backend bring resource browsing, watches, logs, terminals, search, incident timelines, Helm releases, and Argo application views into one local workflow. Users connect through existing kubeconfigs and cluster permissions.

Developers and operators use Kanivet to investigate workloads across contexts and namespaces. The project brings resource state, logs, events, and operational actions into a local desktop workflow.

## Cloud native integration

Kubernetes APIs and client libraries are the core integration. Helm support works with release data, and Argo views expose application-related workflows when the relevant custom resources are present. Cloud discovery helps locate clusters through provider authentication. Metrics and cost features are optional integrations, not prerequisites for basic browsing.

Kanivet is an application rather than a new standard or reference architecture. Its local API is an implementation interface; this repository does not propose a separately governed interoperability specification.

## Related projects

| Project | Area of overlap and comparison |
| --- | --- |
| [Headlamp](https://headlamp.dev/) | Kubernetes UI with desktop/web deployment and extensibility. Both projects provide graphical Kubernetes resource navigation. |
| [K9s](https://k9scli.io/) | Terminal-based resource navigation and operations. Kanivet offers a graphical desktop workflow, while both address Kubernetes investigation. |
| [Lens](https://lenshq.io/) | Kubernetes desktop IDE with cluster navigation and troubleshooting workflows that overlap with Kanivet. |

These descriptions summarize the linked project sites as of 9 September 2026. Consult those sites for current features and deployment options.

## Contribution scope and service separation

[kanivet-ai/kanivet-oss](https://github.com/kanivet-ai/kanivet-oss) contains the standalone application. The project owners maintain the heartbeat service outside this repository. The client also references release infrastructure and third-party services; see [privacy and data handling](PRIVACY.md) for their role.

The founders decide any contribution of project assets under [governance](../GOVERNANCE.md). An open-source code license does not itself transfer domains, service accounts, or trademarks.
