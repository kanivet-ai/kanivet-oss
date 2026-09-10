# Governance

Kanivet uses founder-led governance. The founding group shares final authority over technical direction, project priorities, releases, maintainer appointments, and project policy. The founders welcome contributions and explain significant decisions in public project discussions.

## Founding group

The five developers listed as founders in [MAINTAINERS.md](MAINTAINERS.md) lead the project together. Each founder has equal standing. No individual founder can override a decision of the founding group.

Founders seek consensus on significant decisions: each participating founder must support the proposal or agree to let it proceed. The group gives all founders an opportunity to review a proposal; silence alone does not establish agreement. If founders cannot reach agreement, they keep the existing policy or behavior while they discuss alternatives. The group can agree to delegate a specific decision to a founder or maintainer and record the scope of that delegation.

## Roles

- **Founders** set direction, resolve escalated disputes, appoint maintainers, and approve governance changes. Founders also perform maintainer work.
- **Maintainers** review and merge contributions, triage issues, and coordinate releases within the areas the founders delegate to them. A maintainer appointment does not confer founder status.
- **Contributors** submit code, documentation, tests, designs, and feedback through the [contribution guide](CONTRIBUTING.md). Anyone can contribute.

The founders record maintainer appointments and delegated responsibilities in [MAINTAINERS.md](MAINTAINERS.md). Project participation does not require payment or a commercial relationship.

## Decisions and reviews

Contributors and maintainers use issues and pull requests to propose changes. Routine changes can proceed through a founder or a maintainer responsible for the affected area. Seek review from someone other than the author and run the applicable checks before merging. Explain a rejection or requested change in the PR.

Discuss substantial architecture changes, breaking behavior, project priorities, and unusual dependency licenses in a public issue before implementation. Contributors can explain their needs and propose alternatives; the founders make the final decision and record their reasoning. The group sets discussion time according to the scope and urgency of the change.

The founders decide changes to governance, licensing policy, project asset transfers, and maintainer appointments by consensus. Licensing and asset decisions also require authorization from the relevant rights holders. Project authority does not grant rights over someone else's work or assets.

Use pull requests for changes to the default branch. A founder or delegated maintainer may act to restore service or contain a security incident, then document the action and seek review. Keep sensitive incident details private until disclosure is safe.

## Maintainer membership

A contributor may ask to become a maintainer through a public issue. A founder or maintainer may also nominate someone with their consent. The founders consider the nominee's contributions, reviews, knowledge of the project, and conduct, then decide the appointment by consensus.

The founders define the maintainer's responsibilities, update the roster, and grant the access needed for that work. A maintainer may step down through a roster PR. The founders may adjust or end a maintainer's responsibilities after discussing participation or concerns with them. Explain the change in the project record while respecting confidential conduct matters.

## Founder succession

A founder may step down and name a proposed successor. The remaining founders decide any successor appointment by consensus and update the roster. Founders who leave retain credit for their work.

If a founder becomes unavailable, the remaining founders may continue project decisions after making reasonable attempts to contact them and recording who participated. If no founder remains available, the listed maintainers may agree by consensus on successor project leadership and document the transition. A leadership transition does not transfer copyright or other assets without the required authorization.

## Releases and security

Founders retain authority over release policy and may delegate release coordination to maintainers. Release coordinators follow [the release guide](docs/RELEASING.md), seek independent review, and record validation results.

Report vulnerabilities through [SECURITY.md](SECURITY.md). Founders and maintainers may take urgent containment steps within their access, such as rotating credentials or suspending a compromised account. They must record the action and seek review from an uninvolved founder or maintainer.

## Conduct and disputes

Founders and maintainers follow the [code of conduct](CODE_OF_CONDUCT.md). Anyone involved in a conduct complaint must recuse themselves from handling it. Founder status does not exempt someone from the conduct policy.

Raise technical disagreements in the relevant issue or PR. A maintainer can help clarify the options and escalate the decision to the founders. Report personal misconduct through the private conduct process.

## Public project record

Use [GitHub issues](https://github.com/kanivet-ai/kanivet-oss/issues) and pull requests to record proposals, agreed priorities, significant decisions, and their reasoning. Publish decisions from project meetings so contributors can participate without attending live.
