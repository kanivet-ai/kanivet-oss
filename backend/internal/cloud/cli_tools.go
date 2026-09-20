package cloud

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// extraCLIPaths are locations where cloud CLIs commonly live but which a GUI
// process launched from Finder/Dock may not have on PATH.
func extraCLIPaths() []string {
	paths := []string{"/usr/local/bin", "/opt/homebrew/bin", "/usr/bin", "/snap/bin", "/opt/az/bin",
		"/usr/local/Caskroom/google-cloud-sdk/latest/google-cloud-sdk/bin",
		"/opt/homebrew/Caskroom/google-cloud-sdk/latest/google-cloud-sdk/bin",
		"/opt/homebrew/share/google-cloud-sdk/bin",
		"/usr/local/share/google-cloud-sdk/bin",
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		paths = append(paths,
			filepath.Join(home, "google-cloud-sdk", "bin"),
			filepath.Join(home, ".local", "bin"),
			filepath.Join(home, "bin"),
			filepath.Join(home, ".krew", "bin"),
		)
	}
	return paths
}

// augmentedPATH returns PATH with the extra CLI locations appended.
func augmentedPATH() string {
	current := os.Getenv("PATH")
	parts := strings.Split(current, string(os.PathListSeparator))
	seen := make(map[string]struct{}, len(parts))
	for _, p := range parts {
		seen[p] = struct{}{}
	}
	for _, p := range extraCLIPaths() {
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		parts = append(parts, p)
	}
	return strings.Join(parts, string(os.PathListSeparator))
}

// lookPath finds a binary on the augmented PATH.
func lookPath(name string) (string, bool) {
	if path, err := exec.LookPath(name); err == nil {
		return path, true
	}
	for _, dir := range extraCLIPaths() {
		candidate := filepath.Join(dir, name)
		if runtime.GOOS == "windows" {
			candidate += ".exe"
		}
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, true
		}
	}
	return "", false
}

// cliEnv is the environment for spawned cloud CLIs: the user's environment
// with the augmented PATH so the same binaries the terminal uses are found.
func cliEnv(extra ...string) []string {
	env := os.Environ()
	filtered := make([]string, 0, len(env)+len(extra)+1)
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			continue
		}
		filtered = append(filtered, kv)
	}
	filtered = append(filtered, "PATH="+augmentedPATH())
	return append(filtered, extra...)
}

// openBrowserURL opens url in the user's default browser without blocking.
func openBrowserURL(url string) error {
	if url == "" {
		return fmt.Errorf("empty url")
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
