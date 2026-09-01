package metrics

import (
	"reflect"
	"testing"
)

func TestParseUserStatsTenants(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []string
	}{
		{
			name: "response with one opaque tenant",
			body: `[{"userID":"example-tenant","ingestionRate":42,"numSeries":100,"APIIngestionRate":42,"RuleIngestionRate":0}]`,
			want: []string{"example-tenant"},
		},
		{
			name: "multiple tenants",
			body: `[{"userID":"team-a"},{"userID":"team-b"},{"userID":"example-tenant"}]`,
			want: []string{"team-a", "team-b", "example-tenant"},
		},
		{
			name: "duplicate userIDs are collapsed",
			body: `[{"userID":"example-tenant"},{"userID":"example-tenant"}]`,
			want: []string{"example-tenant"},
		},
		{
			name: "empty userID skipped",
			body: `[{"userID":""},{"userID":"example-tenant"}]`,
			want: []string{"example-tenant"},
		},
		{
			name: "empty array",
			body: `[]`,
			want: []string{},
		},
		{
			name: "malformed json returns nil",
			body: `<html>404</html>`,
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseUserStatsTenants([]byte(tt.body))
			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseUserStatsTenants() = %v, want %v", got, tt.want)
			}
		})
	}
}
