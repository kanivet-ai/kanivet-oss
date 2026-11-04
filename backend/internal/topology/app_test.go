package topology

import "testing"

func TestAppOf(t *testing.T) {
	cases := []struct {
		labels, ann map[string]string
		want        string
	}{
		{map[string]string{"app.kubernetes.io/name": "api", "app.kubernetes.io/instance": "x"}, nil, "api"},
		{map[string]string{"app.kubernetes.io/instance": "x", "app": "y"}, nil, "x"},
		{map[string]string{"app.kubernetes.io/part-of": "shop", "app": "y"}, nil, "shop"},
		{map[string]string{"k8s-app": "dns"}, nil, "dns"},
		{nil, map[string]string{"meta.helm.sh/release-name": "rel"}, "rel"},
		{map[string]string{"release": "r2"}, nil, "r2"},
		{nil, nil, ""},
	}
	for _, c := range cases {
		if got := appOf(c.labels, c.ann); got != c.want {
			t.Errorf("appOf(%v,%v) = %q, want %q", c.labels, c.ann, got, c.want)
		}
	}
}
