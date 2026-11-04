package rbac

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kanivet/backend/internal/cache"
	authv1 "k8s.io/api/authorization/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	graphTTL      = 30 * time.Second
	graphMaxStale = 10 * time.Minute
	loadTimeout   = 45 * time.Second
)

// ClientSource yields a clientset for a cluster; *k8s.Client satisfies it.
type ClientSource interface {
	GetClientForCluster(cluster string) (kubernetes.Interface, error)
}

// Service answers RBAC questions per cluster. The graph of a cluster is
// loaded once and served stale-while-revalidate for graphTTL, so repeated
// questions cost no API calls.
type Service struct {
	clients ClientSource
	cache   *cache.Cache
}

// NewService builds a Service. A nil cache gets a private one.
func NewService(clients ClientSource, c *cache.Cache) *Service {
	if c == nil {
		c = cache.New(time.Minute, 5*time.Minute)
	}
	return &Service{clients: clients, cache: c}
}

func graphKey(cluster string) string { return "rbac:graph:" + cluster }

// Invalidate drops a cluster's graph so the next question reloads it.
func (s *Service) Invalidate(cluster string) { s.cache.Delete(graphKey(cluster)) }

// Graph returns the cluster's RBAC graph.
func (s *Service) Graph(ctx context.Context, cluster string) (*Graph, error) {
	v, err := s.cache.GetOrSetSWR(graphKey(cluster), graphTTL, graphMaxStale, func() (interface{}, error) {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), loadTimeout)
		defer cancel()
		snap, err := s.load(cctx, cluster)
		if err != nil {
			return nil, err
		}
		return BuildGraph(snap), nil
	})
	if err != nil {
		return nil, err
	}
	return v.(*Graph), nil
}

// ErrNoAccess is returned when none of the RBAC kinds can be read.
var ErrNoAccess = errors.New("no permission to read RBAC objects in this cluster")

func (s *Service) load(ctx context.Context, cluster string) (*Snapshot, error) {
	cs, err := s.clients.GetClientForCluster(cluster)
	if err != nil {
		return nil, err
	}
	snap := &Snapshot{}
	failures := 0
	note := func(kind string, err error) bool {
		if err == nil {
			return true
		}
		failures++
		if apierrors.IsForbidden(err) {
			snap.Warnings = append(snap.Warnings, "Not allowed to list "+kind+"; results may be incomplete.")
		} else {
			snap.Warnings = append(snap.Warnings, fmt.Sprintf("Could not list %s: %v", kind, err))
		}
		return false
	}
	opts := metav1.ListOptions{}
	rbac := cs.RbacV1()
	if l, err := rbac.ClusterRoles().List(ctx, opts); note("ClusterRoles", err) {
		snap.ClusterRoles = l.Items
	}
	if l, err := rbac.ClusterRoleBindings().List(ctx, opts); note("ClusterRoleBindings", err) {
		snap.ClusterRoleBindings = l.Items
	}
	if l, err := rbac.Roles("").List(ctx, opts); note("Roles", err) {
		snap.Roles = l.Items
	}
	if l, err := rbac.RoleBindings("").List(ctx, opts); note("RoleBindings", err) {
		snap.RoleBindings = l.Items
	}
	if failures == 4 {
		return nil, fmt.Errorf("%w: %s", ErrNoAccess, snap.Warnings[0])
	}
	// Service accounts only feed the subject picker; failing to list them
	// is not worth a warning.
	if l, err := cs.CoreV1().ServiceAccounts("").List(ctx, opts); err == nil {
		snap.ServiceAccounts = l.Items
	}
	return snap, nil
}

// WhoCan lists the subjects allowed q in cluster.
func (s *Service) WhoCan(ctx context.Context, cluster string, q Query) (*WhoCanResult, error) {
	g, err := s.Graph(ctx, cluster)
	if err != nil {
		return nil, err
	}
	return g.WhoCan(q), nil
}

// SubjectPermissions is the permission matrix of subject, limited to grants
// that apply in namespace when it is set.
func (s *Service) SubjectPermissions(ctx context.Context, cluster string, subject SubjectRef, namespace string) (*PermissionMatrix, error) {
	g, err := s.Graph(ctx, cluster)
	if err != nil {
		return nil, err
	}
	return g.SubjectPermissions(subject, namespace), nil
}

// RolePermissions is the matrix of one Role/ClusterRole and its bindings.
func (s *Service) RolePermissions(ctx context.Context, cluster, kind, namespace, name string) (*PermissionMatrix, error) {
	g, err := s.Graph(ctx, cluster)
	if err != nil {
		return nil, err
	}
	m, ok := g.RolePermissions(kind, namespace, name)
	if !ok {
		return nil, fmt.Errorf("%s %s not found", kind, name)
	}
	return m, nil
}

// BindingPermissions is the matrix a binding grants and its subjects.
func (s *Service) BindingPermissions(ctx context.Context, cluster, kind, namespace, name string) (*PermissionMatrix, []SubjectRef, error) {
	g, err := s.Graph(ctx, cluster)
	if err != nil {
		return nil, nil, err
	}
	m, subjects, ok := g.BindingPermissions(kind, namespace, name)
	if !ok {
		return nil, nil, fmt.Errorf("%s %s not found", kind, name)
	}
	return m, subjects, nil
}

// RiskyGrants lists risky grants in cluster.
func (s *Service) RiskyGrants(ctx context.Context, cluster string, opts RiskOptions) ([]Risk, error) {
	g, err := s.Graph(ctx, cluster)
	if err != nil {
		return nil, err
	}
	return g.RiskyGrants(opts), nil
}

// Subjects lists every known subject for pickers.
func (s *Service) Subjects(ctx context.Context, cluster string) ([]SubjectSummary, GraphInfo, error) {
	g, err := s.Graph(ctx, cluster)
	if err != nil {
		return nil, GraphInfo{}, err
	}
	return g.Subjects(), g.Info(), nil
}

// SelfPermissions asks the API server what the current user may do in
// namespace (SelfSubjectRulesReview), as a matrix. It works even when the
// user cannot read RBAC objects.
func (s *Service) SelfPermissions(ctx context.Context, cluster, namespace string) (*SelfAccess, error) {
	cs, err := s.clients.GetClientForCluster(cluster)
	if err != nil {
		return nil, err
	}
	if namespace == "" {
		namespace = "default"
	}
	rev, err := cs.AuthorizationV1().SelfSubjectRulesReviews().Create(ctx, &authv1.SelfSubjectRulesReview{
		Spec: authv1.SelfSubjectRulesReviewSpec{Namespace: namespace},
	}, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}
	role := &role{ref: ObjectRef{Kind: "SelfSubjectRulesReview", Name: "you"}}
	for _, r := range rev.Status.ResourceRules {
		role.rules = append(role.rules, Rule{Verbs: r.Verbs, APIGroups: r.APIGroups, Resources: r.Resources, ResourceNames: r.ResourceNames})
	}
	for _, r := range rev.Status.NonResourceRules {
		role.rules = append(role.rules, Rule{Verbs: r.Verbs, NonResourceURLs: r.NonResourceURLs})
	}
	m := buildMatrix([]grantSource{{e: &edge{roleRef: role.ref, role: role, scope: namespace}}})
	m.Bindings = []BindingUse{}
	m.Namespace = namespace
	return &SelfAccess{Namespace: namespace, Incomplete: rev.Status.Incomplete, Matrix: m, Evaluation: rev.Status.EvaluationError}, nil
}

// SelfCanI asks the API server whether the current user may do q
// (SelfSubjectAccessReview). q.Group "*" is sent as the core group.
func (s *Service) SelfCanI(ctx context.Context, cluster string, q Query) (*CanIResult, error) {
	cs, err := s.clients.GetClientForCluster(cluster)
	if err != nil {
		return nil, err
	}
	group := q.Group
	if group == "*" {
		group = ""
	}
	rev, err := cs.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, &authv1.SelfSubjectAccessReview{
		Spec: authv1.SelfSubjectAccessReviewSpec{ResourceAttributes: &authv1.ResourceAttributes{
			Namespace: q.Namespace, Verb: q.Verb, Group: group, Resource: q.Resource,
			Subresource: q.Subresource, Name: q.Name,
		}},
	}, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}
	return &CanIResult{Allowed: rev.Status.Allowed, Denied: rev.Status.Denied, Reason: rev.Status.Reason}, nil
}
