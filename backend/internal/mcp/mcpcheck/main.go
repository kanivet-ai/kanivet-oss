// Command mcpcheck performs a real MCP handshake against a running Kanivet
// backend: initialize, tools/list and one tool call.
//
//	go run ./internal/mcp/mcpcheck -url http://127.0.0.1:53727/mcp-readonly -token "$(jq -r .token ~/.kanivet/mcp.json)" -call list_clusters
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

func main() {
	url := flag.String("url", "http://127.0.0.1:53727/mcp-readonly", "endpoint")
	token := flag.String("token", "", "bearer token")
	call := flag.String("call", "list_clusters", "tool to call")
	args := flag.String("args", "{}", "tool arguments (JSON)")
	tokenFile := flag.String("tokenfile", "", "read the token from this mcp.json (e.g. ~/.kanivet/mcp.json)")
	flag.Parse()
	if *token == "" && *tokenFile != "" {
		var st struct {
			Token string `json:"token"`
		}
		if b, err := os.ReadFile(*tokenFile); err == nil {
			_ = json.Unmarshal(b, &st)
		}
		*token = st.Token
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cs, err := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "mcpcheck", Version: "1"}, nil).Connect(ctx,
		&mcpsdk.StreamableClientTransport{Endpoint: *url, HTTPClient: &http.Client{Transport: bearer{*token}}, MaxRetries: -1}, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "connect:", err)
		os.Exit(1)
	}
	defer cs.Close()
	ir := cs.InitializeResult()
	fmt.Printf("initialize: server=%s %s protocol=%s\n", ir.ServerInfo.Name, ir.ServerInfo.Version, ir.ProtocolVersion)
	tl, err := cs.ListTools(ctx, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tools/list:", err)
		os.Exit(1)
	}
	fmt.Printf("tools/list: %d tools:", len(tl.Tools))
	for _, t := range tl.Tools {
		fmt.Print(" ", t.Name)
	}
	fmt.Println()
	var a map[string]any
	_ = json.Unmarshal([]byte(*args), &a)
	res, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{Name: *call, Arguments: a})
	if err != nil {
		fmt.Fprintln(os.Stderr, "tools/call:", err)
		os.Exit(1)
	}
	for _, c := range res.Content {
		if t, ok := c.(*mcpsdk.TextContent); ok {
			s := t.Text
			if len(s) > 1500 {
				s = s[:1500] + "…"
			}
			fmt.Printf("tools/call %s (isError=%v, %d bytes):\n%s\n", *call, res.IsError, len(t.Text), s)
		}
	}
}
