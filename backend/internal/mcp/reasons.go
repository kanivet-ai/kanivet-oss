package mcp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
)

// Reason explains why a result is empty or partial, so an agent can act on
// it (ask for access, pick another namespace, retry later) instead of
// concluding that nothing exists.
type Reason struct {
	Code      string `json:"code"`
	Kind      string `json:"kind,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

// Reason codes.
const (
	ReasonUserForbidden       = "user_forbidden"
	ReasonKindUnknown         = "kind_unknown"
	ReasonKindNotInstalled    = "kind_not_installed"
	ReasonNamespaceNotFound   = "namespace_not_found"
	ReasonObjectNotFound      = "object_not_found"
	ReasonCacheWarming        = "cache_warming"
	ReasonClusterDisconnected = "cluster_disconnected"
	ReasonCredentialsRejected = "credentials_rejected"
	ReasonTruncated           = "truncated"
	ReasonReadError           = "read_error"
)

// ReasonsDoc is appended to the description of every list/get/search-style
// tool.
const ReasonsDoc = ` When a result is empty or partial, "reasons" says why: user_forbidden (RBAC denies the user; do not retry), kind_unknown (no such kind; check spelling), kind_not_installed (the CRD/API is not on this cluster), namespace_not_found, object_not_found (definitive: it does not exist), cache_warming (retry in a few seconds), cluster_disconnected (cluster unreachable), credentials_rejected (re-authenticate), truncated (more results exist; narrow the query), read_error (other failure, see detail).`

// ErrKindUnknown marks a kind that discovery does not know.
var ErrKindUnknown = errors.New("unknown kind")

// ReasonFor classifies an error into a Reason.
func ReasonFor(err error, kind, namespace string) Reason {
	r := Reason{Code: ReasonReadError, Kind: kind, Namespace: namespace}
	if err == nil {
		return r
	}
	r.Detail = truncateText(ScrubText(err.Error()), 300)
	msg := strings.ToLower(err.Error())
	var netErr net.Error
	var status apierrors.APIStatus
	switch {
	case apierrors.IsForbidden(err):
		r.Code = ReasonUserForbidden
	case apierrors.IsUnauthorized(err):
		r.Code = ReasonCredentialsRejected
	case errors.Is(err, ErrKindUnknown) || strings.Contains(msg, "unknown kind"):
		r.Code = ReasonKindUnknown
	case meta.IsNoMatchError(err) || strings.Contains(msg, "no matches for kind") || strings.Contains(msg, "the server could not find the requested resource"):
		r.Code = ReasonKindNotInstalled
	case apierrors.IsNotFound(err):
		r.Code = ReasonObjectNotFound
		if errors.As(err, &status) {
			if d := status.Status().Details; d != nil && d.Kind == "namespaces" {
				r.Code = ReasonNamespaceNotFound
			}
		}
		if strings.Contains(msg, "namespaces \"") && strings.Contains(msg, "not found") {
			r.Code = ReasonNamespaceNotFound
		}
	case strings.Contains(msg, "cache") && (strings.Contains(msg, "warming") || strings.Contains(msg, "not synced") || strings.Contains(msg, "not ready")):
		r.Code = ReasonCacheWarming
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr),
		strings.Contains(msg, "connection refused"), strings.Contains(msg, "no such host"),
		strings.Contains(msg, "i/o timeout"), strings.Contains(msg, "tls handshake timeout"):
		r.Code = ReasonClusterDisconnected
	case strings.Contains(msg, "unauthorized") || strings.Contains(msg, "token has expired") || strings.Contains(msg, "credentials"):
		r.Code = ReasonCredentialsRejected
	case strings.Contains(msg, "forbidden"):
		r.Code = ReasonUserForbidden
	}
	return r
}

// ReasonsFromWarnings turns a feature's free-text warnings ("pods: forbidden")
// into reasons.
func ReasonsFromWarnings(ws []string) []Reason {
	out := make([]Reason, 0, len(ws))
	for _, w := range ws {
		r := ReasonFor(errors.New(w), "", "")
		out = append(out, r)
	}
	return out
}

// Truncated reports a list cut at shown of total.
func Truncated(kind string, shown, total int) Reason {
	return Reason{Code: ReasonTruncated, Kind: kind, Detail: fmt.Sprintf("showing %d of %d; narrow the query", shown, total)}
}

// WithReasons is the result of a tool that failed in a way an agent can act
// on: an empty result plus the reason, rather than an opaque error.
type WithReasons struct {
	Items   []any    `json:"items"`
	Reasons []Reason `json:"reasons"`
}

// Explain returns an empty result carrying the reason for err.
func Explain(err error, kind, namespace string) WithReasons {
	return WithReasons{Items: []any{}, Reasons: []Reason{ReasonFor(err, kind, namespace)}}
}
