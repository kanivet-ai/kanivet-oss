//go:build !production

package logger

// IsProduction returns false in development builds.
// This is the default when building without the production tag.
func IsProduction() bool {
	return false
}
