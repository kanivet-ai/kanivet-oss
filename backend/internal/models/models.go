package models

type Category struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Resource struct {
	Name       string `json:"name"`
	Group      string `json:"group"`
	Version    string `json:"version"`
	Kind       string `json:"kind"`
	Namespaced bool   `json:"namespaced"`
	Count      *int   `json:"count,omitempty"`
}

type ClusterInfo struct {
	Name      string `json:"name"`
	Context   string `json:"context"`
	Namespace string `json:"namespace"`
	Server    string `json:"server"`
}

type TreeNode struct {
	ID       string      `json:"id"`
	Label    string      `json:"label"`
	Type     string      `json:"type"`
	Level    int         `json:"level"`
	Children []TreeNode  `json:"children,omitempty"`
	Data     interface{} `json:"data,omitempty"`
	Expanded bool        `json:"expanded"`
}
