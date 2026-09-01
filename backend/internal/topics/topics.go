package topics

import "fmt"

// BuildItemsTopic builds the topic string for resource items
func BuildItemsTopic(cluster, group, version, kind, namespace string) string {
	if group == "_" {
		group = ""
	}
	if namespace == "_" {
		namespace = ""
	}
	return fmt.Sprintf("items:%s:%s:%s:%s:%s", cluster, group, version, kind, namespace)
}

// BuildCountsTopic builds the topic string for resource counts
func BuildCountsTopic(cluster string) string {
	return fmt.Sprintf("counts:%s", cluster)
}

// BuildDashboardTopic builds the topic string for dashboard metrics
func BuildDashboardTopic(cluster string) string {
	return fmt.Sprintf("dashboard:%s", cluster)
}

// ParseItemsTopic parses an items topic into its components
func ParseItemsTopic(topic string) (cluster, group, version, kind, namespace string, ok bool) {
	const prefix = "items:"
	if len(topic) < len(prefix) || topic[:len(prefix)] != prefix {
		return "", "", "", "", "", false
	}

	// For cluster names containing colons (like AWS ARNs), we need special handling
	// Use splitNFromEnd to preserve colons in the cluster name
	remainder := topic[len(prefix):]
	parts := splitNFromEnd(remainder, ":", 5)

	if len(parts) != 5 {
		return "", "", "", "", "", false
	}

	return parts[0], parts[1], parts[2], parts[3], parts[4], true
}

// ExtractClusterFromTopic extracts the cluster name from a topic with given prefix
func ExtractClusterFromTopic(topic, prefix string) string {
	if len(topic) < len(prefix) || topic[:len(prefix)] != prefix {
		return ""
	}
	return topic[len(prefix):]
}

func splitN(s, sep string, n int) []string {
	parts := make([]string, 0, n)
	for i := 0; i < n-1; i++ {
		idx := indexOf(s, sep)
		if idx == -1 {
			break
		}
		parts = append(parts, s[:idx])
		s = s[idx+1:]
	}
	if s != "" {
		parts = append(parts, s)
	}
	return parts
}

// splitNFromEnd splits string s by sep into exactly n parts, but preserves
// everything before the last (n-1) separators as the first part.
// This allows cluster names with colons to be preserved.
func splitNFromEnd(s, sep string, n int) []string {
	if n <= 1 {
		return []string{s}
	}

	// Find all separator positions
	sepPositions := make([]int, 0)
	searchStart := 0
	for {
		pos := indexOf(s[searchStart:], sep)
		if pos == -1 {
			break
		}
		sepPositions = append(sepPositions, searchStart+pos)
		searchStart += pos + len(sep)
	}

	// If we don't have enough separators, fall back to regular split
	if len(sepPositions) < n-1 {
		return splitN(s, sep, n)
	}

	// Take the last (n-1) separator positions
	relevantSeps := sepPositions[len(sepPositions)-(n-1):]

	parts := make([]string, 0, n)

	// First part: everything before the first relevant separator
	parts = append(parts, s[:relevantSeps[0]])

	// Middle parts: between consecutive separators
	for i := 0; i < len(relevantSeps)-1; i++ {
		start := relevantSeps[i] + len(sep)
		end := relevantSeps[i+1]
		parts = append(parts, s[start:end])
	}

	// Last part: everything after the last separator
	lastSepPos := relevantSeps[len(relevantSeps)-1]
	parts = append(parts, s[lastSepPos+len(sep):])

	return parts
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
