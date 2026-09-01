package incidents

import (
	"testing"

	"github.com/kanivet/backend/internal/db"
)

func TestClassify_Critical(t *testing.T) {
	cases := []struct {
		name  string
		event db.K8sEvent
		want  Severity
	}{
		{"warning Failed", db.K8sEvent{Type: "Warning", Reason: "Failed"}, SeverityCritical},
		{"warning BackOff", db.K8sEvent{Type: "Warning", Reason: "BackOff"}, SeverityCritical},
		{"warning OOMKilled", db.K8sEvent{Type: "Warning", Reason: "OOMKilled"}, SeverityCritical},
		{"warning CrashLoopBackOff", db.K8sEvent{Type: "Warning", Reason: "CrashLoopBackOff"}, SeverityCritical},
		{"warning FailedScheduling", db.K8sEvent{Type: "Warning", Reason: "FailedScheduling"}, SeverityCritical},
		{"warning Evicted", db.K8sEvent{Type: "Warning", Reason: "Evicted"}, SeverityCritical},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sev, _ := Classify(tc.event)
			if sev != tc.want {
				t.Fatalf("got %q, want %q", sev, tc.want)
			}
		})
	}
}

func TestClassify_Warning(t *testing.T) {
	sev, _ := Classify(db.K8sEvent{Type: "Warning", Reason: "Unhealthy"})
	if sev != SeverityWarning {
		t.Fatalf("got %q, want %q", sev, SeverityWarning)
	}
}

func TestClassify_Info(t *testing.T) {
	sev, _ := Classify(db.K8sEvent{Type: "Normal", Reason: "Started"})
	if sev != SeverityInfo {
		t.Fatalf("got %q, want %q", sev, SeverityInfo)
	}
}

func TestClassify_Routine(t *testing.T) {
	cases := []struct {
		name    string
		event   db.K8sEvent
		routine bool
	}{
		{"normal Scheduled", db.K8sEvent{Type: "Normal", Reason: "Scheduled"}, true},
		{"normal Pulling", db.K8sEvent{Type: "Normal", Reason: "Pulling"}, true},
		{"normal Pulled", db.K8sEvent{Type: "Normal", Reason: "Pulled"}, true},
		{"normal Created", db.K8sEvent{Type: "Normal", Reason: "Created"}, true},
		{"normal Started", db.K8sEvent{Type: "Normal", Reason: "Started"}, true},
		{"normal SuccessfulCreate", db.K8sEvent{Type: "Normal", Reason: "SuccessfulCreate"}, true},
		{"warning FailedScheduling not routine", db.K8sEvent{Type: "Warning", Reason: "FailedScheduling"}, false},
		{"normal unknown not routine", db.K8sEvent{Type: "Normal", Reason: "SomethingNovel"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, r := Classify(tc.event)
			if r != tc.routine {
				t.Fatalf("routine: got %v, want %v", r, tc.routine)
			}
		})
	}
}
