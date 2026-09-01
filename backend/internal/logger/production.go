//go:build production

package logger

// IsProduction returns true in production builds.
// This file is only included when building with: go build -tags production
func IsProduction() bool {
	return true
}
