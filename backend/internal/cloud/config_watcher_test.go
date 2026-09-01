package cloud

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestConfigWatcherDetectsFileChange(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config")
	if err := os.WriteFile(configPath, []byte("initial"), 0o600); err != nil {
		t.Fatalf("write initial config: %v", err)
	}

	changes := make(chan string, 1)
	watcher := NewConfigWatcher([]string{configPath}, func(reason string) {
		changes <- reason
	})
	watcher.interval = 10 * time.Millisecond
	watcher.debounce = 20 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go watcher.Start(ctx)

	time.Sleep(20 * time.Millisecond)
	if err := os.WriteFile(configPath, []byte("updated"), 0o600); err != nil {
		t.Fatalf("update config: %v", err)
	}

	select {
	case reason := <-changes:
		if reason != configPath {
			t.Fatalf("expected changed path %q, got %q", configPath, reason)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for config change")
	}
}

func TestConfigWatcherDetectsDirectoryEntryChange(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, "cache")
	if err := os.Mkdir(cacheDir, 0o700); err != nil {
		t.Fatalf("create cache dir: %v", err)
	}

	changes := make(chan string, 1)
	watcher := NewConfigWatcher([]string{cacheDir}, func(reason string) {
		changes <- reason
	})
	watcher.interval = 10 * time.Millisecond
	watcher.debounce = 20 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go watcher.Start(ctx)

	time.Sleep(20 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(cacheDir, "token.json"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("write cache file: %v", err)
	}

	select {
	case reason := <-changes:
		if reason != cacheDir {
			t.Fatalf("expected changed path %q, got %q", cacheDir, reason)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for directory change")
	}
}
