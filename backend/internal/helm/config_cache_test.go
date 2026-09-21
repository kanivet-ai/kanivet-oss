package helm

import (
	"testing"

	"helm.sh/helm/v3/pkg/action"
)

func TestClearConfigCacheDropsConfigsBuiltWithOldCredentials(t *testing.T) {
	s := &Service{configCache: map[string]*action.Configuration{
		"cluster-a:default": new(action.Configuration),
		"cluster-b:default": new(action.Configuration),
	}}

	s.ClearConfigCache()

	if len(s.configCache) != 0 {
		t.Errorf("%d cached configs survived", len(s.configCache))
	}
}
