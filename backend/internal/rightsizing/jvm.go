package rightsizing

import (
	"math"
	"path"
	"slices"
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
	// command replaces the image's entrypoint and often starts the JVM itself
	// (java -Xmx2g -jar app.jar, or the same inside sh -c), so its words come
	// first, then the arguments, then the option variables.
	line := slices.Concat(c.Command, c.Args)
	for _, e := range c.Env {
		switch e.Name {
		case "JAVA_TOOL_OPTIONS", "JDK_JAVA_OPTIONS", "_JAVA_OPTIONS", "JAVA_OPTS":
			line = append(line, e.Value)
		}
	}
	var words []string
	for _, w := range strings.Fields(strings.Join(line, " ")) {
		words = append(words, strings.Trim(w, `"'`))
	}
	image := strings.ToLower(c.Image)
	isJVM := slices.ContainsFunc(words, jvmWord)
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
	for _, f := range words {
		switch {
		case strings.HasPrefix(f, "-Xmx"):
			if b, ok := parseJVMSize(strings.TrimPrefix(f, "-Xmx")); ok {
				info.HeapMax = b
			}
		case strings.HasPrefix(f, "-XX:MaxHeapSize="):
			// The long form of -Xmx.
			if b, ok := parseJVMSize(strings.TrimPrefix(f, "-XX:MaxHeapSize=")); ok {
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

// jvmWord says whether one word of a command line is the java launcher or an
// option only a JVM takes. Whole words count, not substrings: a script named
// javascript-worker or a Python module named javalang is not a JVM.
func jvmWord(w string) bool {
	if path.Base(w) == "java" {
		return true
	}
	for _, p := range []string{"-Xmx", "-Xms", "-Xss", "-XX:", "-javaagent:", "-Djava."} {
		if strings.HasPrefix(w, p) {
			return true
		}
	}
	return false
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

// Other runtimes can be given a fixed heap ceiling too, and grow into it the
// same way: the memory limit must hold it plus what they use outside the
// heap (buffers, native memory, compiled code), or the kernel kills them
// first.

// HeapCeiling is a fixed heap ceiling set for a runtime other than the JVM.
type HeapCeiling struct {
	Runtime string  `json:"runtime"` // node | dotnet | go
	Setting string  `json:"setting"` // the flag or variable that sets it
	Max     float64 `json:"max"`     // bytes
}

func (h *HeapCeiling) runtimeName() string {
	switch h.Runtime {
	case "node":
		return "Node"
	case "dotnet":
		return ".NET"
	}
	return "Go"
}

// heapCeilingOf reads a fixed heap ceiling from a container's command line
// and environment. It returns nil when there is none.
func heapCeilingOf(c v1.Container) *HeapCeiling {
	env := map[string]string{}
	for _, e := range c.Env {
		env[e.Name] = e.Value
	}
	// Node takes --max-old-space-size in MiB from NODE_OPTIONS, then from
	// its command line, which wins.
	var node float64
	line := append([]string{env["NODE_OPTIONS"]}, slices.Concat(c.Command, c.Args)...)
	for _, w := range strings.Fields(strings.Join(line, " ")) {
		w = strings.Trim(w, `"'`)
		for _, flag := range []string{"--max-old-space-size=", "--max_old_space_size="} {
			if v, ok := strings.CutPrefix(w, flag); ok {
				if mb, err := strconv.ParseFloat(v, 64); err == nil && mb > 0 {
					node = mb * mib
				}
			}
		}
	}
	if node > 0 {
		return &HeapCeiling{Runtime: "node", Setting: "--max-old-space-size", Max: node}
	}
	// .NET's hard limit on the GC heap, in hexadecimal bytes.
	for _, k := range []string{"DOTNET_GCHeapHardLimit", "COMPlus_GCHeapHardLimit"} {
		v := strings.TrimPrefix(strings.TrimPrefix(env[k], "0x"), "0X")
		if b, err := strconv.ParseUint(v, 16, 64); err == nil && b > 0 {
			return &HeapCeiling{Runtime: "dotnet", Setting: k, Max: float64(b)}
		}
	}
	// GOMEMLIMIT is a soft limit the Go GC works to stay under, not a size it
	// grows to, except with the GC otherwise off: then the heap fills up to it.
	if strings.EqualFold(env["GOGC"], "off") {
		if b, ok := parseGoMemLimit(env["GOMEMLIMIT"]); ok {
			return &HeapCeiling{Runtime: "go", Setting: "GOMEMLIMIT", Max: b}
		}
	}
	return nil
}

// parseGoMemLimit reads a GOMEMLIMIT value: bytes with an optional B, KiB,
// MiB, GiB or TiB suffix.
func parseGoMemLimit(s string) (float64, bool) {
	mult := 1.0
	for _, u := range []struct {
		suffix string
		mult   float64
	}{{"KiB", 1 << 10}, {"MiB", 1 << 20}, {"GiB", 1 << 30}, {"TiB", 1 << 40}, {"B", 1}} {
		if v, ok := strings.CutSuffix(s, u.suffix); ok {
			s, mult = v, u.mult
			break
		}
	}
	v, err := strconv.ParseFloat(s, 64)
	// The largest int64 is Go's default: no limit.
	if err != nil || v <= 0 || v*mult >= math.MaxInt64 {
		return 0, false
	}
	return v * mult, true
}
