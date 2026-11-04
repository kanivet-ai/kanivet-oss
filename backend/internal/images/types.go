package images

import (
	"errors"
	"fmt"
)

// PullContext says where an image runs, so the service can authenticate the
// way the kubelet would and pick the node's platform. Every field is optional.
// Only names travel from the UI; secret contents are read server-side and
// never returned.
type PullContext struct {
	Cluster   string `json:"cluster,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	// Pod, when set, contributes its imagePullSecrets, service account and
	// node (for the platform).
	Pod string `json:"pod,omitempty"`
	// Container selects the container whose status imageID gives the running
	// digest. Ignored without Pod.
	Container string `json:"container,omitempty"`
	// PullSecrets are extra secret names in Namespace (e.g. from a workload's
	// pod template).
	PullSecrets []string `json:"pullSecrets,omitempty"`
	// ServiceAccount contributes its imagePullSecrets when Pod is not given.
	ServiceAccount string `json:"serviceAccount,omitempty"`
	// ImageID is a container status imageID, when the caller already has it.
	ImageID string `json:"imageID,omitempty"`
	// Platform forces "os/arch[/variant]" instead of the node's.
	Platform string `json:"platform,omitempty"`
}

// ImageInfo is everything about an image that needs only the manifest and
// config blob (no layer downloads).
type ImageInfo struct {
	Ref string `json:"ref"`
	// ResolvedRef is the reference actually fetched (repo@digest when the
	// running digest was known).
	ResolvedRef string `json:"resolvedRef"`
	// Digest of the platform image manifest.
	Digest string `json:"digest"`
	// IndexDigest is set for multi-arch images.
	IndexDigest string     `json:"indexDigest,omitempty"`
	MediaType   string     `json:"mediaType"`
	Platform    string     `json:"platform"`
	Platforms   []string   `json:"platforms,omitempty"`
	Created     string     `json:"created,omitempty"`
	Size        int64      `json:"size"` // compressed layers + config
	Config      Config     `json:"config"`
	Layers      []Layer    `json:"layers"`
	History     []History  `json:"history,omitempty"`
	Tag         *TagStatus `json:"tag,omitempty"`
	AuthMethod  string     `json:"authMethod"`
	Indexed     bool       `json:"indexed"` // filesystem index ready on disk
}

// Config is the runnable part of the image config, with secrets scrubbed.
type Config struct {
	Entrypoint   []string          `json:"entrypoint,omitempty"`
	Cmd          []string          `json:"cmd,omitempty"`
	Env          []EnvVar          `json:"env,omitempty"`
	User         string            `json:"user,omitempty"`
	WorkingDir   string            `json:"workingDir,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	ExposedPorts []string          `json:"exposedPorts,omitempty"`
	Volumes      []string          `json:"volumes,omitempty"`
	StopSignal   string            `json:"stopSignal,omitempty"`
	OS           string            `json:"os,omitempty"`
	Architecture string            `json:"architecture,omitempty"`
	Author       string            `json:"author,omitempty"`
}

type EnvVar struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Redacted bool   `json:"redacted,omitempty"`
}

// Layer is one filesystem layer with the history entry that produced it.
type Layer struct {
	Index     int    `json:"index"`
	Digest    string `json:"digest"`
	DiffID    string `json:"diffID,omitempty"`
	Size      int64  `json:"size"`
	MediaType string `json:"mediaType"`
	CreatedBy string `json:"createdBy,omitempty"`
	Created   string `json:"created,omitempty"`
	Comment   string `json:"comment,omitempty"`
}

// History is a raw history entry, including empty (metadata-only) ones.
type History struct {
	CreatedBy  string `json:"createdBy,omitempty"`
	Created    string `json:"created,omitempty"`
	EmptyLayer bool   `json:"emptyLayer,omitempty"`
	LayerIndex int    `json:"layerIndex"` // -1 for empty layers
}

// TagStatus compares the running digest with the tag's current digest.
type TagStatus struct {
	Tag           string `json:"tag"`
	CurrentDigest string `json:"currentDigest,omitempty"`
	RunningDigest string `json:"runningDigest,omitempty"`
	// Moved is true only when both digests are known and differ.
	Moved bool   `json:"moved"`
	Error string `json:"error,omitempty"`
}

// FileEntry is a node of the merged filesystem.
type FileEntry struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Type       string `json:"type"` // file, dir, symlink, hardlink, other
	Size       int64  `json:"size"`
	Mode       string `json:"mode"`
	LinkTarget string `json:"linkTarget,omitempty"`
	Layer      int    `json:"layer"`            // layer that last wrote it
	Children   int    `json:"children,omitempty"` // direct children (dirs)
	TotalSize  int64  `json:"totalSize,omitempty"` // recursive (dirs)
}

// DirListing is the content of one directory.
type DirListing struct {
	Path    string      `json:"path"`
	Entries []FileEntry `json:"entries"`
	Files   int         `json:"totalFiles"`
	Size    int64       `json:"totalSize"`
}

// Change is one entry a layer added, modified or deleted.
type Change struct {
	Path string `json:"path"`
	Op   string `json:"op"` // added, modified, deleted
	Type string `json:"type"`
	Size int64  `json:"size"`
}

type LayerChanges struct {
	Layer     int      `json:"layer"`
	Changes   []Change `json:"changes"`
	Truncated bool     `json:"truncated,omitempty"`
	Added     int      `json:"added"`
	Modified  int      `json:"modified"`
	Deleted   int      `json:"deleted"`
}

// FileContent is a preview of one file.
type FileContent struct {
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	Content   string `json:"content,omitempty"`
	Binary    bool   `json:"binary"`
	Truncated bool   `json:"truncated"`
	Layer     int    `json:"layer"`
}

// SearchResult lists merged-tree entries whose path matches a query.
type SearchResult struct {
	Query     string      `json:"query"`
	Entries   []FileEntry `json:"entries"`
	Truncated bool        `json:"truncated,omitempty"`
}

// ErrorKind classifies registry failures so the UI can show a useful state.
type ErrorKind string

const (
	ErrAuth        ErrorKind = "auth"
	ErrNotFound    ErrorKind = "not_found"
	ErrUnreachable ErrorKind = "unreachable"
	ErrInvalid     ErrorKind = "invalid"
	ErrTooLarge    ErrorKind = "too_large"
	ErrInternal    ErrorKind = "internal"
)

// Error is returned by every Service method for user-visible failures.
type Error struct {
	Kind    ErrorKind `json:"kind"`
	Message string    `json:"error"`
	Err     error     `json:"-"`
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Err)
	}
	return e.Message
}

func (e *Error) Unwrap() error { return e.Err }

func newErr(kind ErrorKind, msg string, err error) *Error {
	return &Error{Kind: kind, Message: msg, Err: err}
}

// KindOf returns the ErrorKind of err, or ErrInternal.
func KindOf(err error) ErrorKind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return ErrInternal
}
