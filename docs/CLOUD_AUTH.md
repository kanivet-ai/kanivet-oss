# Cloud sign-in (AWS, Google Cloud, Azure)

Kanivet reuses the credentials your terminal already has. It never keeps a
private copy of a cloud login, never rewrites your `default` AWS profile, and
never opens a browser on its own. Sign-ins started from Kanivet are visible to
the CLIs, and CLI sign-ins are visible to Kanivet.

## Where credentials come from

| Provider | Kanivet reads | Kanivet writes |
| --- | --- | --- |
| AWS IAM Identity Center | `~/.aws/sso/cache` (the AWS CLI token cache), `~/.aws/config` | Tokens into the same cache; `kanivet-sso-*` profiles only when no profile of yours already matches |
| AWS named profiles (Leapp, aws-vault, granted, static keys) | `~/.aws/config`, `~/.aws/credentials` | Nothing |
| Google Cloud | The active `gcloud` account | Nothing (`gcloud auth login` writes its own state) |
| Azure | The Azure CLI login (`az account show`) | Nothing (`az login` writes its own state) |

Imported clusters use the same exec plugins `kubectl` uses: `aws eks get-token`,
`gke-gcloud-auth-plugin`, and `kubelogin --login azurecli`. Whatever refreshes
credentials for your terminal refreshes them for Kanivet.

## Cloud accounts menu

The account icon in the toolbar lists every identity Kanivet can see:

- **AWS IAM Identity Center** portals: the ones you added in Kanivet, every
  `[sso-session …]` in `~/.aws/config`, and any portal that has a cached token.
  Each row shows *Signed in · renews automatically*, *Signed in · renewing*,
  *Expired · sign in required*, or *Not signed in*. A countdown such as
  *Signed in · 40m left* appears only for a token that has no refresh token;
  AWS access tokens last about an hour and the portal does not report when the
  session itself ends. Expand a row to see the accounts assigned to you.
- **Google Cloud**: the active gcloud account and project.
- **Azure**: the Azure CLI user and subscription.

Sign in from the row's button. Kanivet shows the device code, opens your
browser, and waits; you can reopen the browser tab, copy the code, or cancel.
Signing out of an AWS portal removes its cached tokens for the AWS CLI too.
Removing a portal only forgets Kanivet's entry; tokens and profiles the CLI
created stay.

The menu tray (macOS menu bar) mirrors the same list and lets you start a
sign-in without opening the window.

## When a cluster cannot authenticate

The cluster pane explains how that context authenticates and offers the
sign-in that fixes it:

- **AWS SSO profile**: a *Sign in to AWS SSO* button for the right portal. One
  sign-in reconnects every cluster on that portal.
- **Profile managed by another tool** (Leapp, aws-vault, `credential_process`,
  static keys): Kanivet names the profile and tells you to refresh that tool's
  session, then *Retry connection*.
- **GKE**: *Sign in with gcloud*. **AKS**: *Sign in with Azure CLI*.
- **Missing CLI or plugin**: an install hint for `aws`, `gcloud`,
  `gke-gcloud-auth-plugin`, `az` or `kubelogin`.

Each pane also shows the equivalent terminal command. Kanivet notices terminal
sign-ins within a few seconds and reconnects failing clusters automatically.

## Silent renewal

AWS access tokens are renewed in the background with the refresh token the
portal issued, exactly as `aws sso login` would do in place, so an 8-hour
session keeps working without prompts for as long as your administrator allows
refresh. Only when the refresh token itself is rejected does a session show as
*Expired*, and even then Kanivet waits for you to click *Sign in*.

## Compatibility notes

- Kanivet stores tokens under its own `kanivet-sso-<hash>` session name and,
  when a user-defined `[sso-session]` points at the same portal with plain
  `sso:account:access` scopes, under that session too. Sessions that request
  extra scopes (for example Amazon Q) are left untouched.
- Earlier Kanivet versions could replace the `[default]` profile with SSO role
  credentials and keep a backup in `kanivet-backup-original-default`. On first
  start this version restores the backup and stops touching `[default]`.
- Azure clusters imported by Kanivet use `kubelogin --login azurecli`. If a
  kubeconfig from elsewhere uses `devicecode`, run
  `kubelogin convert-kubeconfig -l azurecli` on it, or re-import the cluster.
