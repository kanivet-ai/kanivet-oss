package investigate

import "encoding/json"

// chatExtras is what a stream line adds beyond parsed events.
type chatExtras struct {
	delta     string // streamed assistant text
	sessionID string
	mcpStatus string // kanivet server status from claude's init line
}

// parseClaudeChatLine reads `claude -p --output-format stream-json
// --include-partial-messages`. Text arrives as deltas; the complete
// assistant message repeats it, so its text blocks are dropped once
// deltas were seen.
func parseClaudeChatLine(line []byte, sawDelta bool) (parsed, chatExtras) {
	var x chatExtras
	var m struct {
		Type       string `json:"type"`
		Subtype    string `json:"subtype"`
		SessionID  string `json:"session_id"`
		MCPServers []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"mcp_servers"`
		Event struct {
			Type  string `json:"type"`
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
		} `json:"event"`
	}
	if json.Unmarshal(line, &m) != nil {
		return parsed{}, x
	}
	switch m.Type {
	case "stream_event":
		if m.Event.Type == "content_block_delta" && m.Event.Delta.Type == "text_delta" {
			x.delta = m.Event.Delta.Text
		}
		return parsed{}, x
	case "system":
		if m.Subtype == "init" {
			x.sessionID = m.SessionID
			x.mcpStatus = "not loaded"
			for _, s := range m.MCPServers {
				if s.Name == "kanivet" {
					x.mcpStatus = s.Status
				}
			}
		}
	}
	p := parseClaudeLine(line)
	if sawDelta {
		kept := p.events[:0]
		for _, e := range p.events {
			if e.Type != "text" {
				kept = append(kept, e)
			}
		}
		p.events = kept
	}
	return p, x
}

// codexExtras picks the thread id from `codex exec --json`.
func codexExtras(line []byte) chatExtras {
	var m struct {
		Type     string `json:"type"`
		ThreadID string `json:"thread_id"`
	}
	if json.Unmarshal(line, &m) == nil && m.Type == "thread.started" {
		return chatExtras{sessionID: m.ThreadID}
	}
	return chatExtras{}
}
