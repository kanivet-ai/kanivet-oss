package storage

import "strings"

// BuildResourceID returns the canonical document ID for a resource:
// cluster/group/version/resource/namespace/name, with the namespace segment
// omitted for cluster-scoped resources. resource is the plural API resource
// name, the same spelling the tree and the items topics use, so a document
// indexed from a LIST sweep and one indexed from a watch event share an ID.
func BuildResourceID(cluster, group, version, resource, namespace, name string) string {
	if namespace != "" {
		return cluster + "/" + group + "/" + version + "/" + resource + "/" + namespace + "/" + name
	}
	return cluster + "/" + group + "/" + version + "/" + resource + "/" + name
}

// ResourceNameFromID extracts the plural resource segment from a canonical
// document ID. Cluster names may themselves contain slashes (EKS ARNs do), so
// the caller supplies the cluster and only the remainder is split.
func ResourceNameFromID(id, cluster string) (string, bool) {
	prefix := cluster + "/"
	if cluster == "" || !strings.HasPrefix(id, prefix) {
		return "", false
	}
	parts := strings.Split(id[len(prefix):], "/")
	if len(parts) != 4 && len(parts) != 5 {
		return "", false
	}
	if parts[2] == "" {
		return "", false
	}
	return parts[2], true
}
