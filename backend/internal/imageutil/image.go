// Package imageutil holds small, dependency-free helpers for container image
// references as Kubernetes reports them.
package imageutil

import "strings"

// DigestFromImageID extracts the content digest from a container status
// imageID. Runtimes report it in several shapes:
//
//	docker-pullable://repo/name@sha256:abc   (dockershim)
//	repo/name@sha256:abc                     (containerd, CRI-O)
//	sha256:abc                               (image config ID, not a repo digest)
//
// It returns the digest and whether it is a repository (manifest) digest. A
// bare "sha256:" ID is usually the local image ID and cannot be compared with
// a registry digest, so repoDigest is false for it.
func DigestFromImageID(imageID string) (digest string, repoDigest bool) {
	id := strings.TrimSpace(imageID)
	if i := strings.Index(id, "://"); i >= 0 {
		id = id[i+3:]
	}
	if i := strings.LastIndex(id, "@"); i >= 0 {
		d := id[i+1:]
		if IsDigest(d) {
			return d, true
		}
		return "", false
	}
	if IsDigest(id) {
		return id, false
	}
	return "", false
}

// RepoFromImageID returns the repository part of a repo-digest imageID
// ("docker-pullable://repo/name@sha256:..." -> "repo/name"), or "".
func RepoFromImageID(imageID string) string {
	id := strings.TrimSpace(imageID)
	if i := strings.Index(id, "://"); i >= 0 {
		id = id[i+3:]
	}
	if i := strings.LastIndex(id, "@"); i > 0 {
		return id[:i]
	}
	return ""
}

// IsDigest reports whether s looks like "<algo>:<hex>".
func IsDigest(s string) bool {
	algo, hex, ok := strings.Cut(s, ":")
	if !ok || algo == "" || len(hex) < 32 {
		return false
	}
	for _, c := range hex {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// Split breaks an image reference into repository, tag and digest. The tag is
// empty when none is given.
func Split(ref string) (repo, tag, digest string) {
	r := strings.TrimSpace(ref)
	if i := strings.Index(r, "@"); i >= 0 {
		digest = r[i+1:]
		r = r[:i]
	}
	// A colon after the last slash is a tag; one before it is a registry port.
	slash := strings.LastIndex(r, "/")
	if c := strings.LastIndex(r, ":"); c > slash {
		tag = r[c+1:]
		r = r[:c]
	}
	return r, tag, digest
}

// Short returns a compact display form: the last path segment plus the tag,
// or a 12-char digest when there is no tag.
func Short(ref string) string {
	repo, tag, digest := Split(ref)
	name := repo
	if i := strings.LastIndex(repo, "/"); i >= 0 {
		name = repo[i+1:]
	}
	switch {
	case tag != "":
		return name + ":" + tag
	case digest != "":
		_, hex, _ := strings.Cut(digest, ":")
		if len(hex) > 12 {
			hex = hex[:12]
		}
		return name + "@" + hex
	}
	return name
}

// WithDigest returns repo@digest for a reference, dropping its tag.
func WithDigest(ref, digest string) string {
	repo, _, _ := Split(ref)
	return repo + "@" + digest
}
