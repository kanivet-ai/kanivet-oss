package api

import (
	"context"
	"errors"
	"fmt"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestCountWorthRetrying(t *testing.T) {
	gr := schema.GroupResource{Group: "gateway.networking.k8s.io", Resource: "gateways"}
	wrap := func(err error) error { return fmt.Errorf("failed to count resources: %w", err) }
	for name, tc := range map[string]struct {
		err  error
		want bool
	}{
		"resource not served":   {wrap(apierrors.NewNotFound(gr, "")), false},
		"not allowed to list":   {wrap(apierrors.NewForbidden(gr, "", errors.New("rbac"))), false},
		"list not supported":    {wrap(apierrors.NewMethodNotSupported(gr, "list")), false},
		"expired credentials":   {wrap(apierrors.NewUnauthorized("token expired")), true},
		"api server timeout":    {wrap(context.DeadlineExceeded), true},
		"credential plugin":     {errors.New("getting credentials: exec: executable aws failed with exit code 255"), true},
		"api server overloaded": {wrap(apierrors.NewTooManyRequests("slow down", 1)), true},
	} {
		if got := countWorthRetrying(tc.err); got != tc.want {
			t.Errorf("%s: countWorthRetrying = %v, want %v", name, got, tc.want)
		}
	}
}
