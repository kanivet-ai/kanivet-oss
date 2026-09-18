package storage

import "testing"

func TestResourceNameFromID(t *testing.T) {
	arn := "arn:aws:eks:eu-west-2:1:cluster/bench"
	cases := []struct {
		id, cluster, want string
		ok                bool
	}{
		{BuildResourceID("c", "apps", "v1", "deployments", "ns", "web"), "c", "deployments", true},
		{BuildResourceID("c", "", "v1", "nodes", "", "n1"), "c", "nodes", true},
		{BuildResourceID(arn, "rbac.authorization.k8s.io", "v1", "clusterroles", "", "admin"), arn, "clusterroles", true},
		{BuildResourceID(arn, "", "v1", "pods", "kube-system", "coredns-1"), arn, "pods", true},
		{"kind:c:apps:v1:Deployment", "c", "", false},
		{"c///daemonsets/ns/alloy", "c", "daemonsets", true},
		{"other/apps/v1/deployments/ns/web", "c", "", false},
	}
	for _, tc := range cases {
		got, ok := ResourceNameFromID(tc.id, tc.cluster)
		if got != tc.want || ok != tc.ok {
			t.Errorf("ResourceNameFromID(%q, %q) = %q,%v want %q,%v", tc.id, tc.cluster, got, ok, tc.want, tc.ok)
		}
	}
}
