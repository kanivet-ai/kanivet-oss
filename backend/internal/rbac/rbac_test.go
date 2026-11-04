package rbac

import (
	"context"
	"testing"

	authv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func forbidden(res string) error {
	return apierrors.NewForbidden(schema.GroupResource{Group: "rbac.authorization.k8s.io", Resource: res}, "", nil)
}

func om(name, ns string) metav1.ObjectMeta { return metav1.ObjectMeta{Name: name, Namespace: ns} }

func sa(name, ns string) rbacv1.Subject {
	return rbacv1.Subject{Kind: "ServiceAccount", Name: name, Namespace: ns}
}

func fixture() *Snapshot {
	return &Snapshot{
		ClusterRoles: []rbacv1.ClusterRole{
			{ObjectMeta: om("cluster-admin", ""), Rules: []rbacv1.PolicyRule{{Verbs: []string{"*"}, APIGroups: []string{"*"}, Resources: []string{"*"}}}},
			{ObjectMeta: metav1.ObjectMeta{Name: "view-pods", Labels: map[string]string{"agg/view": "true"}},
				Rules: []rbacv1.PolicyRule{{Verbs: []string{"get", "list"}, APIGroups: []string{""}, Resources: []string{"pods"}}}},
			{ObjectMeta: metav1.ObjectMeta{Name: "view-deploys", Labels: map[string]string{"agg/view": "true"}},
				Rules: []rbacv1.PolicyRule{{Verbs: []string{"get"}, APIGroups: []string{"apps"}, Resources: []string{"deployments"}}}},
			{ObjectMeta: om("viewer", ""),
				AggregationRule: &rbacv1.AggregationRule{ClusterRoleSelectors: []metav1.LabelSelector{{MatchLabels: map[string]string{"agg/view": "true"}}}},
				// The controller has copied only the pods rule so far.
				Rules: []rbacv1.PolicyRule{{Verbs: []string{"get", "list"}, APIGroups: []string{""}, Resources: []string{"pods"}}}},
			{ObjectMeta: om("secret-reader", ""), Rules: []rbacv1.PolicyRule{{Verbs: []string{"get", "list"}, APIGroups: []string{""}, Resources: []string{"secrets"}}}},
		},
		Roles: []rbacv1.Role{
			{ObjectMeta: om("exec", "team"), Rules: []rbacv1.PolicyRule{
				{Verbs: []string{"create"}, APIGroups: []string{""}, Resources: []string{"pods/exec"}},
				{Verbs: []string{"get"}, APIGroups: []string{""}, Resources: []string{"configmaps"}, ResourceNames: []string{"app"}},
			}},
		},
		ClusterRoleBindings: []rbacv1.ClusterRoleBinding{
			{ObjectMeta: om("admins", ""), RoleRef: rbacv1.RoleRef{Kind: "ClusterRole", Name: "cluster-admin"},
				Subjects: []rbacv1.Subject{{Kind: "Group", Name: "ops"}}},
			{ObjectMeta: om("all-sa-view", ""), RoleRef: rbacv1.RoleRef{Kind: "ClusterRole", Name: "viewer"},
				Subjects: []rbacv1.Subject{{Kind: "Group", Name: "system:serviceaccounts:team"}}},
			{ObjectMeta: om("system:basic", ""), RoleRef: rbacv1.RoleRef{Kind: "ClusterRole", Name: "cluster-admin"},
				Subjects: []rbacv1.Subject{{Kind: "User", Name: "system:kube-controller-manager"}}},
		},
		RoleBindings: []rbacv1.RoleBinding{
			{ObjectMeta: om("ci-exec", "team"), RoleRef: rbacv1.RoleRef{Kind: "Role", Name: "exec"},
				Subjects: []rbacv1.Subject{{Kind: "ServiceAccount", Name: "ci"}}}, // namespace defaults to the binding's
			{ObjectMeta: om("default-secrets", "team"), RoleRef: rbacv1.RoleRef{Kind: "ClusterRole", Name: "secret-reader"},
				Subjects: []rbacv1.Subject{sa("default", "team")}},
			{ObjectMeta: om("dangling", "team"), RoleRef: rbacv1.RoleRef{Kind: "Role", Name: "gone"},
				Subjects: []rbacv1.Subject{{Kind: "User", Name: "bob"}}},
		},
		ServiceAccounts: []corev1.ServiceAccount{{ObjectMeta: om("ci", "team")}, {ObjectMeta: om("default", "team")}},
	}
}

func TestRuleAllows(t *testing.T) {
	cases := []struct {
		r    Rule
		q    Query
		want bool
	}{
		{Rule{Verbs: []string{"get"}, APIGroups: []string{""}, Resources: []string{"pods"}}, Query{Verb: "get", Group: "", Resource: "pods"}, true},
		{Rule{Verbs: []string{"get"}, APIGroups: []string{""}, Resources: []string{"pods"}}, Query{Verb: "list", Group: "", Resource: "pods"}, false},
		{Rule{Verbs: []string{"get"}, APIGroups: []string{""}, Resources: []string{"pods"}}, Query{Verb: "get", Group: "apps", Resource: "pods"}, false},
		{Rule{Verbs: []string{"get"}, APIGroups: []string{"apps"}, Resources: []string{"pods"}}, Query{Verb: "get", Group: "*", Resource: "pods"}, true},
		{Rule{Verbs: []string{"get"}, APIGroups: []string{""}, Resources: []string{"pods"}}, Query{Verb: "get", Resource: "pods", Subresource: "log"}, false},
		{Rule{Verbs: []string{"get"}, APIGroups: []string{""}, Resources: []string{"pods/*"}}, Query{Verb: "get", Resource: "pods", Subresource: "log"}, true},
		{Rule{Verbs: []string{"*"}, APIGroups: []string{"*"}, Resources: []string{"*"}}, Query{Verb: "escalate", Group: "rbac.authorization.k8s.io", Resource: "roles"}, true},
		{Rule{Verbs: []string{"get"}, APIGroups: []string{""}, Resources: []string{"secrets"}, ResourceNames: []string{"a"}}, Query{Verb: "get", Resource: "secrets"}, false},
		{Rule{Verbs: []string{"get"}, APIGroups: []string{""}, Resources: []string{"secrets"}, ResourceNames: []string{"a"}}, Query{Verb: "get", Resource: "secrets", Name: "a"}, true},
		{Rule{Verbs: []string{"get"}, NonResourceURLs: []string{"/metrics"}}, Query{Verb: "get", Group: "*", Resource: "pods"}, false},
	}
	for i, c := range cases {
		if got := RuleAllows(c.r, c.q); got != c.want {
			t.Errorf("case %d: got %v want %v", i, got, c.want)
		}
	}
}

func TestAggregationRecordsSourceAndFillsMissing(t *testing.T) {
	g := BuildGraph(fixture())
	m, ok := g.RolePermissions("ClusterRole", "", "viewer")
	if !ok {
		t.Fatal("viewer not found")
	}
	var pods, deploys *MatrixRow
	for i := range m.Rows {
		switch m.Rows[i].Resource {
		case "pods":
			pods = &m.Rows[i]
		case "deployments":
			deploys = &m.Rows[i]
		}
	}
	if pods == nil || deploys == nil {
		t.Fatalf("rows: %+v", m.Rows)
	}
	if src := pods.Cells["get"].Paths[0].Rule.Source; src != "view-pods" {
		t.Errorf("pods source = %q", src)
	}
	if !deploys.Cells["get"].Allowed || deploys.Cells["get"].Paths[0].Rule.Source != "view-deploys" {
		t.Errorf("unreconciled member rule missing: %+v", deploys.Cells)
	}
	if len(m.Bindings) != 1 || m.Bindings[0].Binding.Name != "all-sa-view" {
		t.Errorf("bindings = %+v", m.Bindings)
	}
}

func TestWhoCan(t *testing.T) {
	g := BuildGraph(fixture())
	r := g.WhoCan(Query{Verb: "create", Group: "", Resource: "pods/exec", Namespace: "team"})
	keys := map[string]bool{}
	for _, s := range r.Subjects {
		keys[s.Subject.Key()] = true
	}
	for _, want := range []string{"Group:ops", "ServiceAccount:team/ci", "User:system:kube-controller-manager"} {
		if !keys[want] {
			t.Errorf("missing %s in %v", want, keys)
		}
	}
	if !r.Subjects[0].ClusterWide {
		t.Error("cluster-wide subjects sort first")
	}
	// Exec in another namespace: the team Role does not apply.
	r = g.WhoCan(Query{Verb: "create", Group: "", Resource: "pods", Subresource: "exec", Namespace: "other"})
	for _, s := range r.Subjects {
		if s.Subject.Name == "ci" {
			t.Error("namespaced grant leaked into another namespace")
		}
	}
	// A named rule does not answer a question about every configmap.
	r = g.WhoCan(Query{Verb: "get", Group: "", Resource: "configmaps", Namespace: "team"})
	for _, s := range r.Subjects {
		if s.Subject.Name == "ci" {
			t.Error("resourceNames rule matched an unnamed query")
		}
	}
}

func TestSubjectPermissionsIncludesImplicitGroups(t *testing.T) {
	g := BuildGraph(fixture())
	m := g.SubjectPermissions(SubjectRef{Kind: KindServiceAccount, Name: "ci", Namespace: "team"}, "team")
	if len(m.Bindings) != 2 {
		t.Fatalf("bindings = %+v", m.Bindings)
	}
	var via string
	for _, b := range m.Bindings {
		if b.Binding.Name == "all-sa-view" {
			via = b.Via
		}
	}
	if via != "Group system:serviceaccounts:team" {
		t.Errorf("via = %q", via)
	}
	found := false
	for _, row := range m.Rows {
		if row.Resource == "configmaps" {
			found = true
			if c := row.Cells["get"]; c == nil || !c.Limited {
				t.Errorf("configmaps get should be limited: %+v", c)
			}
		}
	}
	if !found {
		t.Error("configmaps row missing")
	}
	hasExec := false
	for _, r := range m.Risks {
		if r.Category == "exec" {
			hasExec = true
		}
	}
	if !hasExec {
		t.Errorf("exec risk missing: %+v", m.Risks)
	}
}

func TestDanglingRoleRef(t *testing.T) {
	g := BuildGraph(fixture())
	m := g.SubjectPermissions(SubjectRef{Kind: KindUser, Name: "bob"}, "")
	if len(m.Bindings) != 1 || !m.Bindings[0].RoleMissing {
		t.Errorf("bindings = %+v", m.Bindings)
	}
}

func TestRiskyGrants(t *testing.T) {
	g := BuildGraph(fixture())
	rs := g.RiskyGrants(RiskOptions{})
	cats := map[string]Severity{}
	for _, r := range rs {
		if r.System {
			t.Errorf("system binding reported: %+v", r)
		}
		cats[r.Category+"/"+r.Binding.Name] = r.Severity
	}
	want := map[string]Severity{
		"cluster-admin/admins":       SeverityCritical,
		"secrets/default-secrets":    SeverityMedium,
		"default-sa/default-secrets": SeverityMedium,
		"exec/ci-exec":               SeverityMedium,
	}
	for k, sev := range want {
		if cats[k] != sev {
			t.Errorf("%s = %q want %q (all %v)", k, cats[k], sev, cats)
		}
	}
	if rs[0].Severity != SeverityCritical {
		t.Error("critical first")
	}
	with := g.RiskyGrants(RiskOptions{IncludeSystem: true})
	if len(with) <= len(rs) {
		t.Error("IncludeSystem should add system bindings")
	}
}

type fakeClients struct{ cs kubernetes.Interface }

func (f fakeClients) GetClientForCluster(string) (kubernetes.Interface, error) { return f.cs, nil }

func TestServiceLoadsAndDegrades(t *testing.T) {
	s := fixture()
	var objs []runtime.Object
	for i := range s.ClusterRoles {
		objs = append(objs, &s.ClusterRoles[i])
	}
	for i := range s.RoleBindings {
		objs = append(objs, &s.RoleBindings[i])
	}
	cs := fake.NewSimpleClientset(objs...)
	cs.PrependReactor("list", "clusterrolebindings", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, forbidden("clusterrolebindings")
	})
	svc := NewService(fakeClients{cs}, nil)
	subjects, info, err := svc.Subjects(context.Background(), "c")
	if err != nil {
		t.Fatal(err)
	}
	if info.ClusterRoles != len(s.ClusterRoles) || len(info.Warnings) != 1 {
		t.Errorf("info = %+v", info)
	}
	if len(subjects) == 0 {
		t.Error("no subjects")
	}
}

func TestSelfPermissions(t *testing.T) {
	cs := fake.NewSimpleClientset()
	cs.PrependReactor("create", "selfsubjectrulesreviews", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, &authv1.SelfSubjectRulesReview{Status: authv1.SubjectRulesReviewStatus{
			ResourceRules: []authv1.ResourceRule{{Verbs: []string{"get", "list"}, APIGroups: []string{""}, Resources: []string{"pods"}}},
			Incomplete:    true,
		}}, nil
	})
	svc := NewService(fakeClients{cs}, nil)
	res, err := svc.SelfPermissions(context.Background(), "c", "team")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Incomplete || len(res.Matrix.Rows) != 1 || !res.Matrix.Rows[0].Cells["list"].Allowed {
		t.Errorf("self = %+v", res.Matrix)
	}
}

func TestFullWildcardIsOneFindingAndComponentsAreSystem(t *testing.T) {
	s := fixture()
	s.ClusterRoles = append(s.ClusterRoles, rbacv1.ClusterRole{ObjectMeta: om("god", ""),
		Rules: []rbacv1.PolicyRule{{Verbs: []string{"*"}, APIGroups: []string{"*"}, Resources: []string{"*"}}}})
	s.ClusterRoleBindings = append(s.ClusterRoleBindings,
		rbacv1.ClusterRoleBinding{ObjectMeta: om("god-ops", ""), RoleRef: rbacv1.RoleRef{Kind: "ClusterRole", Name: "god"},
			Subjects: []rbacv1.Subject{{Kind: "Group", Name: "ops"}}},
		rbacv1.ClusterRoleBinding{ObjectMeta: om("masters", ""), RoleRef: rbacv1.RoleRef{Kind: "ClusterRole", Name: "cluster-admin"},
			Subjects: []rbacv1.Subject{{Kind: "Group", Name: "system:masters"}}},
		rbacv1.ClusterRoleBinding{ObjectMeta: om("everyone", ""), RoleRef: rbacv1.RoleRef{Kind: "ClusterRole", Name: "secret-reader"},
			Subjects: []rbacv1.Subject{{Kind: "Group", Name: "system:authenticated"}}})
	var god, everyone []string
	for _, r := range BuildGraph(s).RiskyGrants(RiskOptions{}) {
		switch r.Binding.Name {
		case "god-ops":
			god = append(god, r.Category)
		case "masters":
			t.Error("component-only binding should count as system")
		case "everyone":
			everyone = append(everyone, r.Category)
		}
	}
	if len(god) != 1 || god[0] != "wildcard" {
		t.Errorf("god-ops findings = %v", god)
	}
	if len(everyone) != 1 || everyone[0] != "secrets" {
		t.Errorf("everyone findings = %v", everyone)
	}
}
