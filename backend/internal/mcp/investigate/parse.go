package investigate

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Event is one step of an investigation's progress.
type Event struct {
	Seq   int    `json:"seq"`
	Time  int64  `json:"time"` // unix ms
	Type  string `json:"type"` // status | thinking | text | tool | tool_result | error
	Text  string `json:"text,omitempty"`
	Tool  string `json:"tool,omitempty"`
	Input string `json:"input,omitempty"`
	OK    *bool  `json:"ok,omitempty"`
}

// ResourceRef is a resource the findings point at.
type ResourceRef struct {
	Kind       string `json:"kind"`
	APIVersion string `json:"apiVersion,omitempty"`
	Namespace  string `json:"namespace,omitempty"`
	Name       string `json:"name"`
	Cluster    string `json:"cluster,omitempty"`
}

// Finding is one conclusion with its evidence.
type Finding struct {
	Title     string        `json:"title"`
	Detail    string        `json:"detail,omitempty"`
	Severity  string        `json:"severity,omitempty"`
	Resources []ResourceRef `json:"resources,omitempty"`
}

// Findings is the structured end result the agent is asked to emit.
type Findings struct {
	Summary    string        `json:"summary"`
	RootCause  string        `json:"rootCause,omitempty"`
	Severity   string        `json:"severity,omitempty"`
	Confidence string        `json:"confidence,omitempty"`
	Findings   []Finding     `json:"findings,omitempty"`
	NextSteps  []string      `json:"nextSteps,omitempty"`
	Resources  []ResourceRef `json:"resources,omitempty"`
}

// parsed is what one output line of an agent contributes.
type parsed struct {
	events []Event
	final  string
	cost   float64
	err    string
}

// parseClaudeLine reads one line of `claude -p --output-format stream-json`.
func parseClaudeLine(line []byte) parsed {
	var m map[string]any
	if json.Unmarshal(line, &m) != nil {
		return parsed{}
	}
	var p parsed
	switch m["type"] {
	case "system":
		if m["subtype"] == "init" {
			status := "connected"
			if servers, ok := m["mcp_servers"].([]any); ok {
				for _, s := range servers {
					sm, _ := s.(map[string]any)
					if sm["name"] == "kanivet" {
						status, _ = sm["status"].(string)
					}
				}
			}
			p.events = append(p.events, Event{Type: "status", Text: "Agent started; Kanivet MCP " + status})
		}
	case "assistant":
		msg, _ := m["message"].(map[string]any)
		content, _ := msg["content"].([]any)
		for _, c := range content {
			cm, _ := c.(map[string]any)
			switch cm["type"] {
			case "text":
				if t, _ := cm["text"].(string); strings.TrimSpace(t) != "" {
					p.events = append(p.events, Event{Type: "text", Text: t})
				}
			case "thinking":
				if t, _ := cm["thinking"].(string); strings.TrimSpace(t) != "" {
					p.events = append(p.events, Event{Type: "thinking", Text: t})
				}
			case "tool_use":
				name, _ := cm["name"].(string)
				p.events = append(p.events, Event{Type: "tool", Tool: shortToolName(name), Input: compactInput(cm["input"])})
			}
		}
	case "user":
		msg, _ := m["message"].(map[string]any)
		content, _ := msg["content"].([]any)
		for _, c := range content {
			cm, _ := c.(map[string]any)
			if cm["type"] == "tool_result" {
				isErr, _ := cm["is_error"].(bool)
				ok := !isErr
				p.events = append(p.events, Event{Type: "tool_result", OK: &ok, Text: resultPreview(cm["content"])})
			}
		}
	case "result":
		p.final, _ = m["result"].(string)
		p.cost, _ = m["total_cost_usd"].(float64)
		if isErr, _ := m["is_error"].(bool); isErr {
			p.err = fmt.Sprint(m["subtype"])
			if p.final != "" {
				p.err = p.final
			}
		}
	}
	return p
}

// parseCodexLine reads one line of `codex exec --json`.
func parseCodexLine(line []byte) parsed {
	var m map[string]any
	if json.Unmarshal(line, &m) != nil {
		return parsed{}
	}
	var p parsed
	typ, _ := m["type"].(string)
	item, _ := m["item"].(map[string]any)
	itype, _ := item["type"].(string)
	switch {
	case typ == "thread.started":
		p.events = append(p.events, Event{Type: "status", Text: "Agent started"})
	case typ == "item.started" && itype == "mcp_tool_call":
		tool, _ := item["tool"].(string)
		p.events = append(p.events, Event{Type: "tool", Tool: tool, Input: compactInput(item["arguments"])})
	case typ == "item.completed" && itype == "mcp_tool_call":
		status, _ := item["status"].(string)
		ok := status != "failed"
		p.events = append(p.events, Event{Type: "tool_result", OK: &ok, Text: resultPreview(item["result"])})
	case typ == "item.completed" && itype == "reasoning":
		if t, _ := item["text"].(string); t != "" {
			p.events = append(p.events, Event{Type: "thinking", Text: t})
		}
	case typ == "item.completed" && itype == "agent_message":
		if t, _ := item["text"].(string); t != "" {
			p.events = append(p.events, Event{Type: "text", Text: t})
			p.final = t
		}
	case typ == "error" || typ == "turn.failed":
		msg, _ := m["message"].(string)
		if e, ok := m["error"].(map[string]any); ok {
			msg, _ = e["message"].(string)
		}
		p.err = msg
	}
	return p
}

func shortToolName(n string) string {
	return strings.TrimPrefix(n, "mcp__kanivet__")
}

func compactInput(v any) string {
	if v == nil {
		return ""
	}
	b, _ := json.Marshal(v)
	s := string(b)
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}

func resultPreview(v any) string {
	var s string
	switch t := v.(type) {
	case string:
		s = t
	case []any:
		for _, c := range t {
			if cm, ok := c.(map[string]any); ok {
				if txt, ok := cm["text"].(string); ok {
					s += txt
				}
			}
		}
	case map[string]any:
		if c, ok := t["content"]; ok {
			return resultPreview(c)
		}
		b, _ := json.Marshal(t)
		s = string(b)
	}
	s = strings.TrimSpace(s)
	if len(s) > 240 {
		s = s[:240] + "…"
	}
	return s
}

var jsonFence = regexp.MustCompile("(?s)```(?:json)?\\s*(\\{.*?\\})\\s*```")

// ExtractFindings pulls the structured findings block out of the agent's
// final answer and returns the remaining markdown report.
func ExtractFindings(final string) (*Findings, string) {
	matches := jsonFence.FindAllStringSubmatchIndex(final, -1)
	for i := len(matches) - 1; i >= 0; i-- {
		m := matches[i]
		var f Findings
		if json.Unmarshal([]byte(final[m[2]:m[3]]), &f) == nil && (f.Summary != "" || len(f.Findings) > 0) {
			report := strings.TrimSpace(final[:m[0]] + final[m[1]:])
			return &f, report
		}
	}
	return nil, strings.TrimSpace(final)
}
