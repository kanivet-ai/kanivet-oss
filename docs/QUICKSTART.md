# Get started with Kanivet

## Download and open the app

Download Kanivet for your computer from the [latest release](https://github.com/kanivet-ai/kanivet-oss/releases/latest).

- **macOS:** download the DMG for Apple Silicon (ARM64) or Intel (x64), open it, and drag Kanivet into Applications.
- **Windows:** download and run the EXE installer.
- **Linux:** download the AppImage for your architecture, make it executable, and open it.

Launch Kanivet after installation.

## Open a cluster

Kanivet finds clusters from your local kubeconfig files, including `~/.kube/config`. If you do not have a kubeconfig, get one from your cluster administrator or cloud provider.

1. Open the cluster selector in Kanivet.
2. Use **Quick Find** to find your cluster and select it.
3. Choose a namespace and open a workload or pod.

If you added a kubeconfig while Kanivet was running, use **Refresh kubeconfigs** under **Manage Groups**. You can also use **Cloud Discovery** to find clusters through your cloud credentials.

## Explore

Browse resources, inspect pod status, read logs, and view related events. Use the cluster selector to switch clusters.

If a cluster does not connect, check your network or VPN and make sure your cluster login is current. For other problems, [open an issue](https://github.com/kanivet-ai/kanivet-oss/issues) with your Kanivet version and operating system.
