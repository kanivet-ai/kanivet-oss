package rightsizing

import (
	"math/rand/v2"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
)

func TestJVMDetection(t *testing.T) {
	cases := []struct {
		name string
		c    v1.Container
		want *JVMInfo
	}{
		{"plain app", v1.Container{Image: "ghcr.io/acme/api:1.2"}, nil},
		{"jdk image, default sizing", v1.Container{Image: "eclipse-temurin:21-jre"}, &JVMInfo{RAMPercentage: 25}},
		{"percentage in tool options", v1.Container{Image: "acme/svc", Env: []v1.EnvVar{{Name: "JAVA_TOOL_OPTIONS", Value: "-XX:MaxRAMPercentage=75.0"}}}, &JVMInfo{RAMPercentage: 75}},
		{"fixed heap in args", v1.Container{Image: "acme/svc", Args: []string{"java", "-Xmx2g"}}, &JVMInfo{HeapMax: 2 * gib}},
		{"last -Xmx wins", v1.Container{Image: "keycloak/keycloak", Args: []string{"-Xmx512m"}, Env: []v1.EnvVar{{Name: "JAVA_OPTS", Value: "-Xmx1024m"}}}, &JVMInfo{HeapMax: 1024 * mib}},
		// command replaces the entrypoint and is where manifests often start java.
		{"fixed heap in command", v1.Container{Image: "acme/billing:1", Command: []string{"java", "-Xmx2g", "-jar", "/app.jar"}}, &JVMInfo{HeapMax: 2 * gib}},
		{"fixed heap in command, jdk image", v1.Container{Image: "eclipse-temurin:21-jre", Command: []string{"/opt/java/openjdk/bin/java", "-Xmx2g", "-jar", "/app.jar"}}, &JVMInfo{HeapMax: 2 * gib}},
		{"fixed heap in a shell command", v1.Container{Image: "acme/billing:1", Command: []string{"sh", "-c", "exec java -Xmx2g -jar /app.jar"}}, &JVMInfo{HeapMax: 2 * gib}},
		{"launcher in command, heap in args", v1.Container{Image: "acme/billing:1", Command: []string{"java"}, Args: []string{"-Xmx2g", "-jar", "/app.jar"}}, &JVMInfo{HeapMax: 2 * gib}},
		{"args after command win", v1.Container{Image: "acme/billing:1", Command: []string{"java", "-Xmx1g"}, Args: []string{"-Xmx3g", "-jar", "/app.jar"}}, &JVMInfo{HeapMax: 3 * gib}},
		{"MaxHeapSize is -Xmx", v1.Container{Image: "acme/billing:1", Env: []v1.EnvVar{{Name: "JAVA_TOOL_OPTIONS", Value: "-XX:MaxHeapSize=2g"}}}, &JVMInfo{HeapMax: 2 * gib}},
		{"MaxHeapSize on a jdk image", v1.Container{Image: "eclipse-temurin:21", Env: []v1.EnvVar{{Name: "JAVA_TOOL_OPTIONS", Value: "-XX:MaxHeapSize=1536m"}}}, &JVMInfo{HeapMax: 1536 * mib}},
		// Words that merely contain "java" are not a JVM.
		{"python module named java-something", v1.Container{Image: "python:3.12", Command: []string{"python", "-m", "javalang_tool"}}, nil},
		{"node script named javascript", v1.Container{Image: "node:22", Command: []string{"node", "javascript-worker.js"}}, nil},
	}
	for _, tc := range cases {
		got := jvmOf(tc.c)
		if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
			t.Errorf("%s: got %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

func TestHeapCeilingDetection(t *testing.T) {
	cases := []struct {
		name string
		c    v1.Container
		want *HeapCeiling
	}{
		{"plain app", v1.Container{Image: "node:22", Command: []string{"node", "server.js"}}, nil},
		{"node options", v1.Container{Env: []v1.EnvVar{{Name: "NODE_OPTIONS", Value: "--max-old-space-size=4096"}}}, &HeapCeiling{Runtime: "node", Setting: "--max-old-space-size", Max: 4096 * mib}},
		{"node command line wins", v1.Container{Command: []string{"node", "--max_old_space_size=1536", "server.js"}, Env: []v1.EnvVar{{Name: "NODE_OPTIONS", Value: "--max-old-space-size=4096"}}}, &HeapCeiling{Runtime: "node", Setting: "--max-old-space-size", Max: 1536 * mib}},
		{"node in a shell", v1.Container{Command: []string{"sh", "-c", "exec node --max-old-space-size=2048 server.js"}}, &HeapCeiling{Runtime: "node", Setting: "--max-old-space-size", Max: 2 * gib}},
		{".NET hard limit is hex", v1.Container{Env: []v1.EnvVar{{Name: "DOTNET_GCHeapHardLimit", Value: "0x40000000"}}}, &HeapCeiling{Runtime: "dotnet", Setting: "DOTNET_GCHeapHardLimit", Max: gib}},
		{".NET legacy name, no 0x", v1.Container{Env: []v1.EnvVar{{Name: "COMPlus_GCHeapHardLimit", Value: "20000000"}}}, &HeapCeiling{Runtime: "dotnet", Setting: "COMPlus_GCHeapHardLimit", Max: 512 * mib}},
		{"GOMEMLIMIT with the GC off", v1.Container{Env: []v1.EnvVar{{Name: "GOGC", Value: "off"}, {Name: "GOMEMLIMIT", Value: "1536MiB"}}}, &HeapCeiling{Runtime: "go", Setting: "GOMEMLIMIT", Max: 1536 * mib}},
		// A soft limit the GC works to stay under is no size the heap grows to.
		{"GOMEMLIMIT alone", v1.Container{Env: []v1.EnvVar{{Name: "GOMEMLIMIT", Value: "1GiB"}}}, nil},
		{"GOMEMLIMIT from the limit", v1.Container{Env: []v1.EnvVar{{Name: "GOGC", Value: "off"}, {Name: "GOMEMLIMIT", ValueFrom: &v1.EnvVarSource{}}}}, nil},
	}
	for _, tc := range cases {
		got := heapCeilingOf(tc.c)
		if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
			t.Errorf("%s: got %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

// Node allowed a 2Gi heap that has used 300Mi so far: the limit must hold
// the heap it may grow to, and a limit that can't is under-provisioned.
func TestAnalyzeHeapCeilingSetsAFloor(t *testing.T) {
	rng := rand.New(rand.NewPCG(27, 27))
	in := synth(14, 1, diurnal(rng, 0.1, 0.2), flatMem(rng, 300*mib))
	in.current = Resources{CPURequest: 0.2, MemRequest: 4 * gib, MemLimit: 4 * gib}
	in.heap = &HeapCeiling{Runtime: "node", Setting: "--max-old-space-size", Max: 2 * gib}
	r := analyze(in)
	if r.Memory.Recommended < 2.5*gib || r.Heap == nil {
		t.Fatalf("recommended %vMi under the 2Gi heap plus what Node uses outside it", r.Memory.Recommended/mib)
	}
	in.current = Resources{CPURequest: 0.2, MemRequest: gib, MemLimit: gib}
	r = analyze(in)
	if r.Memory.Verdict != VerdictUnder || !hasFinding(r, "heap-over-limit") {
		t.Fatalf("verdict %s findings %+v", r.Memory.Verdict, r.Findings)
	}
}

// A JVM allowed a 1Gi heap that has only used 300Mi so far must still get a
// limit that holds the full heap plus off-heap memory.
func TestAnalyzeJVMFixedHeapSetsAFloor(t *testing.T) {
	rng := rand.New(rand.NewPCG(21, 21))
	in := synth(14, 1, diurnal(rng, 0.1, 0.2), flatMem(rng, 300*mib))
	in.current = Resources{CPURequest: 0.2, MemRequest: 2 * gib, MemLimit: 2 * gib}
	in.jvm = &JVMInfo{HeapMax: gib}
	r := analyze(in)
	if r.Memory.Recommended < 1.25*gib {
		t.Fatalf("recommended %v Mi under the 1Gi heap plus off-heap", r.Memory.Recommended/mib)
	}

	// A limit that can't hold the heap is under-provisioned, whatever usage says.
	in.current = Resources{CPURequest: 0.2, MemRequest: 512 * mib, MemLimit: 512 * mib}
	r = analyze(in)
	if r.Memory.Verdict != VerdictUnder || !hasFinding(r, "jvm-heap-over-limit") {
		t.Fatalf("verdict %s findings %+v", r.Memory.Verdict, r.Findings)
	}
}

func TestAnalyzeJVMHeapFollowingLimitIsFlagged(t *testing.T) {
	rng := rand.New(rand.NewPCG(22, 22))
	in := synth(14, 2, diurnal(rng, 0.1, 0.2), flatMem(rng, 300*mib))
	in.current = Resources{CPURequest: 1, MemRequest: 4 * gib, MemLimit: 4 * gib}
	in.jvm = &JVMInfo{RAMPercentage: 25}
	r := analyze(in)
	if !hasFinding(r, "jvm-heap-follows-limit") || r.Confidence == "high" {
		t.Fatalf("confidence %s findings %+v", r.Confidence, r.Findings)
	}
}

func TestAnalyzeRecentRolloutIsFlagged(t *testing.T) {
	rng := rand.New(rand.NewPCG(23, 23))
	in := synth(14, 2, diurnal(rng, 0.1, 0.2), flatMem(rng, 300*mib))
	in.current = Resources{CPURequest: 1, MemRequest: gib, MemLimit: gib}
	in.versionSince = t0.Add(13*day + 6*time.Hour) // 18 hours before the end
	r := analyze(in)
	if !hasFinding(r, "new-version") || r.Confidence == "high" || r.VersionSince == nil {
		t.Fatalf("confidence %s findings %+v", r.Confidence, r.Findings)
	}
}
