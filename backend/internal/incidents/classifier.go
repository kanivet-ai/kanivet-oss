package incidents

import "github.com/kanivet/backend/internal/db"

var criticalReasons = map[string]struct{}{
	"Failed":             {},
	"FailedScheduling":   {},
	"FailedMount":        {},
	"FailedCreate":       {},
	"FailedSync":         {},
	"FailedKillPod":      {},
	"BackOff":            {},
	"CrashLoopBackOff":   {},
	"OOMKilled":          {},
	"Evicted":            {},
	"NodeNotReady":       {},
	"ContainerCannotRun": {},
	"ErrImagePull":       {},
	"ImagePullBackOff":   {},
	"Unschedulable":      {},
	"DeadlineExceeded":   {},
	"PodOOMKilling":      {},
}

var routineNormalReasons = map[string]struct{}{
	"Scheduled":          {},
	"Pulling":            {},
	"Pulled":             {},
	"Created":            {},
	"Started":            {},
	"SuccessfulCreate":   {},
	"SuccessfulDelete":   {},
	"Killing":            {},
	"NodeReady":          {},
	"NodeSchedulable":    {},
	"SandboxChanged":     {},
	"LeaderElection":     {},
	"RegisteredNode":     {},
	"ScalingReplicaSet":  {},
	"SuccessfulRescale":  {},
	"SuccessfulMountVol": {},
}

func Classify(event db.K8sEvent) (Severity, bool) {
	if event.Type == "Warning" {
		if _, ok := criticalReasons[event.Reason]; ok {
			return SeverityCritical, false
		}
		return SeverityWarning, false
	}
	_, routine := routineNormalReasons[event.Reason]
	return SeverityInfo, routine
}
