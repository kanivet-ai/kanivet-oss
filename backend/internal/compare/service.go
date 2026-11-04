package compare

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	sigsyaml "sigs.k8s.io/yaml"
)

const (
	helmReleaseNameAnnotation      = "meta.helm.sh/release-name"
	helmReleaseNamespaceAnnotation = "meta.helm.sh/release-namespace"
)

// Clients is what the service needs from the Kubernetes layer.
type Clients interface {
	GetInteractiveDynamicClient(cluster string) (dynamic.Interface, error)
	GetResourceName(cluster, group, version, kind string) string
}

// HelmManifests returns a Helm release's rendered manifest.
type HelmManifests interface {
	GetReleaseManifest(ctx context.Context, cluster, namespace, name string) (string, error)
}

// Service compares objects.
type Service struct {
	clients Clients
	helm    HelmManifests
	// fetchLive is replaceable in tests.
	fetchLive func(ctx context.Context, ref Ref) (map[string]any, error)
}

// NewService builds a Service. helm may be nil (Helm drift then reports
// that no release is available).
func NewService(clients Clients, helm HelmManifests) *Service {
	s := &Service{clients: clients, helm: helm}
	s.fetchLive = s.getLive
	return s
}

// SideError is returned for a side that could not be read.
type SideError struct {
	Status int
	Err    error
}

func (e *SideError) Error() string { return e.Err.Error() }

func (s *Service) getLive(ctx context.Context, ref Ref) (map[string]any, error) {
	if ref.Cluster == "" || ref.Kind == "" || ref.Name == "" || ref.Version == "" {
		return nil, &SideError{Status: http.StatusBadRequest, Err: errors.New("cluster, version, kind and name are required")}
	}
	dc, err := s.clients.GetInteractiveDynamicClient(ref.Cluster)
	if err != nil {
		return nil, &SideError{Status: http.StatusInternalServerError, Err: err}
	}
	gvr := schema.GroupVersionResource{Group: ref.Group, Version: ref.Version, Resource: s.clients.GetResourceName(ref.Cluster, ref.Group, ref.Version, ref.Kind)}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var ri dynamic.ResourceInterface = dc.Resource(gvr)
	if ref.Namespace != "" {
		ri = dc.Resource(gvr).Namespace(ref.Namespace)
	}
	obj, err := ri.Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		switch {
		case apierrors.IsNotFound(err):
			where := ref.Cluster
			if ref.Namespace != "" {
				where = ref.Namespace + " on " + ref.Cluster
			}
			return nil, &SideError{Status: http.StatusNotFound, Err: fmt.Errorf("%s %s not found in %s", ref.Kind, ref.Name, where)}
		case apierrors.IsForbidden(err):
			return nil, &SideError{Status: http.StatusForbidden, Err: fmt.Errorf("not allowed to read %s %s on %s", ref.Kind, ref.Name, ref.Cluster)}
		}
		return nil, &SideError{Status: http.StatusInternalServerError, Err: err}
	}
	return obj.Object, nil
}

// Baselines reports which declared versions exist for the live object ref.
func (s *Service) Baselines(ctx context.Context, ref Ref) (Baselines, error) {
	live, err := s.fetchLive(ctx, ref)
	if err != nil {
		return Baselines{}, err
	}
	return baselinesOf(live, ref, s.helm != nil), nil
}

func baselinesOf(live map[string]any, ref Ref, haveHelm bool) Baselines {
	ann, _ := mapAt(live, "metadata")["annotations"].(map[string]any)
	var b Baselines
	if v, ok := ann[lastAppliedAnnotation].(string); ok && strings.TrimSpace(v) != "" {
		b.LastApplied = true
	}
	if rel, ok := ann[helmReleaseNameAnnotation].(string); ok && rel != "" && haveHelm {
		b.HelmRelease = rel
		b.HelmNamespace, _ = ann[helmReleaseNamespaceAnnotation].(string)
		if b.HelmNamespace == "" {
			b.HelmNamespace = ref.Namespace
		}
	}
	return b
}

// load returns the object a ref names, unnormalized.
func (s *Service) load(ctx context.Context, ref Ref) (map[string]any, error) {
	live, err := s.fetchLive(ctx, ref)
	if err != nil {
		return nil, err
	}
	switch ref.source() {
	case SourceLive:
		return live, nil
	case SourceLastApplied:
		ann, _ := mapAt(live, "metadata")["annotations"].(map[string]any)
		raw, _ := ann[lastAppliedAnnotation].(string)
		if strings.TrimSpace(raw) == "" {
			return nil, &SideError{Status: http.StatusNotFound, Err: fmt.Errorf("%s %s has no last-applied-configuration (it was not created with kubectl apply)", ref.Kind, ref.Name)}
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(raw), &obj); err != nil {
			return nil, &SideError{Status: http.StatusInternalServerError, Err: fmt.Errorf("last-applied-configuration is not valid JSON: %w", err)}
		}
		return obj, nil
	case SourceHelm:
		b := baselinesOf(live, ref, s.helm != nil)
		if b.HelmRelease == "" {
			return nil, &SideError{Status: http.StatusNotFound, Err: fmt.Errorf("%s %s is not managed by a Helm release", ref.Kind, ref.Name)}
		}
		manifest, err := s.helm.GetReleaseManifest(ctx, ref.Cluster, b.HelmNamespace, b.HelmRelease)
		if err != nil {
			return nil, &SideError{Status: http.StatusInternalServerError, Err: fmt.Errorf("reading Helm release %s: %w", b.HelmRelease, err)}
		}
		obj := FindInManifest(manifest, ref.Kind, ref.Name, ref.Namespace)
		if obj == nil {
			return nil, &SideError{Status: http.StatusNotFound, Err: fmt.Errorf("%s %s is not in the manifest of Helm release %s", ref.Kind, ref.Name, b.HelmRelease)}
		}
		return obj, nil
	}
	return nil, &SideError{Status: http.StatusBadRequest, Err: fmt.Errorf("unknown source %q", ref.Source)}
}

// FindInManifest returns the document of a multi-document YAML manifest that
// declares kind/name. A document without a namespace matches any namespace,
// since Helm fills it in at install time.
func FindInManifest(manifest, kind, name, namespace string) map[string]any {
	for _, doc := range splitDocs(manifest) {
		var obj map[string]any
		if err := sigsyaml.Unmarshal([]byte(doc), &obj); err != nil || obj == nil {
			continue
		}
		if str(obj["kind"]) != kind {
			continue
		}
		md := mapAt(obj, "metadata")
		if str(md["name"]) != name {
			continue
		}
		if ns := str(md["namespace"]); ns != "" && namespace != "" && ns != namespace {
			continue
		}
		return obj
	}
	return nil
}

func splitDocs(manifest string) []string {
	var docs []string
	var cur []string
	for _, line := range strings.Split(strings.ReplaceAll(manifest, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(line, "---") {
			docs = append(docs, strings.Join(cur, "\n"))
			cur = nil
			continue
		}
		cur = append(cur, line)
	}
	docs = append(docs, strings.Join(cur, "\n"))
	return docs
}

// Label is a short human name for a ref.
func Label(r Ref) string {
	l := r.Kind + " " + r.Name
	if r.Namespace != "" {
		l = r.Kind + " " + r.Namespace + "/" + r.Name
	}
	switch r.source() {
	case SourceLastApplied:
		l += " (last applied)"
	case SourceHelm:
		l += " (Helm manifest)"
	}
	return l + " · " + r.Cluster
}

// Compare loads, normalizes and diffs a and b. A side that cannot be read is
// reported in its Side.Error rather than as an error, so a caller can show
// which one failed; err is only for invalid input.
func (s *Service) Compare(ctx context.Context, a, b Ref, opts Options) (*Result, error) {
	res := &Result{A: Side{Ref: a, Label: Label(a)}, B: Side{Ref: b, Label: Label(b)}, Summary: []Insight{}, Changes: []FieldChange{}}
	type loaded struct {
		obj map[string]any
		err error
	}
	ch := [2]chan loaded{make(chan loaded, 1), make(chan loaded, 1)}
	for i, r := range []Ref{a, b} {
		go func(i int, r Ref) {
			o, err := s.load(ctx, r)
			ch[i] <- loaded{o, err}
		}(i, r)
	}
	la, lb := <-ch[0], <-ch[1]
	setErr := func(side *Side, err error) {
		side.Error = err.Error()
		side.Status = http.StatusInternalServerError
		var se *SideError
		if errors.As(err, &se) {
			side.Status = se.Status
		}
	}
	if la.err != nil {
		setErr(&res.A, la.err)
	}
	if lb.err != nil {
		setErr(&res.B, lb.err)
	}
	if la.err != nil || lb.err != nil {
		return res, nil
	}
	res.Notes = Prepare(la.obj, lb.obj, a.source(), b.source(), opts)
	objA, objB := la.obj, lb.obj
	if !opts.IncludeUndeclared {
		switch {
		case a.source() != SourceLive && b.source() == SourceLive:
			objB, _ = PruneUndeclared(objB, objA).(map[string]any)
		case b.source() != SourceLive && a.source() == SourceLive:
			objA, _ = PruneUndeclared(objA, objB).(map[string]any)
		}
	}
	return finish(res, objA, objB)
}

// Prepare normalizes both objects and returns de-duplicated notes.
func Prepare(a, b map[string]any, sa, sb Source, opts Options) []string {
	seen := map[string]bool{}
	var notes []string
	for _, n := range append(Normalize(a, opts), Normalize(b, opts)...) {
		if !seen[n] {
			seen[n] = true
			notes = append(notes, n)
		}
	}
	if !opts.IncludeStatus {
		notes = append(notes, "Status is hidden.")
	}
	if !opts.KeepDefaults {
		notes = append(notes, "Server defaults and cluster-assigned fields are hidden.")
	}
	if !opts.IncludeUndeclared && (sa == SourceLive) != (sb == SourceLive) {
		notes = append(notes, "Live fields the declared version never set are hidden.")
	}
	return notes
}

func finish(res *Result, a, b map[string]any) (*Result, error) {
	var err error
	if res.A.YAML, err = ToYAML(a); err != nil {
		return nil, err
	}
	if res.B.YAML, err = ToYAML(b); err != nil {
		return nil, err
	}
	res.Changes = Diff(a, b)
	if res.Changes == nil {
		res.Changes = []FieldChange{}
	}
	for _, c := range res.Changes {
		switch c.Type {
		case OnlyInA:
			res.Stats.Removed++
		case OnlyInB:
			res.Stats.Added++
		default:
			res.Stats.Changed++
		}
	}
	res.Identical = len(res.Changes) == 0
	res.Summary = Summarize(a, b, res.Changes)
	if res.Summary == nil {
		res.Summary = []Insight{}
	}
	return res, nil
}

// Drift compares what was declared for ref (source last-applied or helm)
// with the live object: A is the declared version, B is live.
func (s *Service) Drift(ctx context.Context, ref Ref, source Source, opts Options) (*Result, error) {
	declared := ref
	declared.Source = source
	live := ref
	live.Source = SourceLive
	return s.Compare(ctx, declared, live, opts)
}

// CompareObjects diffs two objects already in hand (for callers outside the
// cluster, e.g. a manifest file against a live object).
func CompareObjects(a, b map[string]any, opts Options) (*Result, error) {
	res := &Result{Summary: []Insight{}, Changes: []FieldChange{}}
	res.Notes = Prepare(a, b, SourceLive, SourceLive, opts)
	return finish(res, a, b)
}
