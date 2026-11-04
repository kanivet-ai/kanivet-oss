package compare

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"

	"gopkg.in/yaml.v3"
)

// ToYAML renders obj in the order kubectl users expect (apiVersion, kind,
// metadata, then fields alphabetically, status last), so both sides of a diff
// line up field by field.
func ToYAML(obj any) (string, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(toNode(obj)); err != nil {
		return "", err
	}
	_ = enc.Close()
	return buf.String(), nil
}

func toNode(v any) *yaml.Node {
	switch x := v.(type) {
	case map[string]any:
		n := &yaml.Node{Kind: yaml.MappingNode}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			ri, rj := keyRank(keys[i]), keyRank(keys[j])
			if ri != rj {
				return ri < rj
			}
			return keys[i] < keys[j]
		})
		for _, k := range keys {
			n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k}, toNode(x[k]))
		}
		return n
	case []any:
		n := &yaml.Node{Kind: yaml.SequenceNode}
		for _, it := range x {
			n.Content = append(n.Content, toNode(it))
		}
		return n
	case nil:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}
	case string:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: x}
	case bool:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(x)}
	case int64:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.FormatInt(x, 10)}
	case int:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(x)}
	case float64:
		if x == float64(int64(x)) {
			return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.FormatInt(int64(x), 10)}
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!float", Value: strconv.FormatFloat(x, 'g', -1, 64)}
	}
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: fmt.Sprint(v)}
}
