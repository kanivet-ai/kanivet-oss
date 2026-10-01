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
	}
	for _, tc := range cases {
		got := jvmOf(tc.c)
		if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
			t.Errorf("%s: got %+v, want %+v", tc.name, got, tc.want)
		}
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
