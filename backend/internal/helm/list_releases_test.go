package helm

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"testing"

	"github.com/kanivet/backend/internal/k8s"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	kubefake "helm.sh/helm/v3/pkg/kube/fake"
	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/storage"
	"helm.sh/helm/v3/pkg/storage/driver"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func storeRelease(t *testing.T, cs *fake.Clientset, ns, name string, version int, status release.Status) {
	t.Helper()
	rel := &release.Release{
		Name:      name,
		Namespace: ns,
		Version:   version,
		Chart:     &chart.Chart{Metadata: &chart.Metadata{Name: name, Version: "1.0.0"}},
		Info:      &release.Info{Status: status},
	}
	if err := driver.NewSecrets(cs.CoreV1().Secrets(ns)).Create(fmt.Sprintf("sh.helm.release.v1.%s.v%d", name, version), rel); err != nil {
		t.Fatal(err)
	}
}

// helmTestService serves Helm configurations whose cluster is reachable
// without a network; the release store is the fake clientset's Secrets.
func helmTestService(t *testing.T, cs *fake.Clientset, namespaces ...string) *Service {
	s := NewService(&k8s.MockClient{TypedClient: cs})
	for _, ns := range append([]string{""}, namespaces...) {
		s.configCache["c1:"+ns] = &action.Configuration{
			KubeClient: &kubefake.PrintingKubeClient{Out: io.Discard},
			Releases:   storage.Init(driver.NewSecrets(cs.CoreV1().Secrets(ns))),
			Log:        t.Logf,
		}
	}
	return s
}

func secretLists(cs *fake.Clientset) []k8stesting.ListActionImpl {
	var out []k8stesting.ListActionImpl
	for _, a := range cs.Actions() {
		if l, ok := a.(k8stesting.ListActionImpl); ok && l.GetResource().Resource == "secrets" {
			out = append(out, l)
		}
	}
	return out
}

func releaseKeys(rels []Release) string {
	keys := make([]string, len(rels))
	for i, r := range rels {
		keys[i] = fmt.Sprintf("%s/%s@%d:%s", r.Namespace, r.Name, r.Revision, r.Status)
	}
	return strings.Join(keys, " ")
}

// The all-namespaces view ran one LIST per namespace, and each decoded every
// revision Helm keeps before keeping the latest. One cluster-wide LIST that
// leaves superseded revisions on the server returns the same releases.
func TestListReleasesAllNamespacesListsOnceWithoutHistory(t *testing.T) {
	nss := []string{"apps", "data", "legacy"}
	cs := fake.NewSimpleClientset()
	for _, ns := range nss {
		if _, err := cs.CoreV1().Namespaces().Create(context.Background(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	for v := 1; v < 5; v++ {
		storeRelease(t, cs, "apps", "web", v, release.StatusSuperseded)
	}
	storeRelease(t, cs, "apps", "web", 5, release.StatusDeployed)
	storeRelease(t, cs, "data", "db", 1, release.StatusDeployed)
	storeRelease(t, cs, "data", "db", 2, release.StatusFailed)
	storeRelease(t, cs, "legacy", "gone", 1, release.StatusSuperseded)
	storeRelease(t, cs, "legacy", "gone", 2, release.StatusUninstalled)
	cs.ClearActions()

	s := helmTestService(t, cs, nss...)
	got, err := s.ListReleases(context.Background(), "c1", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if want := "apps/web@5:deployed data/db@2:failed"; releaseKeys(got) != want {
		t.Fatalf("releases %q, want %q", releaseKeys(got), want)
	}
	lists := secretLists(cs)
	if len(lists) != 1 {
		t.Fatalf("%d Secrets LISTs, want one across all namespaces", len(lists))
	}
	if ns := lists[0].GetNamespace(); ns != "" {
		t.Errorf("Secrets listed in namespace %q, want all namespaces", ns)
	}
	if sel := lists[0].ListOptions.LabelSelector; !strings.Contains(sel, "status!=superseded") {
		t.Errorf("label selector %q downloads superseded revisions", sel)
	}

	// The streaming view takes the same path.
	cs.ClearActions()
	rels, _, err := s.StreamReleases(context.Background(), "c1")
	if err != nil {
		t.Fatal(err)
	}
	var streamed []Release
	for batch := range rels {
		streamed = append(streamed, batch...)
	}
	if releaseKeys(streamed) != releaseKeys(got) {
		t.Fatalf("streamed %q, listed %q", releaseKeys(streamed), releaseKeys(got))
	}
	if n := len(secretLists(cs)); n != 1 {
		t.Fatalf("streaming made %d Secrets LISTs, want 1", n)
	}
}

// The secrets driver logs a release record it cannot decode with the whole
// Secret as an argument. Helm's logger must name the record, never print its
// payload: backend stderr ends up in the give-up dialog.
func TestHelmLogNamesUndecodableRecordsWithoutTheirPayload(t *testing.T) {
	cs := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "sh.helm.release.v1.web.v3", Namespace: "apps", Labels: map[string]string{"owner": "helm"}},
		Data:       map[string][]byte{"release": []byte("not-a-release PLAINTEXT-VALUES")},
	})
	var out strings.Builder
	log.SetOutput(&out)
	defer log.SetOutput(os.Stderr)
	d := driver.NewSecrets(cs.CoreV1().Secrets(""))
	d.Log = helmLog

	if _, err := d.List(func(*release.Release) bool { return true }); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out.String(), "apps/sh.helm.release.v1.web.v3") {
		t.Fatalf("log does not name the record: %q", out.String())
	}
	if strings.Contains(out.String(), "PLAINTEXT") || strings.Contains(out.String(), "owner") {
		t.Fatalf("log carries the record's contents: %q", out.String())
	}
}

// Without RBAC to list Secrets across namespaces, the view lists them per
// namespace as before.
func TestListReleasesAllNamespacesFallsBackPerNamespace(t *testing.T) {
	nss := []string{"apps", "data"}
	cs := fake.NewSimpleClientset()
	for _, ns := range nss {
		if _, err := cs.CoreV1().Namespaces().Create(context.Background(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	storeRelease(t, cs, "apps", "web", 1, release.StatusDeployed)
	storeRelease(t, cs, "data", "db", 1, release.StatusDeployed)
	cs.PrependReactor("list", "secrets", func(a k8stesting.Action) (bool, runtime.Object, error) {
		if a.GetNamespace() == "" {
			return true, nil, fmt.Errorf("secrets is forbidden: cannot list resource \"secrets\" at the cluster scope")
		}
		return false, nil, nil
	})

	got, err := helmTestService(t, cs, nss...).ListReleases(context.Background(), "c1", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if want := "apps/web@1:deployed data/db@1:deployed"; releaseKeys(got) != want {
		t.Fatalf("releases %q, want %q", releaseKeys(got), want)
	}
}
