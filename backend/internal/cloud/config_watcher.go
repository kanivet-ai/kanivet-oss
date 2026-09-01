package cloud

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	defaultConfigWatchInterval = 2 * time.Second
	defaultConfigWatchDebounce = 1500 * time.Millisecond
)

type ConfigWatcher struct {
	paths    []string
	interval time.Duration
	debounce time.Duration
	onChange func(reason string)
}

func NewConfigWatcher(paths []string, onChange func(reason string)) *ConfigWatcher {
	deduped := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		cleaned := filepath.Clean(path)
		if _, ok := seen[cleaned]; ok {
			continue
		}
		seen[cleaned] = struct{}{}
		deduped = append(deduped, cleaned)
	}

	return &ConfigWatcher{
		paths:    deduped,
		interval: defaultConfigWatchInterval,
		debounce: defaultConfigWatchDebounce,
		onChange: onChange,
	}
}

func DefaultConfigWatchPaths() []string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}

	paths := []string{
		filepath.Join(home, ".aws", "config"),
		filepath.Join(home, ".aws", "credentials"),
		filepath.Join(home, ".aws", "sso", "cache"),
		filepath.Join(home, ".kube"),
		KanivetKubeconfigPath(),
	}

	if kubeconfigEnv := os.Getenv("KUBECONFIG"); kubeconfigEnv != "" {
		separator := ":"
		if strings.Contains(kubeconfigEnv, ";") {
			separator = ";"
		}
		for _, path := range strings.Split(kubeconfigEnv, separator) {
			path = strings.TrimSpace(path)
			if path != "" {
				paths = append(paths, path)
			}
		}
	}

	return paths
}

func (w *ConfigWatcher) Start(ctx context.Context) {
	if len(w.paths) == 0 || w.onChange == nil {
		return
	}

	snapshots := make(map[string]string, len(w.paths))
	for _, path := range w.paths {
		snapshots[path] = snapshotPath(path)
	}

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	var pending bool
	var pendingReason string
	var changedAt time.Time

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, path := range w.paths {
				next := snapshotPath(path)
				if next == snapshots[path] {
					continue
				}
				log.Printf("[CloudConfigWatcher] Detected change in %s", path)
				snapshots[path] = next
				pending = true
				pendingReason = path
				changedAt = time.Now()
			}

			if pending && time.Since(changedAt) >= w.debounce {
				pending = false
				reason := pendingReason
				if reason == "" {
					reason = "external_config_change"
				}
				w.onChange(reason)
			}
		}
	}
}

func snapshotPath(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "missing"
		}
		return "error:" + err.Error()
	}

	if !info.IsDir() {
		return fmt.Sprintf("file:%d:%d", info.Size(), info.ModTime().UnixNano())
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return "dir-error:" + err.Error()
	}

	parts := make([]string, 0, len(entries))
	for _, entry := range entries {
		entryInfo, err := entry.Info()
		if err != nil {
			parts = append(parts, entry.Name()+":error")
			continue
		}
		kind := "f"
		if entryInfo.IsDir() {
			kind = "d"
		}
		parts = append(parts, fmt.Sprintf("%s:%s:%d:%d", kind, entry.Name(), entryInfo.Size(), entryInfo.ModTime().UnixNano()))
	}
	sort.Strings(parts)

	return "dir:" + strings.Join(parts, "|")
}
