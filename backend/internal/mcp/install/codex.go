package install

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// codexServer is Codex's [mcp_servers.<name>] table for a streamable HTTP
// server.
type codexServer struct {
	URL     string            `toml:"url"`
	Headers map[string]string `toml:"http_headers,omitempty"`
}

// readCodexEntry returns mcp_servers.kanivet from config.toml (nil if absent).
func readCodexEntry(path string) (map[string]any, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if _, err := toml.Decode(string(b), &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	servers, _ := doc["mcp_servers"].(map[string]any)
	e, _ := servers[ServerName].(map[string]any)
	return e, nil
}

// isKanivetHeader reports whether a TOML table header line belongs to our
// entry: [mcp_servers.kanivet] or a sub-table of it.
func isKanivetHeader(line string) bool {
	l := strings.TrimSpace(line)
	if !strings.HasPrefix(l, "[") || strings.HasPrefix(l, "[[") {
		return false
	}
	end := strings.Index(l, "]")
	if end < 0 {
		return false
	}
	name := strings.ReplaceAll(strings.TrimSpace(l[1:end]), " ", "")
	for _, prefix := range []string{"mcp_servers." + ServerName, `mcp_servers."` + ServerName + `"`} {
		if name == prefix || strings.HasPrefix(name, prefix+".") {
			return true
		}
	}
	return false
}

func isHeader(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "[")
}

// editCodex replaces (or, with srv nil, removes) the kanivet table. Every
// other line of the file is kept byte for byte, comments included; the
// result is parsed again and compared with the original to be sure nothing
// else changed before it is written.
func editCodex(path string, srv *codexServer, now time.Time) error {
	orig, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	var before map[string]any
	if _, err := toml.Decode(string(orig), &before); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if servers, _ := before["mcp_servers"].(map[string]any); servers != nil {
		if _, inline := servers[ServerName]; inline && !strings.Contains(string(orig), "mcp_servers."+ServerName) && !strings.Contains(string(orig), `mcp_servers."`+ServerName+`"`) {
			return fmt.Errorf("%s defines mcp_servers.%s inline; edit it by hand", path, ServerName)
		}
	}

	var kept []string
	skipping := false
	for _, line := range strings.SplitAfter(string(orig), "\n") {
		if isHeader(line) {
			skipping = isKanivetHeader(line)
		}
		if !skipping {
			kept = append(kept, line)
		}
	}
	out := strings.Join(kept, "")
	if srv != nil {
		var buf bytes.Buffer
		buf.WriteString("[mcp_servers." + ServerName + "]\n")
		if err := toml.NewEncoder(&buf).Encode(srv); err != nil {
			return err
		}
		block := buf.String()
		// The encoder writes http_headers as its own table; keep it under ours.
		block = strings.Replace(block, "\n[http_headers]", "\n[mcp_servers."+ServerName+".http_headers]", 1)
		out = strings.TrimRight(out, "\n")
		if out != "" {
			out += "\n\n"
		}
		out += block
	} else if out == string(orig) {
		return nil
	}

	var after map[string]any
	if _, err := toml.Decode(out, &after); err != nil {
		return fmt.Errorf("refusing to write %s: result does not parse: %w", path, err)
	}
	strip := func(m map[string]any) map[string]any {
		c := map[string]any{}
		for k, v := range m {
			c[k] = v
		}
		if s, ok := c["mcp_servers"].(map[string]any); ok {
			sc := map[string]any{}
			for k, v := range s {
				if k != ServerName {
					sc[k] = v
				}
			}
			if len(sc) == 0 {
				delete(c, "mcp_servers")
			} else {
				c["mcp_servers"] = sc
			}
		}
		return c
	}
	if !reflect.DeepEqual(strip(before), strip(after)) {
		return fmt.Errorf("refusing to write %s: the edit would change other settings", path)
	}
	if srv != nil {
		servers, _ := after["mcp_servers"].(map[string]any)
		got, _ := servers[ServerName].(map[string]any)
		if got["url"] != srv.URL {
			return fmt.Errorf("refusing to write %s: entry did not round-trip", path)
		}
	}
	_, err = writeFileSafely(path, []byte(out), now)
	return err
}
