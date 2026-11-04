package mcp

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/kanivet/backend/internal/mcp/mcpreg"
)

// Settings is persisted to ~/.kanivet/mcp.json (mode 0600).
type Settings struct {
	Enabled     bool `json:"enabled"`
	AllowWrites bool `json:"allowWrites"`
	// AllowExec registers exec_pod (on /mcp only). Off by default: command
	// output is raw and can contain secrets.
	AllowExec bool   `json:"allowExec,omitempty"`
	Token     string `json:"token"`
	// Port is the backend's current port, for the stdio bridge.
	Port int `json:"port,omitempty"`
}

// Store owns the persisted settings, the live call log and the cluster the
// UI has open (the default target for tools called without `cluster`).
type Store struct {
	path string

	mu       sync.RWMutex
	s        Settings
	active   string
	calls    []mcpreg.Record
	lastCall time.Time
	clients  map[string]time.Time
}

const maxCalls = 300

// NewToken returns a fresh random bearer token.
func NewToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return "kmcp_" + hex.EncodeToString(b)
}

// OpenStore loads settings from dir/mcp.json, creating them (enabled, writes
// allowed, new token) on first run.
func OpenStore(dir string) (*Store, error) {
	st := &Store{path: filepath.Join(dir, "mcp.json"), clients: map[string]time.Time{}}
	data, err := os.ReadFile(st.path)
	if err == nil {
		_ = json.Unmarshal(data, &st.s)
	}
	if err != nil || st.s.Token == "" {
		if os.IsNotExist(err) || st.s.Token == "" {
			st.s = Settings{Enabled: true, AllowWrites: true, Token: NewToken()}
			if err := st.saveLocked(); err != nil {
				return st, err
			}
		}
	}
	return st, nil
}

func (st *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(st.path), 0o700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(st.s, "", "  ")
	tmp := st.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, st.path)
}

// Settings returns a copy of the current settings.
func (st *Store) Settings() Settings {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.s
}

// Update applies fn and persists the result.
func (st *Store) Update(fn func(*Settings)) (Settings, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	fn(&st.s)
	return st.s, st.saveLocked()
}

// SetActiveCluster records the cluster the UI shows.
func (st *Store) SetActiveCluster(c string) {
	st.mu.Lock()
	st.active = c
	st.mu.Unlock()
}

// ActiveCluster is the cluster the UI shows, if it told us.
func (st *Store) ActiveCluster() string {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.active
}

// Observe appends a call record (registered as the registry observer).
func (st *Store) Observe(r mcpreg.Record) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.calls = append(st.calls, r)
	if len(st.calls) > maxCalls {
		st.calls = append([]mcpreg.Record(nil), st.calls[len(st.calls)-maxCalls:]...)
	}
	st.lastCall = r.Time
	st.clients[r.Client] = r.Time
}

// Calls returns the most recent records, newest first.
func (st *Store) Calls(limit int) []mcpreg.Record {
	st.mu.RLock()
	defer st.mu.RUnlock()
	if limit <= 0 || limit > len(st.calls) {
		limit = len(st.calls)
	}
	out := make([]mcpreg.Record, 0, limit)
	for i := len(st.calls) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, st.calls[i])
	}
	return out
}

// Activity reports the last call time and the clients seen recently.
func (st *Store) Activity(window time.Duration) (time.Time, []string) {
	st.mu.RLock()
	defer st.mu.RUnlock()
	var names []string
	for n, t := range st.clients {
		if time.Since(t) < window {
			names = append(names, n)
		}
	}
	return st.lastCall, names
}
