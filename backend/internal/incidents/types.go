package incidents

import "time"

type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityWarning  Severity = "warning"
	SeverityInfo     Severity = "info"
)

type TimelineFilters struct {
	Cluster        string
	Since          time.Time
	Until          time.Time
	Namespaces     []string
	Kinds          []string
	Severities     []Severity
	Search         string
	IncludeRoutine bool
	Limit          int
}

type TimelineEntry struct {
	ID              string    `json:"id"`
	Severity        Severity  `json:"severity"`
	Reason          string    `json:"reason"`
	Message         string    `json:"message"`
	Kind            string    `json:"kind"`
	APIVersion      string    `json:"apiVersion"`
	Namespace       string    `json:"namespace"`
	Name            string    `json:"name"`
	Count           int32     `json:"count"`
	FirstTimestamp  time.Time `json:"firstTimestamp"`
	LastTimestamp   time.Time `json:"lastTimestamp"`
	SourceComponent string    `json:"sourceComponent"`
	Routine         bool      `json:"routine"`
	EventIDs        []uint    `json:"eventIds"`
}

type TimelineSummary struct {
	Total    int `json:"total"`
	Critical int `json:"critical"`
	Warning  int `json:"warning"`
	Info     int `json:"info"`
	Routine  int `json:"routine"`
}

type TimelineResponse struct {
	Entries []TimelineEntry `json:"entries"`
	Summary TimelineSummary `json:"summary"`
}
