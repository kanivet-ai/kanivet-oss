package helm

import (
	"errors"
	"fmt"
	"testing"

	"helm.sh/helm/v3/pkg/storage/driver"
)

// The watch tells a pruned revision from an uninstalled release by whether a
// read of the release finds no revision, which Helm reports two ways.
func TestIsReleaseNotFound(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{driver.ErrReleaseNotFound, true},
		{fmt.Errorf("query: %w", driver.ErrReleaseNotFound), true},
		{errors.New(`no revision for release "app"`), true},
		{errors.New("connection refused"), false},
	} {
		if got := isReleaseNotFound(tc.err); got != tc.want {
			t.Errorf("isReleaseNotFound(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
	if !errors.Is(fmt.Errorf("release app not found: %w", ErrReleaseNotFound), driver.ErrReleaseNotFound) {
		t.Error("ErrReleaseNotFound does not match the driver's error")
	}
}
