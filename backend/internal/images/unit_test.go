package images

import (
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	corev1 "k8s.io/api/core/v1"
)

func TestScrubVar(t *testing.T) {
	cases := []struct {
		name, value string
		redact      bool
	}{
		{"PATH", "/usr/bin", false},
		{"DB_PASSWORD", "x", true},
		{"GITHUB_TOKEN", "x", true},
		{"AWS_SECRET_ACCESS_KEY", "x", true},
		{"PASSWORD_FILE", "/run/secrets/pw", false},
		{"LANG", "ghp_abcdefghijklmnopqrstuvwxyz0123", true},
		{"REDIS_URL", "redis://user:pass@host:6379", true},
		{"HOME", "/root", false},
		{"EMPTY_SECRET", "", false},
	}
	for _, c := range cases {
		if got := scrubVar(c.name, c.value); got.Redacted != c.redact {
			t.Errorf("%s=%s redacted=%v", c.name, c.value, got.Redacted)
		}
	}
	if s := scrubText(`/bin/sh -c export API_KEY=abc123 && echo ok`); s != `/bin/sh -c export API_KEY=`+redacted+` && echo ok` {
		t.Errorf("scrubText: %s", s)
	}
}

func TestParsePullSecret(t *testing.T) {
	s := &corev1.Secret{Type: corev1.SecretTypeDockerConfigJson, Data: map[string][]byte{
		corev1.DockerConfigJsonKey: []byte(`{"auths":{"https://index.docker.io/v1/":{"auth":"dTpw"},"ghcr.io":{"username":"a","password":"b"},"*.azurecr.io":{"username":"c","password":"d"}}}`),
	}}
	kc := &secretKeychain{entries: parsePullSecret(s)}
	check := func(ref, user string) {
		t.Helper()
		r, err := name.ParseReference(ref)
		if err != nil {
			t.Fatal(err)
		}
		a, _ := kc.Resolve(r.Context())
		if user == "" {
			if a != authn.Anonymous {
				t.Errorf("%s: want anonymous", ref)
			}
			return
		}
		cfg, err := a.Authorization()
		if err != nil || cfg.Username != user {
			t.Errorf("%s: got %+v %v", ref, cfg, err)
		}
	}
	check("nginx:1", "u")
	check("ghcr.io/x/y:1", "a")
	check("myreg.azurecr.io/app:1", "c")
	check("quay.io/org/app:1", "")
	if got := parsePullSecret(&corev1.Secret{Type: corev1.SecretTypeOpaque}); len(got) != 0 {
		t.Errorf("opaque secret parsed: %v", got)
	}
}

func TestMergeLayersEdgeCases(t *testing.T) {
	tr := mergeLayers([][]layerEntry{
		{{Path: "a/b/c", Type: "file", Size: 3}, {Path: "a/d", Type: "file", Size: 4}, {Path: "lib", Type: "symlink", Link: "a"}},
		{{Path: "a/b", Type: "file", Size: 1}, {Path: "x/y", Type: "file", Size: 2}},
	})
	if n := tr.lookup("a/b", false); n == nil || n.children != nil {
		t.Errorf("file should replace dir")
	}
	if n := tr.lookup("lib/d", true); n == nil || n.size != 4 {
		t.Errorf("symlinked dir lookup failed")
	}
	if l, ok := tr.list("/lib"); !ok || len(l.Entries) != 2 {
		t.Errorf("list through symlink: %+v", l)
	}
	if tr.files != 4 || tr.size != 7 {
		t.Errorf("aggregates files=%d size=%d", tr.files, tr.size)
	}
	if cleanPath("./usr//bin/") != "usr/bin" || cleanPath("/") != "" || cleanPath("../../etc") != "etc" {
		t.Errorf("cleanPath")
	}
}

func TestTrimPartialRune(t *testing.T) {
	b := []byte("héllo€")
	if string(trimPartialRune(b[:len(b)-1])) != "héllo" {
		t.Errorf("trim")
	}
	if string(trimPartialRune(b)) != "héllo€" {
		t.Errorf("keep")
	}
	if looksBinary([]byte("plain text\n")) || !looksBinary([]byte{0x00, 0x01}) {
		t.Errorf("binary detection")
	}
}
