package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// RunStdioBridge serves MCP on stdin/stdout by relaying every message to the
// running backend's HTTP endpoint, for clients that only launch stdio
// servers (Claude Desktop). The port and token are read from mcp.json in
// stateDir: the port when the bridge starts, the token on every request, so
// rotating the token in Kanivet does not break a configured client.
func RunStdioBridge(ctx context.Context, stateDir string, full bool) error {
	return runBridge(ctx, filepath.Join(stateDir, "mcp.json"), full, &mcpsdk.StdioTransport{})
}

func runBridge(ctx context.Context, path string, full bool, localT mcpsdk.Transport) error {
	st, err := readSettingsFile(path)
	if err != nil {
		return err
	}
	if st.Port == 0 {
		return errors.New("kanivet is not running (no port in " + path + "); start Kanivet and retry")
	}
	endpoint := fmt.Sprintf("http://127.0.0.1:%d/mcp-readonly", st.Port)
	if full {
		endpoint = fmt.Sprintf("http://127.0.0.1:%d/mcp", st.Port)
	}
	httpc := &http.Client{Transport: tokenFromFile{path: path, base: http.DefaultTransport}}
	remote, err := (&mcpsdk.StreamableClientTransport{Endpoint: endpoint, HTTPClient: httpc}).Connect(ctx)
	if err != nil {
		return fmt.Errorf("connect %s: %w", endpoint, err)
	}
	defer remote.Close()
	local, err := localT.Connect(ctx)
	if err != nil {
		return err
	}
	defer local.Close()
	return relay(ctx, local, remote)
}

// relay copies messages both ways until either side ends.
func relay(ctx context.Context, a, b mcpsdk.Connection) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errc := make(chan error, 2)
	pump := func(from, to mcpsdk.Connection) {
		for {
			msg, err := from.Read(ctx)
			if err != nil {
				errc <- err
				return
			}
			if err := to.Write(ctx, msg); err != nil {
				errc <- err
				return
			}
		}
	}
	go pump(a, b)
	go pump(b, a)
	err := <-errc
	if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

type tokenFromFile struct {
	path string
	base http.RoundTripper
}

func (t tokenFromFile) RoundTrip(r *http.Request) (*http.Response, error) {
	st, err := readSettingsFile(t.path)
	if err != nil {
		return nil, err
	}
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+st.Token)
	return t.base.RoundTrip(r)
}

func readSettingsFile(path string) (Settings, error) {
	var st Settings
	b, err := os.ReadFile(path)
	if err != nil {
		return st, fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return st, fmt.Errorf("parse %s: %w", path, err)
	}
	return st, nil
}
