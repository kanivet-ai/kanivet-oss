package printercolumns

import (
	"fmt"
	"reflect"
	"strings"

	"k8s.io/client-go/util/jsonpath"
)

// Evaluate applies CRD printer-column JSONPaths to a resource object, matching
// the apiserver table convertor used by kubectl get.
func Evaluate(obj map[string]interface{}, columns []Column) []Cell {
	if obj == nil || len(columns) == 0 {
		return nil
	}
	cells := make([]Cell, 0, len(columns))
	for _, col := range columns {
		cells = append(cells, Cell{
			Name:     col.Name,
			Type:     col.Type,
			Value:    evaluateColumn(obj, col),
			Priority: col.Priority,
		})
	}
	return cells
}

func evaluateColumn(obj map[string]interface{}, col Column) string {
	path := jsonpath.New(col.Name)
	path.AllowMissingKeys(true)
	expr := col.JSONPath
	if !strings.HasPrefix(strings.TrimSpace(expr), "{") {
		expr = "{" + expr + "}"
	}
	if err := path.Parse(expr); err != nil {
		return ""
	}
	results, err := path.FindResults(obj)
	if err != nil || len(results) == 0 || len(results[0]) == 0 {
		return ""
	}
	value := results[0][0].Interface()
	if value == nil {
		return ""
	}
	return stringifyCell(col.Type, value)
}

func stringifyCell(colType string, value interface{}) string {
	if value == nil {
		return ""
	}
	switch strings.ToLower(colType) {
	case "boolean":
		if b, ok := value.(bool); ok {
			if b {
				return "True"
			}
			return "False"
		}
	case "integer":
		switch typed := value.(type) {
		case int64:
			return fmt.Sprintf("%d", typed)
		case int32:
			return fmt.Sprintf("%d", typed)
		case int:
			return fmt.Sprintf("%d", typed)
		case float64:
			return fmt.Sprintf("%d", int64(typed))
		}
	case "string", "date":
		if s, ok := value.(string); ok {
			return s
		}
		if rv := reflect.ValueOf(value); rv.Kind() == reflect.String {
			return rv.String()
		}
	}
	if s, ok := value.(string); ok {
		return s
	}
	if b, ok := value.(bool); ok {
		if b {
			return "True"
		}
		return "False"
	}
	return fmt.Sprint(value)
}
