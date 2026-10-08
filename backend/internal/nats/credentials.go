package nats

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

// credentialSecretNames is a starting list of well-known Secret names that
// might hold a working NATS credential. It is not exhaustive: the real-world
// naming convention for NATS credential Secrets varies by Helm chart and by
// how each cluster operator set up accounts. Extend it as real deployments
// show a name this list misses.
var credentialSecretNames = []string{
	"nats-sys-creds",
	"nats-sys-user",
	"nats-creds",
	"nats-auth",
	"nats-box-creds",
	"nats-box-user",
}

// natsAccountGVR is deinstapel/nats-jwt-operator's Account CRD. A NatsAccount
// names the Kubernetes namespaces its NatsUser objects (and their generated
// Secrets) are allowed to live in - not necessarily the same namespace the
// NATS server itself runs in, so credential discovery has to follow this
// pointer rather than only searching the server's own namespace.
var natsAccountGVR = schema.GroupVersionResource{Group: "nats.deinstapel.de", Version: "v1alpha1", Resource: "natsaccounts"}

// resolvedCredential describes which credential path, if any, produced a
// working connection. It is informational only, surfaced to callers that
// want to show the user what was tried.
type resolvedCredential struct {
	kind      string // "none", "secret:<ns>/<name>:creds", "secret:<ns>/<name>:token", "secret:<ns>/<name>:userpass"
	attempted []string
}

// candidate is one successfully-authenticated connection found during
// discovery, not yet known to have JetStream access.
type candidate struct {
	nc   *nats.Conn
	kind string
}

// connectOptions are applied to every dial attempt, no-auth or credentialed.
func connectOptions(extra ...nats.Option) []nats.Option {
	opts := []nats.Option{
		nats.Name("kanivet"),
		nats.Timeout(5 * time.Second),
		nats.MaxReconnects(0),
		nats.RetryOnFailedConnect(false),
	}
	return append(opts, extra...)
}

// tryConnect dials and confirms the connection actually works with a flush,
// so a stale or wrong credential fails here instead of surfacing later.
func tryConnect(ctx context.Context, url string, opts ...nats.Option) (*nats.Conn, error) {
	nc, err := nats.Connect(url, opts...)
	if err != nil {
		return nil, err
	}
	if err := nc.FlushWithContext(ctx); err != nil {
		nc.Close()
		return nil, err
	}
	return nc, nil
}

// hasJetStream reports whether JetStream is actually enabled and reachable
// on the account a connection authenticated as. A connection can be
// perfectly valid (e.g. a NATS system account) and still see none of the
// JetStream data tier 2 needs, because JetStream is scoped per account.
//
// An account with no JetStream permissions doesn't make AccountInfo fail
// fast: NATS permission errors land as an async client event, not a reply,
// so the request can sit waiting for a reply that will never come. This
// check gets its own short budget, independent of the caller's overall
// timeout, so one bad candidate can't burn the whole discovery walk and
// starve the credential that would have actually worked.
func hasJetStream(ctx context.Context, nc *nats.Conn) bool {
	js, err := jetstream.New(nc)
	if err != nil {
		return false
	}
	checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_, err = js.AccountInfo(checkCtx)
	return err == nil
}

// resolveCredential tries, in order: no-auth, well-known Secret names in
// namespace, then every namespace a NatsAccount CR (if any) declares as
// valid for its users. Among every connection that authenticates, it
// prefers the first with working JetStream access - the point of tier 2 -
// and only falls back to a merely-authenticated connection if none of them
// have it.
func resolveCredential(ctx context.Context, kube kubernetes.Interface, dyn dynamic.Interface, namespace, url string) (*nats.Conn, resolvedCredential, error) {
	cred := resolvedCredential{}
	var fallback *candidate

	// accept decides what to do with a successfully-authenticated candidate:
	// take it immediately if it has working JetStream access, otherwise hold
	// at most one as a fallback (closing any further ones so connections
	// don't leak) and keep searching.
	accept := func(found candidate, ok bool) (*nats.Conn, string, bool) {
		if !ok {
			return nil, "", false
		}
		if hasJetStream(ctx, found.nc) {
			return found.nc, found.kind, true
		}
		if fallback == nil {
			fallback = &found
		} else {
			found.nc.Close()
		}
		return nil, "", false
	}

	if nc, err := tryConnect(ctx, url, connectOptions()...); err == nil {
		if conn, kind, done := accept(candidate{nc: nc, kind: "none"}, true); done {
			cred.kind = kind
			return conn, cred, nil
		}
	}

	tried := make(map[string]bool, len(credentialSecretNames))
	for _, name := range credentialSecretNames {
		tried[namespace+"/"+name] = true
		secret, err := kube.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			if !apierrors.IsNotFound(err) {
				cred.attempted = append(cred.attempted, fmt.Sprintf("%s/%s (read failed: %v)", namespace, name, err))
			}
			continue
		}
		cred.attempted = append(cred.attempted, namespace+"/"+name)
		found, ok := tryConnectWithSecret(ctx, url, namespace, secret)
		if conn, kind, done := accept(found, ok); done {
			cred.kind = kind
			return conn, cred, nil
		}
	}

	// None of the well-known names in the server's own namespace worked, or
	// only gave an account without JetStream. Discover other namespaces to
	// search: the server's own namespace again (for a Secret not named
	// anything on the fixed list) plus every namespace any NatsAccount CR
	// declares its users may live in.
	for _, ns := range discoverySearchNamespaces(ctx, dyn, namespace) {
		found, ok := tryAnyCredsSecret(ctx, kube, ns, url, tried)
		if conn, kind, done := accept(found, ok); done {
			cred.kind = kind
			cred.attempted = append(cred.attempted, kind+" (discovered)")
			return conn, cred, nil
		}
		if ok {
			cred.attempted = append(cred.attempted, found.kind+" (discovered, no JetStream)")
		}
	}

	if fallback != nil {
		cred.kind = fallback.kind
		return fallback.nc, cred, nil
	}
	return nil, cred, fmt.Errorf("no working NATS credential found; tried no-auth and Secret(s): %s", strings.Join(cred.attempted, ", "))
}

// discoverySearchNamespaces returns namespace to search beyond the fixed
// name list: the server's own namespace, plus every namespace named in a
// NatsAccount CR's allowedUserNamespaces, if that CRD exists on this
// cluster. Absent the CRD (most clusters), this is just [namespace].
func discoverySearchNamespaces(ctx context.Context, dyn dynamic.Interface, namespace string) []string {
	namespaces := []string{namespace}
	if dyn == nil {
		return namespaces
	}
	list, err := dyn.Resource(natsAccountGVR).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return namespaces
	}
	seen := map[string]bool{namespace: true}
	for _, account := range list.Items {
		allowed, _, _ := unstructuredStringSlice(account.Object, "spec", "allowedUserNamespaces")
		for _, ns := range allowed {
			if !seen[ns] {
				seen[ns] = true
				namespaces = append(namespaces, ns)
			}
		}
	}
	return namespaces
}

// unstructuredStringSlice reads a []string field out of an unstructured
// object without requiring generated types for the deinstapel CRDs.
func unstructuredStringSlice(obj map[string]any, fields ...string) ([]string, bool, error) {
	var cur any = obj
	for _, f := range fields {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false, nil
		}
		cur, ok = m[f]
		if !ok {
			return nil, false, nil
		}
	}
	raw, ok := cur.([]any)
	if !ok {
		return nil, false, nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out, true, nil
}

// tryAnyCredsSecret lists every Secret in namespace not already tried by
// namespace/name and tries the first one carrying a key that ends in
// ".creds". It never inspects token/username/password fields on a
// discovered Secret - only the explicit, low-false-positive .creds signal -
// so a Secret that happens to have unrelated keys is left alone.
func tryAnyCredsSecret(ctx context.Context, kube kubernetes.Interface, namespace, url string, alreadyTried map[string]bool) (candidate, bool) {
	secrets, err := kube.CoreV1().Secrets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return candidate{}, false
	}
	for _, secret := range secrets.Items {
		if alreadyTried[namespace+"/"+secret.Name] {
			continue
		}
		alreadyTried[namespace+"/"+secret.Name] = true
		for key, value := range secret.Data {
			if !strings.HasSuffix(key, ".creds") {
				continue
			}
			if nc, err := tryConnect(ctx, url, connectOptions(nats.UserCredentialBytes(value))...); err == nil {
				return candidate{nc: nc, kind: fmt.Sprintf("secret:%s/%s:creds", namespace, secret.Name)}, true
			}
			break
		}
	}
	return candidate{}, false
}

// tryConnectWithSecret tries every credential shape a Secret might hold, in
// priority order: a .creds file (JWT+seed), a bare token, then username and
// password.
func tryConnectWithSecret(ctx context.Context, url, namespace string, secret *corev1.Secret) (candidate, bool) {
	for key, value := range secret.Data {
		if strings.HasSuffix(key, ".creds") {
			if nc, err := tryConnect(ctx, url, connectOptions(nats.UserCredentialBytes(value))...); err == nil {
				return candidate{nc: nc, kind: fmt.Sprintf("secret:%s/%s:creds", namespace, secret.Name)}, true
			}
		}
	}
	if token, ok := secret.Data["token"]; ok {
		if nc, err := tryConnect(ctx, url, connectOptions(nats.Token(string(token)))...); err == nil {
			return candidate{nc: nc, kind: fmt.Sprintf("secret:%s/%s:token", namespace, secret.Name)}, true
		}
	}
	user, hasUser := secret.Data["username"]
	pass, hasPass := secret.Data["password"]
	if hasUser && hasPass {
		if nc, err := tryConnect(ctx, url, connectOptions(nats.UserInfo(string(user), string(pass)))...); err == nil {
			return candidate{nc: nc, kind: fmt.Sprintf("secret:%s/%s:userpass", namespace, secret.Name)}, true
		}
	}
	return candidate{}, false
}
