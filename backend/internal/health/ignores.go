package health

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// IgnoreStore keeps audit ignores in a JSON file under ~/.kanivet, so they
// survive restarts and stay on this machine.
type IgnoreStore struct {
	path string
	mu   sync.Mutex
	list []Ignore
}

// NewIgnoreStore loads the store at path; a missing file is an empty store.
// An empty path keeps ignores in memory only.
func NewIgnoreStore(path string) *IgnoreStore {
	s := &IgnoreStore{path: path}
	if path != "" {
		if b, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(b, &s.list)
		}
	}
	return s
}

func (s *IgnoreStore) List(cluster string) []Ignore {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Ignore{}
	for _, ig := range s.list {
		if ig.Cluster == cluster {
			out = append(out, ig)
		}
	}
	return out
}

func sameIgnore(a, b Ignore) bool {
	return a.Cluster == b.Cluster && a.CheckID == b.CheckID && a.Namespace == b.Namespace && a.Kind == b.Kind && a.Name == b.Name
}

func (s *IgnoreStore) Add(ig Ignore) error {
	if ig.Cluster == "" || ig.CheckID == "" {
		return errors.New("cluster and checkId are required")
	}
	if ig.Name != "" && ig.Kind == "" {
		return errors.New("kind is required with name")
	}
	if ig.CreatedAt.IsZero() {
		ig.CreatedAt = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, cur := range s.list {
		if sameIgnore(cur, ig) {
			return nil
		}
	}
	s.list = append(s.list, ig)
	return s.save()
}

func (s *IgnoreStore) Remove(ig Ignore) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.list[:0]
	for _, cur := range s.list {
		if !sameIgnore(cur, ig) {
			out = append(out, cur)
		}
	}
	s.list = out
	return s.save()
}

func (s *IgnoreStore) save() error {
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.list, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Matcher returns whether a finding of the cluster is ignored.
func (s *IgnoreStore) Matcher(cluster string) func(Finding) bool {
	list := s.List(cluster)
	return func(f Finding) bool {
		for _, ig := range list {
			if ig.CheckID != f.CheckID && ig.CheckID != "*" {
				continue
			}
			switch {
			case ig.Namespace == "" && ig.Name == "":
				return true
			case ig.Name == "" && ig.Namespace == f.Resource.Namespace:
				return true
			case ig.Name == f.Resource.Name && ig.Kind == f.Resource.Kind && ig.Namespace == f.Resource.Namespace:
				return true
			}
		}
		return false
	}
}
