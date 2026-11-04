package investigate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// ---- Code locations: which local folders hold the source of what -------

// CodeLocation is a local folder (usually a git checkout) the user told
// Kanivet about.
type CodeLocation struct {
	Path    string   `json:"path"`
	Name    string   `json:"name"`
	Remotes []string `json:"remotes,omitempty"`
	// Charts are Helm chart names found in the folder (Chart.yaml).
	Charts []string `json:"charts,omitempty"`
}

// SourceHints describe where a resource's source might live, from GitOps
// and Helm metadata on the cluster.
type SourceHints struct {
	RepoURLs []string `json:"repoUrls,omitempty"` // Argo CD / Flux source URLs
	Paths    []string `json:"paths,omitempty"`    // Argo source path / Flux Kustomization path
	Charts   []string `json:"charts,omitempty"`   // Helm chart names
	Names    []string `json:"names,omitempty"`    // app / release / resource names
	Manager  string   `json:"manager,omitempty"`  // argocd | flux | helm | ""
}

// Suggestion is a ranked code location for a resource.
type Suggestion struct {
	Location CodeLocation `json:"location"`
	Score    int          `json:"score"`
	Reason   string       `json:"reason"`
	// SubPath is the folder inside the repo the hints point at, if known.
	SubPath string `json:"subPath,omitempty"`
}

// LocationStore persists code locations in ~/.kanivet/ai/code-locations.json.
type LocationStore struct {
	file string
	mu   sync.Mutex
}

// NewLocationStore uses file for storage.
func NewLocationStore(file string) *LocationStore { return &LocationStore{file: file} }

// List returns the saved locations.
func (s *LocationStore) List() []CodeLocation {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

func (s *LocationStore) load() []CodeLocation {
	b, err := os.ReadFile(s.file)
	if err != nil {
		return []CodeLocation{}
	}
	var out []CodeLocation
	if json.Unmarshal(b, &out) != nil || out == nil {
		return []CodeLocation{}
	}
	return out
}

func (s *LocationStore) save(l []CodeLocation) error {
	if err := os.MkdirAll(filepath.Dir(s.file), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(l, "", "  ")
	return os.WriteFile(s.file, b, 0o600)
}

// Add inspects dir (remotes, chart names) and saves it.
func (s *LocationStore) Add(dir string) (CodeLocation, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return CodeLocation{}, err
	}
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		return CodeLocation{}, fmt.Errorf("%s is not a folder", abs)
	}
	loc := InspectLocation(abs)
	s.mu.Lock()
	defer s.mu.Unlock()
	all := s.load()
	replaced := false
	for i := range all {
		if samePath(all[i].Path, abs) {
			all[i], replaced = loc, true
		}
	}
	if !replaced {
		all = append(all, loc)
	}
	return loc, s.save(all)
}

// Remove forgets dir.
func (s *LocationStore) Remove(dir string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	all := s.load()
	out := all[:0]
	for _, l := range all {
		if !samePath(l.Path, dir) {
			out = append(out, l)
		}
	}
	return s.save(out)
}

func samePath(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}

// InspectLocation reads a folder's git remotes and Helm chart names.
func InspectLocation(dir string) CodeLocation {
	loc := CodeLocation{Path: dir, Name: filepath.Base(dir)}
	if out, err := git(context.Background(), dir, "remote", "-v"); err == nil {
		seen := map[string]bool{}
		for _, l := range strings.Split(out, "\n") {
			f := strings.Fields(l)
			if len(f) >= 2 && !seen[f[1]] {
				seen[f[1]] = true
				loc.Remotes = append(loc.Remotes, f[1])
			}
		}
	}
	loc.Charts = findCharts(dir, 4)
	return loc
}

var chartName = regexp.MustCompile(`(?m)^name:\s*["']?([\w.-]+)`)

func findCharts(root string, maxDepth int) []string {
	var out []string
	seen := map[string]bool{}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		depth := len(strings.Split(rel, string(filepath.Separator)))
		if d.IsDir() {
			n := d.Name()
			if p != root && (strings.HasPrefix(n, ".") || n == "node_modules" || n == "vendor") || depth > maxDepth {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == "Chart.yaml" {
			if b, err := os.ReadFile(p); err == nil {
				if m := chartName.FindSubmatch(b); m != nil && !seen[string(m[1])] {
					seen[string(m[1])] = true
					out = append(out, string(m[1]))
				}
			}
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// NormalizeRepoURL maps the many spellings of a git URL to host/owner/repo:
// git@github.com:org/repo.git, https://github.com/org/repo, ssh://git@...
func NormalizeRepoURL(u string) string {
	u = strings.TrimSpace(strings.ToLower(u))
	u = strings.TrimSuffix(strings.TrimSuffix(u, "/"), ".git")
	for _, p := range []string{"git+ssh://", "ssh://", "https://", "http://", "git://"} {
		u = strings.TrimPrefix(u, p)
	}
	if i := strings.Index(u, "@"); i >= 0 && !strings.Contains(u[:i], "/") {
		u = u[i+1:]
	}
	// scp-like host:path, but not host:port/path
	if i := strings.Index(u, ":"); i >= 0 {
		rest := u[i+1:]
		if j := strings.Index(rest, "/"); j > 0 && isDigits(rest[:j]) {
			u = u[:i] + rest[j:]
		} else {
			u = u[:i] + "/" + rest
		}
	}
	return strings.Trim(u, "/")
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// Suggest ranks locations for a resource's hints: an exact git remote
// match beats a Helm chart name match, which beats a folder-name match.
func Suggest(locs []CodeLocation, h SourceHints) []Suggestion {
	want := map[string]bool{}
	for _, u := range h.RepoURLs {
		if n := NormalizeRepoURL(u); n != "" {
			want[n] = true
		}
	}
	var out []Suggestion
	for _, l := range locs {
		s := Suggestion{Location: l}
		for _, r := range l.Remotes {
			if want[NormalizeRepoURL(r)] {
				s.Score, s.Reason = 100, "git remote matches "+h.Manager+" source "+r
				if len(h.Paths) > 0 {
					s.SubPath = h.Paths[0]
				}
				break
			}
		}
		if s.Score == 0 {
			for _, c := range h.Charts {
				if containsFold(l.Charts, c) {
					s.Score, s.Reason = 60, "contains Helm chart "+c
					break
				}
			}
		}
		if s.Score == 0 {
			base := strings.ToLower(l.Name)
			for _, n := range append(append([]string{}, h.Charts...), h.Names...) {
				n = strings.ToLower(n)
				if len(n) >= 3 && (strings.Contains(base, n) || strings.Contains(n, base) && len(base) >= 3) {
					s.Score, s.Reason = 20, "folder name resembles "+n
					break
				}
			}
		}
		if s.Score > 0 {
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

// HintsFromObject extracts source hints from a resource's labels and
// annotations (Argo CD instance/tracking id, Flux, Helm).
func HintsFromObject(obj map[string]any) (h SourceHints, argoApp, fluxKs, fluxHR [2]string) {
	md, _ := obj["metadata"].(map[string]any)
	labels, _ := md["labels"].(map[string]any)
	ann, _ := md["annotations"].(map[string]any)
	ns, _ := md["namespace"].(string)
	name, _ := md["name"].(string)
	get := func(m map[string]any, k string) string { s, _ := m[k].(string); return s }
	if name != "" {
		h.Names = append(h.Names, name)
	}
	if v := get(labels, "argocd.argoproj.io/instance"); v != "" {
		argoApp = [2]string{"", v}
		h.Manager = "argocd"
	}
	if v := get(ann, "argocd.argoproj.io/tracking-id"); v != "" {
		// "<app>:<group>/<kind>:<ns>/<name>"; app may be "<ns>_<name>".
		app := strings.SplitN(v, ":", 2)[0]
		if i := strings.Index(app, "_"); i > 0 {
			argoApp = [2]string{app[:i], app[i+1:]}
		} else {
			argoApp = [2]string{"", app}
		}
		h.Manager = "argocd"
	}
	if v := get(labels, "kustomize.toolkit.fluxcd.io/name"); v != "" {
		fluxKs = [2]string{get(labels, "kustomize.toolkit.fluxcd.io/namespace"), v}
		h.Manager = "flux"
	}
	if v := get(labels, "helm.toolkit.fluxcd.io/name"); v != "" {
		fluxHR = [2]string{get(labels, "helm.toolkit.fluxcd.io/namespace"), v}
		h.Manager = "flux"
	}
	if v := get(labels, "helm.sh/chart"); v != "" {
		// name-1.2.3 -> name
		if i := strings.LastIndex(v, "-"); i > 0 && strings.ContainsAny(v[i+1:i+2], "0123456789v") {
			v = v[:i]
		}
		h.Charts = append(h.Charts, v)
		if h.Manager == "" {
			h.Manager = "helm"
		}
	}
	for _, k := range []string{"app.kubernetes.io/instance", "app.kubernetes.io/name", "app", "meta.helm.sh/release-name"} {
		if v := get(labels, k); v != "" {
			h.Names = append(h.Names, v)
		}
		if v := get(ann, k); v != "" {
			h.Names = append(h.Names, v)
		}
	}
	h.Names = dedupe(h.Names)
	_ = ns
	return
}

// HintsFromArgoApp adds repoURL/path/chart from an Argo CD Application.
func HintsFromArgoApp(h *SourceHints, app map[string]any) {
	spec, _ := app["spec"].(map[string]any)
	var sources []any
	if s, ok := spec["source"].(map[string]any); ok {
		sources = append(sources, s)
	}
	if ss, ok := spec["sources"].([]any); ok {
		sources = append(sources, ss...)
	}
	for _, s := range sources {
		sm, _ := s.(map[string]any)
		if u, _ := sm["repoURL"].(string); u != "" {
			h.RepoURLs = append(h.RepoURLs, u)
		}
		if p, _ := sm["path"].(string); p != "" {
			h.Paths = append(h.Paths, p)
		}
		if c, _ := sm["chart"].(string); c != "" {
			h.Charts = append(h.Charts, c)
		}
	}
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// ---- Git operations for the review view ---------------------------------

func git(ctx context.Context, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "core.quotepath=off"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(childEnv(nil), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return out.String(), fmt.Errorf("git %s: %s", args[0], msg)
	}
	return out.String(), nil
}

// RepoStatus is the state of a repo before a code fix starts.
type RepoStatus struct {
	Root      string   `json:"root"`
	Branch    string   `json:"branch"`
	HeadSHA   string   `json:"headSha"`
	Dirty     []string `json:"dirty"`     // tracked files with changes
	Untracked []string `json:"untracked"` // untracked files
}

// ErrNotGitRepo is returned when the folder is not a git checkout.
var ErrNotGitRepo = errors.New("not a git repository")

// GetRepoStatus inspects dir.
func GetRepoStatus(ctx context.Context, dir string) (*RepoStatus, error) {
	root, err := git(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, ErrNotGitRepo
	}
	st := &RepoStatus{Root: strings.TrimSpace(root), Dirty: []string{}, Untracked: []string{}}
	if b, err := git(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		st.Branch = strings.TrimSpace(b)
	}
	if h, err := git(ctx, dir, "rev-parse", "HEAD"); err == nil {
		st.HeadSHA = strings.TrimSpace(h)
	}
	out, err := git(ctx, st.Root, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	for _, rec := range splitZ(out) {
		if len(rec) < 4 {
			continue
		}
		code, path := rec[:2], rec[3:]
		if code == "??" {
			st.Untracked = append(st.Untracked, path)
		} else {
			st.Dirty = append(st.Dirty, path)
		}
	}
	return st, nil
}

func splitZ(s string) []string {
	var out []string
	parts := strings.Split(s, "\x00")
	for i := 0; i < len(parts); i++ {
		p := parts[i]
		if p == "" {
			continue
		}
		out = append(out, p)
		// renames: "R  new\x00old" -> skip the old path
		if len(p) > 1 && (p[0] == 'R' || p[0] == 'C') {
			i++
		}
	}
	return out
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// Slug makes a branch-safe slug.
func Slug(s string) string {
	s = strings.Trim(slugRe.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if len(s) > 40 {
		s = strings.Trim(s[:40], "-")
	}
	if s == "" {
		s = "change"
	}
	return s
}

// PrepareOpts says how to start a code fix.
type PrepareOpts struct {
	NewBranch  bool   // create kanivet/fix-<slug> (preferred)
	Slug       string // for the branch name
	AllowDirty bool   // user confirmed working on top of uncommitted changes
}

// ErrDirty is returned when the repo has uncommitted changes and the user
// has neither confirmed nor asked for a new branch.
var ErrDirty = errors.New("the repository has uncommitted changes")

// PrepareRepo checks the repo and, if asked, creates a fresh branch. It
// records what was dirty/untracked at the start so the review diff and
// Discard leave the user's own pending work alone.
func PrepareRepo(ctx context.Context, dir string, o PrepareOpts) (*CodeWork, *RepoStatus, error) {
	st, err := GetRepoStatus(ctx, dir)
	if err != nil {
		return nil, nil, err
	}
	if st.HeadSHA == "" {
		return nil, st, errors.New("the repository has no commits yet")
	}
	if (len(st.Dirty) > 0 || len(st.Untracked) > 0) && !o.AllowDirty {
		return nil, st, ErrDirty
	}
	w := &CodeWork{Repo: st.Root, Branch: st.Branch, BaseBranch: st.Branch, BaseSHA: st.HeadSHA,
		DirtyAtStart: st.Dirty, UntrackedAtStart: st.Untracked}
	if o.NewBranch {
		name := uniqueBranch(ctx, st.Root, "kanivet/fix-"+Slug(o.Slug))
		if _, err := git(ctx, st.Root, "switch", "-c", name); err != nil {
			return nil, st, err
		}
		w.Branch, w.CreatedBranch = name, true
	}
	return w, st, nil
}

func uniqueBranch(ctx context.Context, root, name string) string {
	cand := name
	for i := 2; i < 50; i++ {
		if _, err := git(ctx, root, "rev-parse", "--verify", "--quiet", "refs/heads/"+cand); err != nil {
			return cand
		}
		cand = fmt.Sprintf("%s-%d", name, i)
	}
	return cand
}

// FileDiff is one changed file in the review view.
type FileDiff struct {
	Path        string `json:"path"`
	Status      string `json:"status"` // modified | added | deleted
	Original    string `json:"original"`
	Modified    string `json:"modified"`
	Patch       string `json:"patch"`
	Preexisting bool   `json:"preexisting,omitempty"` // was already dirty before the agent started
	Binary      bool   `json:"binary,omitempty"`
}

// RepoDiff lists what changed in the repo since the work started.
func RepoDiff(ctx context.Context, w *CodeWork) ([]FileDiff, error) {
	pre := map[string]bool{}
	for _, p := range w.DirtyAtStart {
		pre[p] = true
	}
	untrackedBefore := map[string]bool{}
	for _, p := range w.UntrackedAtStart {
		untrackedBefore[p] = true
	}
	out, err := git(ctx, w.Repo, "diff", "--name-status", "-z", "--no-renames", w.BaseSHA, "--")
	if err != nil {
		return nil, err
	}
	var files []FileDiff
	parts := strings.Split(out, "\x00")
	for i := 0; i+1 < len(parts); i += 2 {
		st, path := parts[i], parts[i+1]
		if st == "" {
			break
		}
		fd := FileDiff{Path: path, Preexisting: pre[path]}
		switch st[0] {
		case 'A':
			fd.Status = "added"
		case 'D':
			fd.Status = "deleted"
		default:
			fd.Status = "modified"
		}
		files = append(files, fd)
	}
	st, err := GetRepoStatus(ctx, w.Repo)
	if err != nil {
		return nil, err
	}
	for _, p := range st.Untracked {
		if !untrackedBefore[p] {
			files = append(files, FileDiff{Path: p, Status: "added"})
		}
	}
	for i := range files {
		f := &files[i]
		if f.Status != "added" {
			f.Original, _ = git(ctx, w.Repo, "show", w.BaseSHA+":"+f.Path)
		}
		if f.Status != "deleted" {
			if b, err := os.ReadFile(filepath.Join(w.Repo, filepath.FromSlash(f.Path))); err == nil {
				f.Modified = string(b)
			}
		}
		if bytes.IndexByte([]byte(f.Original+f.Modified), 0) >= 0 {
			f.Binary, f.Original, f.Modified = true, "", ""
		}
		if p, err := git(ctx, w.Repo, "diff", w.BaseSHA, "--", f.Path); err == nil && p != "" {
			f.Patch = p
		} else if f.Status == "added" && !f.Binary {
			f.Patch = "+++ " + f.Path + "\n" + prefixLines(f.Modified, "+")
		}
	}
	sort.SliceStable(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

func prefixLines(s, p string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i := range lines {
		lines[i] = p + lines[i]
	}
	return strings.Join(lines, "\n") + "\n"
}

// DiscardChanges restores files the agent changed to the base commit and
// deletes files it created. Files that were already dirty before the
// agent started are left alone unless force is set. paths limits it to
// some files (nil = all).
func DiscardChanges(ctx context.Context, w *CodeWork, paths []string, force bool) ([]string, error) {
	files, err := RepoDiff(ctx, w)
	if err != nil {
		return nil, err
	}
	only := map[string]bool{}
	for _, p := range paths {
		only[p] = true
	}
	var done []string
	for _, f := range files {
		if len(only) > 0 && !only[f.Path] {
			continue
		}
		if f.Preexisting && !force {
			continue
		}
		if !insideRepo(w.Repo, f.Path) {
			return done, fmt.Errorf("refusing to touch %s outside the repository", f.Path)
		}
		if f.Status == "added" {
			// Untracked new file, or added to the index since base.
			_, _ = git(ctx, w.Repo, "rm", "--cached", "--quiet", "--ignore-unmatch", "--", f.Path)
			if err := os.Remove(filepath.Join(w.Repo, filepath.FromSlash(f.Path))); err != nil && !errors.Is(err, os.ErrNotExist) {
				return done, err
			}
		} else if _, err := git(ctx, w.Repo, "checkout", w.BaseSHA, "--", f.Path); err != nil {
			return done, err
		}
		done = append(done, f.Path)
	}
	return done, nil
}

func insideRepo(root, rel string) bool {
	p := filepath.Clean(filepath.Join(root, filepath.FromSlash(rel)))
	r, err := filepath.Rel(filepath.Clean(root), p)
	return err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)) && !filepath.IsAbs(r)
}

// CommitChanges creates the branch (if still on the base branch and asked)
// and commits the agent's files. Only ever called from an explicit click.
func CommitChanges(ctx context.Context, w *CodeWork, branch, message string) (string, error) {
	files, err := RepoDiff(ctx, w)
	if err != nil {
		return "", err
	}
	var paths []string
	for _, f := range files {
		if !f.Preexisting {
			paths = append(paths, f.Path)
		}
	}
	if len(paths) == 0 {
		return "", errors.New("nothing to commit")
	}
	if branch != "" && branch != w.Branch {
		if _, err := git(ctx, w.Repo, "switch", "-c", branch); err != nil {
			return "", err
		}
		w.Branch, w.CreatedBranch = branch, true
	}
	if _, err := git(ctx, w.Repo, append([]string{"add", "-A", "--"}, paths...)...); err != nil {
		return "", err
	}
	if strings.TrimSpace(message) == "" {
		message = "fix: changes proposed with Kanivet AI"
	}
	if _, err := git(ctx, w.Repo, append([]string{"commit", "-m", message, "--"}, paths...)...); err != nil {
		return "", err
	}
	sha, _ := git(ctx, w.Repo, "rev-parse", "HEAD")
	w.Committed = strings.TrimSpace(sha)
	return w.Committed, nil
}

// OpenInEditor opens a file (or the repo) in VS Code or Cursor via their
// CLI. When neither CLI is on PATH it returns a URL the UI can open.
func OpenInEditor(editor, repo, rel string) (url string, err error) {
	target := repo
	if rel != "" {
		if !insideRepo(repo, rel) {
			return "", errors.New("path outside the repository")
		}
		target = filepath.Join(repo, filepath.FromSlash(rel))
	}
	cands := []string{"code", "cursor"}
	if editor == "cursor" {
		cands = []string{"cursor", "code"}
	}
	for _, c := range cands {
		if p := lookPath(c); p != "" {
			cmd := exec.Command(p, "--reuse-window", repo, "--goto", target)
			cmd.Env = childEnv(nil)
			if err := cmd.Start(); err == nil {
				go func() { _ = cmd.Wait() }()
				return "", nil
			}
		}
	}
	scheme := "vscode"
	if editor == "cursor" {
		scheme = "cursor"
	}
	t := filepath.ToSlash(target)
	if !strings.HasPrefix(t, "/") {
		t = "/" + t
	}
	return scheme + "://file" + t, nil
}
