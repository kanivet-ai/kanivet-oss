"""Wrap MCP tools so the active cluster is auto-injected.

Many tools accept a `context` (kubernetes-mcp-server) or `cluster`
(kanivet-tools) argument identifying which Kubernetes context the call
should target. Smaller models routinely forget to pass it, which routes
the call to the user's kubeconfig default and produces opaque auth
errors. We solve that by wrapping every tool: if the relevant argument
is missing, we set it to the session's active cluster before the tool
runs. If the agent does provide an explicit value, we honour it.
"""

from __future__ import annotations

from typing import Sequence

from langchain_core.tools import BaseTool


_K8S_MCP_TOOL_PREFIXES: tuple[str, ...] = (
    "pods_",
    "resources_",
    "namespaces_",
    "events_",
    "nodes_",
    "helm_install",
    "helm_list",
    "helm_uninstall",
    "kiali_",
    "tekton_",
    "vm_",
    "configuration_",
    "projects_",
)


_KANIVET_CLUSTER_TOOL_NAMES: frozenset[str] = frozenset(
    {
        "list_namespaces",
        "istio_topology",
        "istio_config_snapshot",
        "istio_effective_route",
        "argo_detect",
        "argo_list_apps",
        "argo_app_details",
        "helm_release_details",
        "helm_release_history",
        "helm_values",
        "helm_manifest",
        "helm_rollback",
        "finops_cluster_cost",
        "finops_namespace_costs",
        "finops_workload_costs",
        "finops_node_costs",
        "finops_pod_costs",
        "incidents_timeline",
        "search",
    }
)


def _wants_context(tool_name: str) -> bool:
    return any(tool_name.startswith(p) for p in _K8S_MCP_TOOL_PREFIXES)


def _wants_cluster(tool_name: str) -> bool:
    return tool_name in _KANIVET_CLUSTER_TOOL_NAMES


def inject_active_cluster(tools: Sequence[BaseTool], cluster: str) -> list[BaseTool]:
    if not cluster:
        return list(tools)
    wrapped: list[BaseTool] = []
    for t in tools:
        name = getattr(t, "name", "")
        if _wants_context(name):
            wrapped.append(_wrap_with_default_arg(t, "context", cluster))
        elif _wants_cluster(name):
            wrapped.append(_wrap_with_default_arg(t, "cluster", cluster))
        else:
            wrapped.append(t)
    return wrapped


def _wrap_with_default_arg(tool: BaseTool, arg_name: str, default_value: str) -> BaseTool:
    original_coroutine = getattr(tool, "coroutine", None)
    original_arun = getattr(tool, "_arun", None)

    async def _patched(**kwargs):
        if not kwargs.get(arg_name):
            kwargs[arg_name] = default_value
        if original_coroutine is not None:
            return await original_coroutine(**kwargs)
        if original_arun is not None:
            return await original_arun(**kwargs)
        sync = getattr(tool, "_run", None)
        if sync is not None:
            return sync(**kwargs)
        raise RuntimeError(f"tool {tool.name} has no callable")

    if original_coroutine is not None:
        tool.coroutine = _patched  # type: ignore[assignment]
    else:
        tool._arun = _patched  # type: ignore[assignment]
    return tool
