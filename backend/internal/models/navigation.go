package models

import "time"

type NavigationEntry struct {
	ID        string      `json:"id"`
	Timestamp time.Time   `json:"timestamp"`
	Type      string      `json:"type"`
	Path      string      `json:"path"`
	Resource  *Resource   `json:"resource,omitempty"`
	Item      interface{} `json:"item,omitempty"`
}

type TabHistory struct {
	ClusterID string            `json:"clusterId"`
	Entries   []NavigationEntry `json:"entries"`
	Index     int               `json:"index"`
}
