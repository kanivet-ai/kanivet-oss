package imageutil

import "testing"

const d = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestDigestFromImageID(t *testing.T) {
	cases := []struct {
		in   string
		want string
		repo bool
	}{
		{"docker-pullable://nginx@" + d, d, true},
		{"docker.io/library/nginx@" + d, d, true},
		{d, d, false},
		{"", "", false},
		{"nginx:1.25", "", false},
	}
	for _, c := range cases {
		got, repo := DigestFromImageID(c.in)
		if got != c.want || repo != c.repo {
			t.Errorf("%q: got %q,%v want %q,%v", c.in, got, repo, c.want, c.repo)
		}
	}
	if r := RepoFromImageID("docker-pullable://ghcr.io/a/b@" + d); r != "ghcr.io/a/b" {
		t.Errorf("repo %q", r)
	}
}

func TestSplitAndShort(t *testing.T) {
	cases := []struct{ in, repo, tag, dig, short string }{
		{"nginx", "nginx", "", "", "nginx"},
		{"nginx:1.25", "nginx", "1.25", "", "nginx:1.25"},
		{"localhost:5000/app/web:v2", "localhost:5000/app/web", "v2", "", "web:v2"},
		{"localhost:5000/app/web", "localhost:5000/app/web", "", "", "web"},
		{"ghcr.io/a/b:v1@" + d, "ghcr.io/a/b", "v1", d, "b:v1"},
		{"ghcr.io/a/b@" + d, "ghcr.io/a/b", "", d, "b@0123456789ab"},
	}
	for _, c := range cases {
		r, tg, dg := Split(c.in)
		if r != c.repo || tg != c.tag || dg != c.dig {
			t.Errorf("Split(%q) = %q %q %q", c.in, r, tg, dg)
		}
		if s := Short(c.in); s != c.short {
			t.Errorf("Short(%q) = %q want %q", c.in, s, c.short)
		}
	}
	if w := WithDigest("nginx:1.25", d); w != "nginx@"+d {
		t.Errorf("WithDigest %q", w)
	}
}
