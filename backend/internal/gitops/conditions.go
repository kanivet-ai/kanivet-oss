package gitops

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Condition is a trimmed metav1.Condition as Flux and CNPG report them.
type Condition struct {
	Type               string `json:"type"`
	Status             string `json:"status"`
	Reason             string `json:"reason,omitempty"`
	Message            string `json:"message,omitempty"`
	LastTransitionTime string `json:"lastTransitionTime,omitempty"`
}

// ReadConditions returns status.conditions of any object.
func ReadConditions(obj *unstructured.Unstructured) []Condition {
	raw, _, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
	out := make([]Condition, 0, len(raw))
	for _, r := range raw {
		m, ok := r.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, Condition{
			Type:               str(m["type"]),
			Status:             str(m["status"]),
			Reason:             str(m["reason"]),
			Message:            str(m["message"]),
			LastTransitionTime: str(m["lastTransitionTime"]),
		})
	}
	return out
}

// FindCondition returns the condition of the given type, or nil.
func FindCondition(conds []Condition, t string) *Condition {
	for i := range conds {
		if conds[i].Type == t {
			return &conds[i]
		}
	}
	return nil
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func nestedStr(obj map[string]any, path ...string) string {
	s, _, _ := unstructured.NestedString(obj, path...)
	return s
}
