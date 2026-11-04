package install

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// writeFileSafely backs up an existing file to <path>.bak-kanivet-<ts>, then
// replaces it atomically (temp file in the same directory + rename), keeping
// its mode. New files get mode 0600, since they hold the bearer token.
func writeFileSafely(path string, data []byte, now time.Time) (backup string, err error) {
	mode := fs.FileMode(0o600)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
		old, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		backup = fmt.Sprintf("%s.bak-kanivet-%s", path, now.UTC().Format("20060102T150405Z"))
		if err := os.WriteFile(backup, old, mode); err != nil {
			return "", fmt.Errorf("backup %s: %w", backup, err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return backup, err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".kanivet-*")
	if err != nil {
		return backup, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return backup, err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return backup, err
	}
	if err := tmp.Close(); err != nil {
		return backup, err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return backup, err
	}
	return backup, os.Rename(tmpName, path)
}

// readJSONObject reads a JSON object file. A missing or empty file is an
// empty object. Comments (JSONC) are not supported: such a file is refused
// rather than rewritten without them.
func readJSONObject(path string) (map[string]any, bool, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]any{}, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return map[string]any{}, true, nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, true, fmt.Errorf("%s is not plain JSON (comments or a syntax error?); add the entry by hand: %w", path, err)
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, true, nil
}

func marshalJSON(m map[string]any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// editJSONEntry sets (or, with entry nil, deletes) m[section][ServerName]
// and writes the file back, leaving every other key untouched.
func editJSONEntry(path, section string, entry map[string]any, now time.Time) error {
	m, exists, err := readJSONObject(path)
	if err != nil {
		return err
	}
	sec, _ := m[section].(map[string]any)
	if sec == nil {
		if _, present := m[section]; present {
			return fmt.Errorf("%s: %q is not an object", path, section)
		}
		sec = map[string]any{}
	}
	if entry == nil {
		if _, ok := sec[ServerName]; !ok {
			return nil
		}
		delete(sec, ServerName)
	} else {
		sec[ServerName] = entry
	}
	m[section] = sec
	if !exists && entry == nil {
		return nil
	}
	data, err := marshalJSON(m)
	if err != nil {
		return err
	}
	_, err = writeFileSafely(path, data, now)
	return err
}

// jsonEntry returns m[section][ServerName].
func jsonEntry(path, section string) (map[string]any, error) {
	m, _, err := readJSONObject(path)
	if err != nil {
		return nil, err
	}
	sec, _ := m[section].(map[string]any)
	e, _ := sec[ServerName].(map[string]any)
	return e, nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}
