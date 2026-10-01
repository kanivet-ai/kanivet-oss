package rightsizing

import (
	"strconv"
	"strings"

	v1 "k8s.io/api/core/v1"
)

// A JVM's memory depends on how its heap is sized. With -Xmx the heap has a
// fixed ceiling, and the container must hold it plus everything off-heap
// (metaspace, code cache, thread stacks, direct buffers). Without it, the
// JVM sizes the heap as a share of the container limit (MaxRAMPercentage,
// 25% by default), so part of the memory seen is there because the limit
// allowed it, and lowering the limit lowers the heap ceiling with it.

// JVMInfo is what is known of a container's JVM heap sizing.
type JVMInfo struct {
	// HeapMax is the -Xmx ceiling in bytes; 0 means the heap follows the
	// limit at RAMPercentage of it.
	HeapMax       float64 `json:"heapMax,omitempty"`
	RAMPercentage float64 `json:"ramPercentage,omitempty"`
}

// jvmImages are image name fragments of JVMs and of software that runs on one.
var jvmImages = []string{
	"openjdk", "temurin", "corretto", "zulu", "graalvm", "liberica", "semeru", "/jre", "/jdk", "-jre", "-jdk",
	"java", "keycloak", "kafka", "elasticsearch", "opensearch", "logstash", "jenkins", "cassandra", "zookeeper",
	"sonarqube", "nexus", "trino", "presto", "spark", "flink", "solr", "tomcat", "jetty", "wildfly",
}

// jvmOf detects a JVM from a container's image, command line and JVM option
// variables. It returns nil for anything else.
func jvmOf(c v1.Container) *JVMInfo {
	opts := strings.Join(c.Args, " ")
	for _, e := range c.Env {
		switch e.Name {
		case "JAVA_TOOL_OPTIONS", "JDK_JAVA_OPTIONS", "_JAVA_OPTIONS", "JAVA_OPTS":
			opts += " " + e.Value
		}
	}
	image := strings.ToLower(c.Image)
	isJVM := strings.Contains(opts, "-Xmx") || strings.Contains(opts, "RAMPercentage") || strings.Contains(opts, "java")
	for _, k := range jvmImages {
		if strings.Contains(image, k) {
			isJVM = true
			break
		}
	}
	if !isJVM {
		return nil
	}
	info := &JVMInfo{RAMPercentage: 25}
	// The last occurrence wins, as on a JVM command line.
	for _, f := range strings.Fields(opts) {
		switch {
		case strings.HasPrefix(f, "-Xmx"):
			if b, ok := parseJVMSize(strings.TrimPrefix(f, "-Xmx")); ok {
				info.HeapMax = b
			}
		case strings.HasPrefix(f, "-XX:MaxRAMPercentage="):
			if p, err := strconv.ParseFloat(strings.TrimPrefix(f, "-XX:MaxRAMPercentage="), 64); err == nil && p > 0 && p <= 100 {
				info.RAMPercentage = p
			}
		}
	}
	if info.HeapMax > 0 {
		info.RAMPercentage = 0
	}
	return info
}

// parseJVMSize reads a JVM size such as 512m, 2G or 1048576.
func parseJVMSize(s string) (float64, bool) {
	if s == "" {
		return 0, false
	}
	mult := 1.0
	switch s[len(s)-1] {
	case 'k', 'K':
		mult = 1 << 10
	case 'm', 'M':
		mult = 1 << 20
	case 'g', 'G':
		mult = 1 << 30
	case 't', 'T':
		mult = 1 << 40
	}
	if mult != 1 {
		s = s[:len(s)-1]
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v <= 0 {
		return 0, false
	}
	return v * mult, true
}

// jvmOffHeap is the least memory a JVM needs beyond its heap: metaspace, the
// code cache, thread stacks and direct buffers commonly take a few hundred
// MiB together. The limit must hold the heap plus this, or the kernel kills
// the JVM before its own heap limit is reached.
func jvmMemoryFloor(heapMax float64) float64 {
	return max(heapMax*1.25, heapMax+192*mib)
}
