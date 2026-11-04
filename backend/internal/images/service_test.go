package images

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

type tfile struct {
	name, body, link string
	dir, symlink     bool
}

func layerOf(t *testing.T, files ...tfile) v1.Layer {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, f := range files {
		h := &tar.Header{Name: f.name, Mode: 0o644, Size: int64(len(f.body)), Typeflag: tar.TypeReg}
		switch {
		case f.dir:
			h.Typeflag, h.Mode, h.Size = tar.TypeDir, 0o755, 0
		case f.symlink:
			h.Typeflag, h.Linkname, h.Size = tar.TypeSymlink, f.link, 0
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			_, _ = tw.Write([]byte(f.body))
		}
	}
	_ = tw.Close()
	data := buf.Bytes()
	l, err := tarball.LayerFromOpener(func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(data)), nil })
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func testImage(t *testing.T, version string) v1.Image {
	t.Helper()
	l1 := layerOf(t,
		tfile{name: "etc/", dir: true},
		tfile{name: "etc/a.conf", body: "alpha"},
		tfile{name: "etc/b.conf", body: "beta"},
		tfile{name: "opq/", dir: true},
		tfile{name: "opq/old", body: "old"},
		tfile{name: "bin/tool", body: "\x7fELF\x00\x00binary"},
	)
	l2 := layerOf(t,
		tfile{name: "etc/.wh.a.conf"},
		tfile{name: "etc/b.conf", body: "beta " + version},
		tfile{name: "opq/.wh..wh..opq"},
		tfile{name: "opq/new", body: "new"},
		tfile{name: "etc/link", symlink: true, link: "b.conf"},
	)
	img, err := mutate.Append(empty.Image,
		mutate.Addendum{Layer: l1, History: v1.History{CreatedBy: "ADD rootfs /"}},
		mutate.Addendum{Layer: l2, History: v1.History{CreatedBy: "RUN rm /etc/a.conf DB_PASSWORD=hunter2"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := img.ConfigFile()
	cfg = cfg.DeepCopy()
	cfg.OS, cfg.Architecture = "linux", "amd64"
	cfg.Config.Env = []string{"PATH=/bin", "API_TOKEN=abc", "DATABASE_URL=postgres://u:pw@db/x"}
	cfg.Config.Entrypoint = []string{"/bin/tool"}
	cfg.Config.User = "1000"
	cfg.Config.ExposedPorts = map[string]struct{}{"8080/tcp": {}}
	img, err = mutate.ConfigFile(img, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func push(t *testing.T, ref string, img v1.Image) string {
	t.Helper()
	r, err := name.ParseReference(ref)
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.Write(r, img); err != nil {
		t.Fatal(err)
	}
	d, _ := img.Digest()
	return d.String()
}

func newSvc(t *testing.T, clients ClusterClients) *Service {
	t.Helper()
	s, err := NewService(clients, nil, t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestInspectFilesystemAndConfig(t *testing.T) {
	srv := httptest.NewServer(registry.New(registry.Logger(log.New(io.Discard, "", 0))))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	ref := host + "/team/app:v1"
	push(t, ref, testImage(t, "v1"))

	s := newSvc(t, nil)
	ctx := context.Background()
	info, err := s.ImageInfo(ctx, ref, PullContext{})
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Layers) != 2 || info.Layers[1].CreatedBy == "" {
		t.Fatalf("layers: %+v", info.Layers)
	}
	if strings.Contains(info.Layers[1].CreatedBy, "hunter2") {
		t.Errorf("history not scrubbed: %s", info.Layers[1].CreatedBy)
	}
	env := map[string]EnvVar{}
	for _, e := range info.Config.Env {
		env[e.Name] = e
	}
	if !env["API_TOKEN"].Redacted || env["PATH"].Redacted || strings.Contains(env["DATABASE_URL"].Value, "pw") {
		t.Errorf("env scrub: %+v", info.Config.Env)
	}
	if info.Config.User != "1000" || len(info.Config.ExposedPorts) != 1 {
		t.Errorf("config: %+v", info.Config)
	}

	root, err := s.ListDir(ctx, ref, PullContext{}, "/")
	if err != nil {
		t.Fatal(err)
	}
	if len(root.Entries) != 3 || root.Entries[0].Type != "dir" {
		t.Errorf("root: %+v", root.Entries)
	}
	etc, _ := s.ListDir(ctx, ref, PullContext{}, "/etc")
	var names []string
	for _, e := range etc.Entries {
		names = append(names, e.Name)
	}
	if strings.Join(names, ",") != "b.conf,link" {
		t.Errorf("whiteout not applied: %v", names)
	}
	opq, _ := s.ListDir(ctx, ref, PullContext{}, "/opq")
	if len(opq.Entries) != 1 || opq.Entries[0].Name != "new" {
		t.Errorf("opaque dir: %+v", opq.Entries)
	}

	fc, err := s.ReadFile(ctx, ref, PullContext{}, "/etc/link", 0)
	if err != nil {
		t.Fatal(err)
	}
	if fc.Content != "beta v1" || fc.Binary || fc.Layer != 1 {
		t.Errorf("file: %+v", fc)
	}
	bin, err := s.ReadFile(ctx, ref, PullContext{}, "/bin/tool", 0)
	if err != nil || !bin.Binary {
		t.Errorf("binary: %+v %v", bin, err)
	}
	short, _ := s.ReadFile(ctx, ref, PullContext{}, "/etc/b.conf", 3)
	if short.Content != "bet" || !short.Truncated {
		t.Errorf("limit: %+v", short)
	}

	lc, err := s.LayerChanges(ctx, ref, PullContext{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if lc.Deleted != 2 || lc.Added != 2 || lc.Modified != 1 {
		t.Errorf("changes: %+v", lc)
	}
	sr, _ := s.Search(ctx, ref, PullContext{}, "CONF")
	if len(sr.Entries) != 1 {
		t.Errorf("search: %+v", sr.Entries)
	}

	// A fresh service reuses the on-disk index and blobs.
	s2, _ := NewService(nil, nil, s.cache.dir, 0)
	info2, err := s2.ImageInfo(ctx, ref, PullContext{})
	if err != nil || !info2.Indexed {
		t.Errorf("disk index not reused: %v %+v", err, info2)
	}
}

func TestMultiArchAndTagMoved(t *testing.T) {
	srv := httptest.NewServer(registry.New(registry.Logger(log.New(io.Discard, "", 0))))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	ref := host + "/team/multi:stable"

	amd := testImage(t, "amd")
	armBase := testImage(t, "arm")
	cfg, _ := armBase.ConfigFile()
	cfg = cfg.DeepCopy()
	cfg.Architecture = "arm64"
	arm, _ := mutate.ConfigFile(armBase, cfg)
	idx := mutate.AppendManifests(empty.Index,
		mutate.IndexAddendum{Add: amd, Descriptor: v1.Descriptor{Platform: &v1.Platform{OS: "linux", Architecture: "amd64"}}},
		mutate.IndexAddendum{Add: arm, Descriptor: v1.Descriptor{Platform: &v1.Platform{OS: "linux", Architecture: "arm64"}}},
	)
	r, _ := name.ParseReference(ref)
	if err := remote.WriteIndex(r, idx); err != nil {
		t.Fatal(err)
	}
	armDigest, _ := arm.Digest()

	s := newSvc(t, nil)
	ctx := context.Background()
	info, err := s.ImageInfo(ctx, ref, PullContext{Platform: "linux/arm64"})
	if err != nil {
		t.Fatal(err)
	}
	if info.Digest != armDigest.String() || len(info.Platforms) != 2 || info.IndexDigest == "" {
		t.Errorf("platform pick: %+v", info)
	}
	// Running the arm manifest of the current index: not moved.
	info, _ = s.ImageInfo(ctx, ref, PullContext{Platform: "linux/arm64", ImageID: host + "/team/multi@" + armDigest.String()})
	if info.Tag == nil || info.Tag.Moved {
		t.Errorf("should not be moved: %+v", info.Tag)
	}

	// Retag to a new single-arch image: the old digest now differs.
	push(t, ref, testImage(t, "next"))
	info, err = s.ImageInfo(ctx, ref, PullContext{Platform: "linux/amd64", ImageID: "docker-pullable://" + host + "/team/multi@" + armDigest.String()})
	if err != nil {
		t.Fatal(err)
	}
	if info.Tag == nil || !info.Tag.Moved || info.Digest != armDigest.String() {
		t.Errorf("tag moved not detected / running image not inspected: %+v %s", info.Tag, info.Digest)
	}
}

type fakeClients struct{ c kubernetes.Interface }

func (f fakeClients) GetClientForCluster(string) (kubernetes.Interface, error) { return f.c, nil }

func TestPullSecretAuth(t *testing.T) {
	reg := registry.New(registry.Logger(log.New(io.Discard, "", 0)))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != "robot" || p != "s3cret" {
			w.Header().Set("WWW-Authenticate", `Basic realm="test"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		reg.ServeHTTP(w, r)
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	ref := host + "/private/app:v1"

	// Push straight to the inner registry.
	inner := httptest.NewServer(reg)
	defer inner.Close()
	push(t, strings.TrimPrefix(inner.URL, "http://")+"/private/app:v1", testImage(t, "p"))

	auth := base64.StdEncoding.EncodeToString([]byte("robot:s3cret"))
	cs := fake.NewSimpleClientset(
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "regcred", Namespace: "apps"}, Type: corev1.SecretTypeDockerConfigJson,
			Data: map[string][]byte{corev1.DockerConfigJsonKey: []byte(fmt.Sprintf(`{"auths":{"http://%s":{"auth":"%s"}}}`, host, auth))}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "apps"},
			Spec: corev1.PodSpec{ImagePullSecrets: []corev1.LocalObjectReference{{Name: "regcred"}}, Containers: []corev1.Container{{Name: "app", Image: ref}}}},
	)
	s := newSvc(t, fakeClients{cs})
	ctx := context.Background()

	_, err := s.ImageInfo(ctx, ref, PullContext{Cluster: "c", Namespace: "other"})
	if KindOf(err) != ErrAuth {
		t.Fatalf("want auth error, got %v", err)
	}
	info, err := s.ImageInfo(ctx, ref, PullContext{Cluster: "c", Namespace: "apps", Pod: "web", Container: "app"})
	if err != nil {
		t.Fatal(err)
	}
	if info.AuthMethod != "pull-secret" {
		t.Errorf("auth method %q", info.AuthMethod)
	}
}
