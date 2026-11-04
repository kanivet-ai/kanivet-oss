package mcp

import (
	"context"
	"errors"
	"fmt"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/kanivet/backend/internal/mcp/mcpreg"
)

func TestReasonFor(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{apierrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, "", errors.New("denied")), ReasonUserForbidden},
		{apierrors.NewNotFound(schema.GroupResource{Resource: "pods"}, "web"), ReasonObjectNotFound},
		{apierrors.NewNotFound(schema.GroupResource{Resource: "namespaces"}, "nope"), ReasonNamespaceNotFound},
		{apierrors.NewUnauthorized("token expired"), ReasonCredentialsRejected},
		{fmt.Errorf("%w %q", ErrKindUnknown, "Deploymnt"), ReasonKindUnknown},
		{errors.New(`no matches for kind "Rollout" in version "argoproj.io/v1alpha1"`), ReasonKindNotInstalled},
		{errors.New("dial tcp 10.0.0.1:443: connect: connection refused"), ReasonClusterDisconnected},
		{context.DeadlineExceeded, ReasonClusterDisconnected},
		{errors.New("something odd"), ReasonReadError},
	}
	for _, c := range cases {
		if got := ReasonFor(c.err, "", "").Code; got != c.want {
			t.Errorf("%v: got %s, want %s", c.err, got, c.want)
		}
	}
}

type kindNsArgs struct {
	mcpreg.ClusterArg
	Kind      string
	Namespace string
}

func TestExplainedWrapsErrors(t *testing.T) {
	h := Explained(func(ctx context.Context, c *mcpreg.Call, in kindNsArgs) (any, error) {
		return nil, apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "", errors.New("no"))
	})
	out, err := h(context.Background(), &mcpreg.Call{}, kindNsArgs{Kind: "Pod", Namespace: "prod"})
	if err != nil {
		t.Fatal(err)
	}
	w := out.(WithReasons)
	if len(w.Items) != 0 || w.Reasons[0].Code != ReasonUserForbidden || w.Reasons[0].Kind != "Pod" || w.Reasons[0].Namespace != "prod" {
		t.Fatalf("%+v", w)
	}
}
