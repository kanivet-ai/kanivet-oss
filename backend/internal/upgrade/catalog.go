package upgrade

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// Deprecation is one deprecated (and usually removed) API version of a kind,
// as listed in the Kubernetes deprecation guide.
type Deprecation struct {
	Group        string `json:"group"`
	Version      string `json:"version"`
	Kind         string `json:"kind"`
	Resource     string `json:"resource"`
	DeprecatedIn string `json:"deprecatedIn"`
	// RemovedIn is empty when no removal is scheduled.
	RemovedIn string `json:"removedIn"`
	// Replacement is the apiVersion to migrate to; empty when the API has none
	// (PodSecurityPolicy).
	Replacement string `json:"replacement"`
	Hint        string `json:"hint"`
}

// APIVersion is the group/version string of the deprecated API.
func (d Deprecation) APIVersion() string {
	if d.Group == "" {
		return d.Version
	}
	return d.Group + "/" + d.Version
}

// Catalog is the embedded, versioned deprecation table.
type Catalog struct {
	Source        string        `json:"source"`
	Reviewed      string        `json:"reviewed"`
	LatestRelease string        `json:"latestRelease"`
	APIs          []Deprecation `json:"apis"`
}

//go:embed catalog.json
var catalogJSON []byte

var (
	catalogOnce sync.Once
	catalog     *Catalog
	catalogErr  error
)

// DefaultCatalog returns the embedded catalog. It panics only if the embedded
// file is malformed, which the tests rule out.
func DefaultCatalog() *Catalog {
	catalogOnce.Do(func() {
		catalog, catalogErr = parseCatalog(catalogJSON)
	})
	if catalogErr != nil {
		panic(catalogErr)
	}
	return catalog
}

func parseCatalog(raw []byte) (*Catalog, error) {
	var c Catalog
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("upgrade catalog: %w", err)
	}
	if _, err := ParseVersion(c.LatestRelease); err != nil {
		return nil, fmt.Errorf("upgrade catalog latestRelease: %w", err)
	}
	for i, d := range c.APIs {
		if d.Kind == "" || d.Version == "" || d.Resource == "" {
			return nil, fmt.Errorf("upgrade catalog entry %d: kind, version and resource are required", i)
		}
		if _, err := ParseVersion(d.DeprecatedIn); err != nil {
			return nil, fmt.Errorf("upgrade catalog entry %d deprecatedIn: %w", i, err)
		}
		if d.RemovedIn != "" {
			if _, err := ParseVersion(d.RemovedIn); err != nil {
				return nil, fmt.Errorf("upgrade catalog entry %d removedIn: %w", i, err)
			}
		}
	}
	return &c, nil
}

// Lookup returns the catalog entry for an apiVersion and kind, if deprecated.
func (c *Catalog) Lookup(apiVersion, kind string) (Deprecation, bool) {
	for _, d := range c.APIs {
		if d.Kind == kind && d.APIVersion() == apiVersion {
			return d, true
		}
	}
	return Deprecation{}, false
}

// LookupResource returns the entry for a group, version and plural resource.
func (c *Catalog) LookupResource(group, version, resource string) (Deprecation, bool) {
	for _, d := range c.APIs {
		if d.Group == group && d.Version == version && d.Resource == resource {
			return d, true
		}
	}
	return Deprecation{}, false
}

// Version is a Kubernetes minor release, e.g. 1.31.
type Version struct {
	Major int `json:"major"`
	Minor int `json:"minor"`
}

func (v Version) String() string { return fmt.Sprintf("%d.%d", v.Major, v.Minor) }

// Less reports whether v is an older release than o.
func (v Version) Less(o Version) bool {
	if v.Major != o.Major {
		return v.Major < o.Major
	}
	return v.Minor < o.Minor
}

// AtLeast reports whether v is o or newer.
func (v Version) AtLeast(o Version) bool { return !v.Less(o) }

// IsZero reports whether the version is unset.
func (v Version) IsZero() bool { return v.Major == 0 && v.Minor == 0 }

// ParseVersion reads "1.29", "v1.29.3" or "v1.29.3-eks-1234" (and GKE/AKS
// suffixes such as "+"), keeping major and minor.
func ParseVersion(s string) (Version, error) {
	t := strings.TrimSpace(s)
	t = strings.TrimPrefix(t, "v")
	if i := strings.IndexAny(t, "-+"); i >= 0 {
		t = t[:i]
	}
	parts := strings.Split(t, ".")
	if len(parts) < 2 {
		return Version{}, fmt.Errorf("invalid Kubernetes version %q", s)
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return Version{}, fmt.Errorf("invalid Kubernetes version %q", s)
	}
	// Some distributions report the minor as "29+".
	minorStr := strings.TrimRight(parts[1], "+")
	minor, err := strconv.Atoi(minorStr)
	if err != nil {
		return Version{}, fmt.Errorf("invalid Kubernetes version %q", s)
	}
	return Version{Major: major, Minor: minor}, nil
}

func mustVersion(s string) Version {
	v, _ := ParseVersion(s)
	return v
}

// TargetsFrom lists the releases a cluster on current can be planned to, from
// the next minor up to the latest release the catalog knows.
func (c *Catalog) TargetsFrom(current Version) []string {
	latest := mustVersion(c.LatestRelease)
	var out []string
	for m := current.Minor + 1; m <= latest.Minor && current.Major == latest.Major; m++ {
		out = append(out, Version{Major: current.Major, Minor: m}.String())
	}
	return out
}
