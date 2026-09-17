package printercolumns

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Column is one CRD additionalPrinterColumn, matching kubectl's table schema.
type Column struct {
	Name     string
	Type     string
	JSONPath string
	Priority int32
}

// Cell is a evaluated printer-column value for one list item.
type Cell struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Value    string `json:"value"`
	Priority int32  `json:"priority"`
}

const printerColumnsKey = "printerColumns"

// Attach stores evaluated printer cells on a simplified list item.
func Attach(item map[string]interface{}, cells []Cell) {
	if len(cells) == 0 || item == nil {
		return
	}
	out := make([]interface{}, len(cells))
	for i := range cells {
		out[i] = map[string]interface{}{
			"name":     cells[i].Name,
			"type":     cells[i].Type,
			"value":    cells[i].Value,
			"priority": cells[i].Priority,
		}
	}
	item[printerColumnsKey] = out
}

// ParseFromCRD extracts additionalPrinterColumns for the given served version.
// If version is empty or missing, the storage version (or first served version) is used.
func ParseFromCRD(crd map[string]interface{}, version string) []Column {
	if crd == nil {
		return nil
	}
	versions, found, _ := unstructured.NestedSlice(crd, "spec", "versions")
	if !found {
		return parseColumnList(nestedSlice(crd, "spec", "additionalPrinterColumns"))
	}

	var matched, storage, served map[string]interface{}
	for _, raw := range versions {
		vm, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := vm["name"].(string)
		if name == version {
			matched = vm
		}
		if storageTrue(vm["storage"]) {
			storage = vm
		}
		if served == nil && storageTrue(vm["served"]) {
			served = vm
		}
	}

	chosen := matched
	if chosen == nil {
		chosen = storage
	}
	if chosen == nil {
		chosen = served
	}
	if chosen == nil {
		return nil
	}
	return parseColumnList(nestedSlice(chosen, "additionalPrinterColumns"))
}

func parseColumnList(raw []interface{}) []Column {
	if len(raw) == 0 {
		return nil
	}
	cols := make([]Column, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		name := stringField(m, "name")
		jsonPath := stringField(m, "jsonPath")
		if jsonPath == "" {
			jsonPath = stringField(m, "JSONPath")
		}
		if name == "" || jsonPath == "" {
			continue
		}
		colType := stringField(m, "type")
		if colType == "" {
			colType = "string"
		}
		cols = append(cols, Column{
			Name:     name,
			Type:     colType,
			JSONPath: jsonPath,
			Priority: int32Field(m["priority"]),
		})
	}
	if len(cols) == 0 {
		return nil
	}
	return cols
}

func nestedSlice(obj map[string]interface{}, fields ...string) []interface{} {
	s, found, _ := unstructured.NestedSlice(obj, fields...)
	if !found {
		return nil
	}
	return s
}

func stringField(m map[string]interface{}, key string) string {
	s, _ := m[key].(string)
	return s
}

func int32Field(v interface{}) int32 {
	switch n := v.(type) {
	case int32:
		return n
	case int64:
		return int32(n)
	case int:
		return int32(n)
	case float64:
		return int32(n)
	case float32:
		return int32(n)
	default:
		return 0
	}
}

func storageTrue(v interface{}) bool {
	b, ok := v.(bool)
	return ok && b
}
