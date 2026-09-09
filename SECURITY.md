# Security policy

## Reporting a vulnerability

Email [Nuno Morais](mailto:nuno.mcvmorais@gmail.com) at **nuno.mcvmorais@gmail.com** with the subject `Kanivet security report`. This is the project's private security contact.

GitHub private vulnerability reporting is not currently enabled. Use email rather than a public issue for technical details. Do not submit exploit details, credentials, kubeconfigs, or affected users' data in a public issue.

In the private report, include the affected Kanivet version and OS, impact, prerequisites, minimal reproduction, and any proposed mitigation. Redact secrets and use a disposable test environment. Do not test against systems you do not have permission to assess.

## Supported versions

| Version | Security maintenance policy |
| --- | --- |
| Latest stable release | Fixes are prioritized here |
| Older releases | Upgrade to the latest stable release; no backport commitment |
| Release candidates and development builds | Reports welcome; no production support commitment |

This is a community support policy, not a service-level agreement. Maintainers aim to acknowledge reports within five business days and provide an initial assessment within ten business days. If there is no acknowledgment, resend the email. You may open a public issue requesting contact without publishing technical details.

## Response and disclosure

Maintainers reproduce and assess the issue privately, agree on scope and mitigation with the reporter, and coordinate a fix and disclosure date. Give progress updates at least every fourteen days while a confirmed issue is being addressed. Discuss delays or active exploitation with the reporter; do not promise a fixed release date before assessing the issue.

Publish an advisory with affected versions, impact, fixed version, and mitigation when a fix or coordinated disclosure is ready. Request a CVE when appropriate and credit the reporter only with consent. Route reports involving a dependency to its maintainers privately as needed.

## Security model

Kanivet runs with the local user's access to kubeconfigs, cloud authentication, and Kubernetes APIs. Terminal, exec, port-forward, and resource editing operations can affect live systems. It is intended as a local desktop tool. Read [the security model](docs/SECURITY_MODEL.md), including how credentials and cluster permissions apply, before connecting sensitive clusters.
