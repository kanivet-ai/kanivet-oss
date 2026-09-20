package cloud

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// CLILoginJob tracks a cloud CLI sign-in (`gcloud auth login`, `az login`,
// `aws sso login --profile …`) that Kanivet spawned on the user's behalf. The
// CLI owns the browser flow; Kanivet only relays progress so the UI can show
// "waiting for browser", surface a device code, or cancel.
type CLILoginJob struct {
	ID         string   `json:"id"`
	Provider   Provider `json:"provider"`
	Label      string   `json:"label"`
	Command    string   `json:"command"`
	State      string   `json:"state"` // running | succeeded | failed | cancelled
	URL        string   `json:"url,omitempty"`
	Code       string   `json:"code,omitempty"`
	Output     string   `json:"output,omitempty"`
	Error      string   `json:"error,omitempty"`
	StartedAt  int64    `json:"startedAt"`
	FinishedAt int64    `json:"finishedAt,omitempty"`

	cancel context.CancelFunc
	lines  []string
}

const (
	cliJobRunning   = "running"
	cliJobSucceeded = "succeeded"
	cliJobFailed    = "failed"
	cliJobCancelled = "cancelled"
)

func (j *CLILoginJob) snapshot() *CLILoginJob {
	c := *j
	c.cancel = nil
	c.lines = nil
	return &c
}

type cliLoginManager struct {
	mu     sync.Mutex
	byID   map[string]*CLILoginJob
	byKey  map[string]string
	onDone func(job *CLILoginJob)
}

func newCLILoginManager() *cliLoginManager {
	return &cliLoginManager{byID: make(map[string]*CLILoginJob), byKey: make(map[string]string)}
}

var (
	// az: "To sign in, use a web browser to open the page https://microsoft.com/devicelogin and enter the code ABCDEFGH to authenticate."
	azDeviceRe = regexp.MustCompile(`open the page (https?://\S+) and enter the code ([A-Z0-9-]+)`)
	// aws sso login (device flow): a bare URL line followed by "Then enter the code:" and the code.
	awsCodeRe = regexp.MustCompile(`^[A-Z0-9]{4}-[A-Z0-9]{4}$`)
	urlRe     = regexp.MustCompile(`https?://[^\s'"<>]+`)
)

func (m *cliLoginManager) get(id string) (*CLILoginJob, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.byID[id]
	if !ok {
		return nil, false
	}
	return j.snapshot(), true
}

func (m *cliLoginManager) running(key string) (*CLILoginJob, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.byKey[key]
	if !ok {
		return nil, false
	}
	j := m.byID[id]
	if j == nil || j.State != cliJobRunning {
		return nil, false
	}
	return j.snapshot(), true
}

func (m *cliLoginManager) cancel(id string) bool {
	m.mu.Lock()
	j, ok := m.byID[id]
	m.mu.Unlock()
	if !ok {
		return false
	}
	if j.State == cliJobRunning && j.cancel != nil {
		j.cancel()
	}
	return true
}

// start launches name args… and returns immediately. key dedupes concurrent
// logins for the same provider/profile.
func (m *cliLoginManager) start(key string, provider Provider, label, name string, args []string, env []string) (*CLILoginJob, error) {
	if existing, ok := m.running(key); ok {
		return existing, nil
	}
	path, ok := lookPath(name)
	if !ok {
		return nil, &CLIMissingError{Binary: name}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = env
	cmd.Stdin = nil
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to start %s: %w", name, err)
	}

	job := &CLILoginJob{
		ID:        uuid.NewString(),
		Provider:  provider,
		Label:     label,
		Command:   strings.TrimSpace(name + " " + strings.Join(args, " ")),
		State:     cliJobRunning,
		StartedAt: time.Now().UnixMilli(),
		cancel:    cancel,
	}
	m.mu.Lock()
	m.byID[job.ID] = job
	m.byKey[key] = job.ID
	m.mu.Unlock()

	var wg sync.WaitGroup
	consume := func(r io.Reader) {
		defer wg.Done()
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			m.appendLine(job.ID, scanner.Text())
		}
	}
	wg.Add(2)
	go consume(stdout)
	go consume(stderr)

	go func() {
		wg.Wait()
		waitErr := cmd.Wait()
		cancel()
		m.mu.Lock()
		j := m.byID[job.ID]
		j.FinishedAt = time.Now().UnixMilli()
		switch {
		case ctx.Err() == context.Canceled:
			j.State = cliJobCancelled
		case ctx.Err() == context.DeadlineExceeded:
			j.State = cliJobFailed
			j.Error = "Sign-in timed out."
		case waitErr != nil:
			j.State = cliJobFailed
			j.Error = summarizeCLIError(j.lines, waitErr)
		default:
			j.State = cliJobSucceeded
		}
		j.Output = strings.Join(tail(j.lines, 30), "\n")
		done := j.snapshot()
		m.mu.Unlock()
		log.Printf("[CloudLogin] %s finished: %s", job.Command, done.State)
		if m.onDone != nil {
			m.onDone(done)
		}
	}()

	return job.snapshot(), nil
}

func (m *cliLoginManager) appendLine(id, line string) {
	line = strings.TrimRight(line, "\r")
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.byID[id]
	if !ok {
		return
	}
	j.lines = append(j.lines, line)
	if len(j.lines) > 200 {
		j.lines = j.lines[len(j.lines)-200:]
	}
	j.Output = strings.Join(tail(j.lines, 30), "\n")
	if mm := azDeviceRe.FindStringSubmatch(line); mm != nil {
		j.URL, j.Code = mm[1], mm[2]
		return
	}
	trimmed := strings.TrimSpace(line)
	if awsCodeRe.MatchString(trimmed) {
		j.Code = trimmed
		return
	}
	if j.URL == "" {
		if u := urlRe.FindString(line); u != "" && !strings.Contains(u, "aws.amazon.com/cli") && !strings.Contains(u, "cloud.google.com/sdk/docs") {
			j.URL = strings.TrimRight(u, ".,)")
		}
	}
}

func tail(lines []string, n int) []string {
	if len(lines) <= n {
		return lines
	}
	return lines[len(lines)-n:]
}

func summarizeCLIError(lines []string, waitErr error) string {
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if l == "" {
			continue
		}
		lower := strings.ToLower(l)
		if strings.HasPrefix(lower, "error") || strings.Contains(lower, "failed") || strings.Contains(lower, "denied") || strings.Contains(lower, "aadsts") {
			return l
		}
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l
		}
	}
	return waitErr.Error()
}

// CLIMissingError reports a cloud CLI Kanivet needed but could not find.
type CLIMissingError struct {
	Binary string
}

func (e *CLIMissingError) Error() string {
	return fmt.Sprintf("%s is not installed or not on PATH", e.Binary)
}

func cliInstallHint(binary string) string {
	switch binary {
	case "aws":
		return "Install the AWS CLI v2: https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html"
	case "gcloud":
		return "Install the Google Cloud CLI: https://cloud.google.com/sdk/docs/install"
	case "gke-gcloud-auth-plugin":
		return "Install the GKE auth plugin: gcloud components install gke-gcloud-auth-plugin (or brew install gke-gcloud-auth-plugin)"
	case "az":
		return "Install the Azure CLI: https://learn.microsoft.com/cli/azure/install-azure-cli"
	case "kubelogin":
		return "Install kubelogin: az aks install-cli (or brew install Azure/kubelogin/kubelogin)"
	}
	return ""
}
