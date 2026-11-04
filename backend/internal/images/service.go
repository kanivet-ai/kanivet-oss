package images

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/kanivet/backend/internal/imageutil"
)

// ClusterClients returns a typed client for a cluster (k8s.Interface fits).
type ClusterClients interface {
	GetClientForCluster(cluster string) (kubernetes.Interface, error)
}

// PodLister lists a cluster's pods from the shared watch cache
// (podcache.Cache fits).
type PodLister interface {
	List(ctx context.Context, cluster string) ([]*corev1.Pod, error)
}

const (
	maxHandles        = 8
	handleTTL         = 30 * time.Minute
	defaultPreview    = 512 << 10
	maxPreview        = 4 << 20
	searchLimit       = 500
	indexConcurrency  = 3
	defaultPlatformOS = "linux"
)

// Service inspects container images in registries. It is safe for
// concurrent use.
type Service struct {
	clients ClusterClients
	pods    PodLister
	cache   *diskCache
	// extra remote options (tests inject a transport).
	remoteOpts []remote.Option

	mu      sync.Mutex
	handles map[string]*handle
}

// NewService creates a service caching layers under cacheDir (capped at
// maxBytes; 0 means DefaultCacheBytes). clients and pods may be nil, in
// which case pull context is ignored.
func NewService(clients ClusterClients, pods PodLister, cacheDir string, maxBytes int64) (*Service, error) {
	dc, err := newDiskCache(cacheDir, maxBytes)
	if err != nil {
		return nil, err
	}
	return &Service{clients: clients, pods: pods, cache: dc, handles: map[string]*handle{}}, nil
}

// handle is one resolved image with its registry session, kept so file
// reads reuse the same auth and manifest.
type handle struct {
	key     string
	info    *ImageInfo
	img     v1.Image
	layers  []v1.Layer
	used    time.Time
	ctx     context.Context
	cancel  context.CancelFunc
	treeMu  sync.Mutex
	tree    *tree
	treeErr error
	indexing bool
	done    chan struct{}

	progMu      sync.Mutex
	layersDone  int
	bytesDone   int64
}

// IndexStatus reports filesystem indexing progress.
type IndexStatus struct {
	Ready       bool   `json:"ready"`
	Indexing    bool   `json:"indexing"`
	LayersDone  int    `json:"layersDone"`
	LayersTotal int    `json:"layersTotal"`
	BytesDone   int64  `json:"bytesDone"`
	BytesTotal  int64  `json:"bytesTotal"`
	Error       string `json:"error,omitempty"`
}

// ImageInfo fetches manifest and config (no layers) for ref.
func (s *Service) ImageInfo(ctx context.Context, ref string, pc PullContext) (*ImageInfo, error) {
	h, err := s.resolve(ctx, ref, pc)
	if err != nil {
		return nil, err
	}
	info := *h.info
	info.Indexed = h.treeReady() || s.allIndexed(h)
	return &info, nil
}

// ListDir lists one directory of the merged filesystem, indexing layers on
// first use.
func (s *Service) ListDir(ctx context.Context, ref string, pc PullContext, dir string) (*DirListing, error) {
	t, _, err := s.tree(ctx, ref, pc)
	if err != nil {
		return nil, err
	}
	l, ok := t.list(dir)
	if !ok {
		return nil, newErr(ErrNotFound, fmt.Sprintf("%s is not a directory in this image", dir), nil)
	}
	return l, nil
}

// Search finds merged-filesystem paths containing q.
func (s *Service) Search(ctx context.Context, ref string, pc PullContext, q string) (*SearchResult, error) {
	t, _, err := s.tree(ctx, ref, pc)
	if err != nil {
		return nil, err
	}
	return t.search(q, searchLimit), nil
}

// LayerChanges lists what one layer added, modified and deleted.
func (s *Service) LayerChanges(ctx context.Context, ref string, pc PullContext, layer int) (*LayerChanges, error) {
	t, _, err := s.tree(ctx, ref, pc)
	if err != nil {
		return nil, err
	}
	if layer < 0 || layer >= len(t.changes) {
		return nil, newErr(ErrInvalid, "no such layer", nil)
	}
	lc := t.changes[layer]
	return &lc, nil
}

// Status reports indexing progress without starting it.
func (s *Service) Status(ctx context.Context, ref string, pc PullContext) (*IndexStatus, error) {
	h, err := s.resolve(ctx, ref, pc)
	if err != nil {
		return nil, err
	}
	st := &IndexStatus{LayersTotal: len(h.layers)}
	for _, l := range h.info.Layers {
		st.BytesTotal += l.Size
	}
	h.treeMu.Lock()
	st.Ready = h.tree != nil
	st.Indexing = h.indexing
	if h.treeErr != nil {
		st.Error = h.treeErr.Error()
	}
	h.treeMu.Unlock()
	h.progMu.Lock()
	st.LayersDone, st.BytesDone = h.layersDone, h.bytesDone
	h.progMu.Unlock()
	return st, nil
}

// ReadFile previews a file of the merged filesystem. limit <= 0 uses the
// default preview size.
func (s *Service) ReadFile(ctx context.Context, ref string, pc PullContext, p string, limit int64) (*FileContent, error) {
	t, h, err := s.tree(ctx, ref, pc)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = defaultPreview
	}
	if limit > maxPreview {
		limit = maxPreview
	}
	n, real := t.resolve(cleanPath(p), true, 0)
	if n == nil {
		return nil, newErr(ErrNotFound, "file not found in image", nil)
	}
	if n.typ == "hardlink" {
		real = n.link
		if n = t.lookup(n.link, true); n == nil {
			return nil, newErr(ErrNotFound, "hard link target not found", nil)
		}
	}
	if n.children != nil || n.typ != "file" {
		return nil, newErr(ErrInvalid, "not a regular file", nil)
	}
	fc := &FileContent{Path: "/" + real, Size: n.size, Layer: n.layer}
	data, err := s.readFromLayer(ctx, h, n.layer, real, limit)
	if err != nil {
		return nil, err
	}
	fc.Truncated = int64(len(data)) < n.size
	if looksBinary(data) {
		fc.Binary = true
		return fc, nil
	}
	fc.Content = string(trimPartialRune(data))
	return fc, nil
}

// tree returns the merged filesystem, indexing (and caching) layers once.
func (s *Service) tree(ctx context.Context, ref string, pc PullContext) (*tree, *handle, error) {
	h, err := s.resolve(ctx, ref, pc)
	if err != nil {
		return nil, nil, err
	}
	h.treeMu.Lock()
	if h.tree != nil {
		t := h.tree
		h.treeMu.Unlock()
		return t, h, nil
	}
	if !h.indexing {
		h.indexing = true
		h.treeErr = nil
		h.done = make(chan struct{})
		go s.buildTree(h)
	}
	done := h.done
	h.treeMu.Unlock()
	select {
	case <-done:
	case <-ctx.Done():
		return nil, nil, newErr(ErrUnreachable, "indexing is still running; try again shortly", ctx.Err())
	}
	h.treeMu.Lock()
	defer h.treeMu.Unlock()
	if h.treeErr != nil {
		return nil, nil, h.treeErr
	}
	return h.tree, h, nil
}

func (h *handle) treeReady() bool {
	h.treeMu.Lock()
	defer h.treeMu.Unlock()
	return h.tree != nil
}

func (s *Service) allIndexed(h *handle) bool {
	for _, l := range h.info.Layers {
		if !s.cache.hasIndex(l.Digest) {
			return false
		}
	}
	return true
}

// buildTree runs detached from any request so a navigation away does not
// waste a half-finished download; it stops when the handle is evicted.
func (s *Service) buildTree(h *handle) {
	entries := make([][]layerEntry, len(h.layers))
	errs := make([]error, len(h.layers))
	sem := make(chan struct{}, indexConcurrency)
	var wg sync.WaitGroup
	for i, l := range h.layers {
		wg.Add(1)
		go func(i int, l v1.Layer) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			entries[i], errs[i] = s.indexLayer(h, l, h.info.Layers[i].Size)
			h.progMu.Lock()
			h.layersDone++
			h.progMu.Unlock()
		}(i, l)
	}
	wg.Wait()
	var err error
	for _, e := range errs {
		if e != nil {
			err = classify(e, "could not read image layers")
			break
		}
	}
	var t *tree
	if err == nil {
		t = mergeLayers(entries)
	}
	h.treeMu.Lock()
	h.tree, h.treeErr, h.indexing = t, err, false
	close(h.done)
	h.treeMu.Unlock()
}

type countingReader struct {
	r io.Reader
	h *handle
}

func (c countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		c.h.progMu.Lock()
		c.h.bytesDone += int64(n)
		c.h.progMu.Unlock()
	}
	return n, err
}

// indexLayer returns a layer's entries from the disk index, or streams the
// layer once, caching the blob and writing the index as it goes.
func (s *Service) indexLayer(h *handle, l v1.Layer, size int64) ([]layerEntry, error) {
	dg, err := l.Digest()
	if err != nil {
		return nil, err
	}
	digest := dg.String()
	if e, ok := s.cache.loadIndex(digest); ok {
		h.progMu.Lock()
		h.bytesDone += size
		h.progMu.Unlock()
		return e, nil
	}
	var src io.Reader
	var closer io.Closer
	var bw *blobWriter
	if f, ok := s.cache.openBlob(digest); ok {
		src, closer = f, f
	} else {
		rc, err := l.Compressed()
		if err != nil {
			return nil, err
		}
		bw = s.cache.newBlobWriter(digest, size)
		if bw != nil {
			src = io.TeeReader(rc, bw)
		} else {
			src = rc
		}
		closer = rc
	}
	defer closer.Close()
	ur, err := decompress(countingReader{src, h})
	if err != nil {
		bw.abort()
		return nil, err
	}
	entries, err := indexTar(ur)
	_ = ur.Close()
	if err != nil {
		bw.abort()
		return nil, err
	}
	if h.ctx.Err() != nil {
		bw.abort()
		return nil, h.ctx.Err()
	}
	// Drain trailing padding so the digest check sees the whole blob.
	if bw != nil {
		_, _ = io.Copy(io.Discard, src)
		bw.commit()
	}
	_ = s.cache.saveIndex(digest, entries)
	return entries, nil
}

// readFromLayer streams one layer to the named entry and returns up to
// limit bytes of it.
func (s *Service) readFromLayer(ctx context.Context, h *handle, layer int, p string, limit int64) ([]byte, error) {
	l := h.layers[layer]
	dg, err := l.Digest()
	if err != nil {
		return nil, classify(err, "could not read layer")
	}
	var src io.ReadCloser
	if f, ok := s.cache.openBlob(dg.String()); ok {
		src = f
	} else if src, err = l.Compressed(); err != nil {
		return nil, classify(err, "could not download layer")
	}
	defer src.Close()
	ur, err := decompress(src)
	if err != nil {
		return nil, classify(err, "could not decompress layer")
	}
	defer ur.Close()
	data, found, err := readTarEntry(ctx, ur, p, limit)
	if err != nil {
		return nil, classify(err, "could not read layer")
	}
	if !found {
		return nil, newErr(ErrNotFound, "file not found in its layer", nil)
	}
	return data, nil
}

// resolve returns the cached handle for (ref, pull context) or fetches the
// manifest and config.
func (s *Service) resolve(ctx context.Context, ref string, pc PullContext) (*handle, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, newErr(ErrInvalid, "image reference is required", nil)
	}
	parsed, err := name.ParseReference(ref)
	if err != nil {
		return nil, newErr(ErrInvalid, "invalid image reference", err)
	}
	rc := s.gatherContext(ctx, ref, pc)
	key := handleKey(pc.Cluster, pc.Namespace, ref, rc)

	s.mu.Lock()
	if h, ok := s.handles[key]; ok && time.Since(h.used) < handleTTL {
		h.used = time.Now()
		s.mu.Unlock()
		return h, nil
	}
	s.mu.Unlock()

	h, err := s.fetch(ctx, ref, parsed, rc)
	if err != nil {
		return nil, err
	}
	h.key = key
	s.mu.Lock()
	if old, ok := s.handles[key]; ok {
		old.cancel()
	}
	s.handles[key] = h
	s.evictHandlesLocked()
	s.mu.Unlock()
	return h, nil
}

func (s *Service) evictHandlesLocked() {
	for len(s.handles) > maxHandles {
		var oldest *handle
		for _, h := range s.handles {
			if oldest == nil || h.used.Before(oldest.used) {
				oldest = h
			}
		}
		oldest.cancel()
		delete(s.handles, oldest.key)
	}
}

// resolvedContext is what the pull context yields after cluster lookups.
type resolvedContext struct {
	secrets       *secretKeychain
	secretNames   []string
	platform      v1.Platform
	runningDigest string
}

func handleKey(cluster, ns, ref string, rc resolvedContext) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s\x00%s\x00%s\x00%s", cluster, ns, ref, rc.platform.String(), strings.Join(rc.secretNames, ","), rc.runningDigest)
	return hex.EncodeToString(h.Sum(nil))
}

// gatherContext reads pull secrets, platform and running digest from the
// cluster. Every lookup is best effort: RBAC denials just mean fewer creds.
func (s *Service) gatherContext(ctx context.Context, ref string, pc PullContext) resolvedContext {
	rc := resolvedContext{platform: v1.Platform{OS: defaultPlatformOS, Architecture: "amd64"}}
	secretNames := append([]string(nil), pc.PullSecrets...)
	saName := pc.ServiceAccount
	nodeName := ""
	imageID := pc.ImageID

	var client kubernetes.Interface
	if s.clients != nil && pc.Cluster != "" {
		client, _ = s.clients.GetClientForCluster(pc.Cluster)
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if pc.Pod != "" && pc.Namespace != "" {
		if pod := s.findPod(lookupCtx, client, pc); pod != nil {
			for _, ps := range pod.Spec.ImagePullSecrets {
				secretNames = append(secretNames, ps.Name)
			}
			saName = pod.Spec.ServiceAccountName
			nodeName = pod.Spec.NodeName
			if imageID == "" {
				imageID = containerImageID(pod, pc.Container, ref)
			}
		}
	}
	if client != nil && pc.Namespace != "" {
		if saName == "" && pc.Pod != "" {
			saName = "default"
		}
		if saName != "" {
			if sa, err := client.CoreV1().ServiceAccounts(pc.Namespace).Get(lookupCtx, saName, metav1.GetOptions{}); err == nil {
				for _, ps := range sa.ImagePullSecrets {
					secretNames = append(secretNames, ps.Name)
				}
			}
		}
		secretNames = dedupe(secretNames)
		entries := map[string]authn.AuthConfig{}
		for _, n := range secretNames {
			sec, err := client.CoreV1().Secrets(pc.Namespace).Get(lookupCtx, n, metav1.GetOptions{})
			if err != nil {
				continue
			}
			for host, ac := range parsePullSecret(sec) {
				if _, seen := entries[host]; !seen {
					entries[host] = ac
				}
			}
		}
		rc.secrets = &secretKeychain{entries: entries}
		if nodeName != "" && pc.Platform == "" {
			if node, err := client.CoreV1().Nodes().Get(lookupCtx, nodeName, metav1.GetOptions{}); err == nil {
				rc.platform = nodePlatform(node)
			}
		}
	}
	rc.secretNames = secretNames
	if pc.Platform != "" {
		if p, err := v1.ParsePlatform(pc.Platform); err == nil {
			rc.platform = *p
		}
	}
	if d, repo := imageutil.DigestFromImageID(imageID); repo {
		rc.runningDigest = d
	}
	return rc
}

func (s *Service) findPod(ctx context.Context, client kubernetes.Interface, pc PullContext) *corev1.Pod {
	if s.pods != nil {
		if pods, err := s.pods.List(ctx, pc.Cluster); err == nil {
			for _, p := range pods {
				if p.Namespace == pc.Namespace && p.Name == pc.Pod {
					return p
				}
			}
		}
	}
	if client != nil {
		if p, err := client.CoreV1().Pods(pc.Namespace).Get(ctx, pc.Pod, metav1.GetOptions{}); err == nil {
			return p
		}
	}
	return nil
}

// containerImageID picks the status imageID of the named container, or of
// the first container running ref.
func containerImageID(pod *corev1.Pod, container, ref string) string {
	statuses := append(append([]corev1.ContainerStatus{}, pod.Status.InitContainerStatuses...), pod.Status.ContainerStatuses...)
	statuses = append(statuses, pod.Status.EphemeralContainerStatuses...)
	for _, st := range statuses {
		if container != "" && st.Name == container {
			return st.ImageID
		}
	}
	if container == "" {
		for _, st := range statuses {
			if st.Image == ref {
				return st.ImageID
			}
		}
	}
	return ""
}

func nodePlatform(n *corev1.Node) v1.Platform {
	p := v1.Platform{OS: n.Status.NodeInfo.OperatingSystem, Architecture: n.Status.NodeInfo.Architecture}
	if p.OS == "" {
		p.OS = n.Labels["kubernetes.io/os"]
	}
	if p.Architecture == "" {
		p.Architecture = n.Labels["kubernetes.io/arch"]
	}
	if p.OS == "" {
		p.OS = defaultPlatformOS
	}
	if p.Architecture == "" {
		p.Architecture = "amd64"
	}
	return p
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

type authLabel struct {
	mu    sync.Mutex
	label string
}

func (a *authLabel) get() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.label == "" {
		return "anonymous"
	}
	return a.label
}

// fetch resolves the manifest (picking the platform from an index) and the
// config, without downloading layers.
func (s *Service) fetch(ctx context.Context, ref string, parsed name.Reference, rc resolvedContext) (*handle, error) {
	hctx, cancel := context.WithCancel(context.Background())
	label := &authLabel{}
	kc := buildKeychain(hctx, rc.secrets, label)
	// The session outlives this request (file reads reuse it), so remote
	// calls get the handle's context; the request ctx bounds the wait here.
	opts := append([]remote.Option{remote.WithAuthFromKeychain(kc), remote.WithContext(hctx), remote.WithPlatform(rc.platform)}, s.remoteOpts...)

	type result struct {
		h   *handle
		err error
	}
	ch := make(chan result, 1)
	go func() {
		h, err := s.fetchSync(ref, parsed, rc, opts, label)
		ch <- result{h, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			cancel()
			return nil, r.err
		}
		r.h.ctx, r.h.cancel, r.h.used = hctx, cancel, time.Now()
		return r.h, nil
	case <-ctx.Done():
		cancel()
		return nil, newErr(ErrUnreachable, "timed out contacting the registry", ctx.Err())
	}
}

func (s *Service) fetchSync(ref string, parsed name.Reference, rc resolvedContext, opts []remote.Option, label *authLabel) (*handle, error) {
	target := parsed
	if rc.runningDigest != "" {
		if d, err := name.NewDigest(parsed.Context().Name() + "@" + rc.runningDigest); err == nil {
			target = d
		}
	}
	desc, err := remote.Get(target, opts...)
	if err != nil && target != parsed {
		// The running digest may be gone from the registry; fall back to ref.
		target = parsed
		desc, err = remote.Get(target, opts...)
	}
	if err != nil {
		return nil, classify(err, "could not fetch the image manifest")
	}
	info := &ImageInfo{Ref: ref, ResolvedRef: target.Name()}
	var img v1.Image
	if desc.MediaType.IsIndex() {
		idx, err := desc.ImageIndex()
		if err != nil {
			return nil, classify(err, "could not read the image index")
		}
		im, err := idx.IndexManifest()
		if err != nil {
			return nil, classify(err, "could not read the image index")
		}
		info.IndexDigest = desc.Digest.String()
		var chosen *v1.Descriptor
		for i := range im.Manifests {
			m := &im.Manifests[i]
			if m.Platform == nil || m.Platform.Architecture == "unknown" {
				continue // attestation manifests
			}
			info.Platforms = append(info.Platforms, m.Platform.String())
			if chosen == nil && m.Platform.Satisfies(rc.platform) {
				chosen = m
			}
		}
		if chosen == nil {
			return nil, newErr(ErrNotFound, fmt.Sprintf("image has no %s variant (available: %s)", rc.platform.String(), strings.Join(info.Platforms, ", ")), nil)
		}
		if img, err = idx.Image(chosen.Digest); err != nil {
			return nil, classify(err, "could not read the platform manifest")
		}
	} else if img, err = desc.Image(); err != nil {
		return nil, classify(err, "could not read the image manifest")
	}

	dg, err := img.Digest()
	if err != nil {
		return nil, classify(err, "could not read the image manifest")
	}
	mt, _ := img.MediaType()
	cfg, err := img.ConfigFile()
	if err != nil {
		return nil, classify(err, "could not read the image config")
	}
	layers, err := img.Layers()
	if err != nil {
		return nil, classify(err, "could not read the image layers")
	}
	info.Digest = dg.String()
	info.MediaType = string(mt)
	info.Platform = (&v1.Platform{OS: cfg.OS, Architecture: cfg.Architecture, Variant: cfg.Variant}).String()
	if !cfg.Created.IsZero() {
		info.Created = cfg.Created.UTC().Format(time.RFC3339)
	}
	if m, err := img.Manifest(); err == nil {
		info.Size = m.Config.Size
	}
	info.Config = buildConfig(cfg)
	info.Layers, info.History = buildLayers(cfg, layers)
	for _, l := range info.Layers {
		info.Size += l.Size
	}
	info.AuthMethod = label.get()
	info.Tag = s.tagStatus(ref, parsed, rc, info, opts)
	return &handle{info: info, img: img, layers: layers}, nil
}

// tagStatus compares the tag's current digest with the running one.
func (s *Service) tagStatus(ref string, parsed name.Reference, rc resolvedContext, info *ImageInfo, opts []remote.Option) *TagStatus {
	_, tag, digest := imageutil.Split(ref)
	if tag == "" {
		if digest != "" {
			return nil // pinned by digest: nothing can move
		}
		tag = "latest"
	}
	tagRef := parsed.Context().Tag(tag)
	ts := &TagStatus{Tag: tag, RunningDigest: rc.runningDigest}
	head, err := remote.Head(tagRef, opts...)
	if err != nil {
		ts.Error = classify(err, "could not check the tag").Message
		return ts
	}
	ts.CurrentDigest = head.Digest.String()
	if rc.runningDigest == "" {
		return ts
	}
	run := rc.runningDigest
	if run == ts.CurrentDigest {
		return ts
	}
	// The runtime may report the platform manifest while the tag points at
	// an index (or the reverse); only call it moved if the index no longer
	// contains what runs.
	if head.MediaType.IsIndex() {
		if desc, err := remote.Get(tagRef, opts...); err == nil {
			if idx, err := desc.ImageIndex(); err == nil {
				if im, err := idx.IndexManifest(); err == nil {
					for _, m := range im.Manifests {
						if m.Digest.String() == run {
							return ts
						}
					}
				}
			}
		}
	}
	ts.Moved = true
	return ts
}

func buildConfig(cfg *v1.ConfigFile) Config {
	c := cfg.Config
	out := Config{
		Entrypoint:   c.Entrypoint,
		Cmd:          c.Cmd,
		Env:          scrubEnv(c.Env),
		User:         c.User,
		WorkingDir:   c.WorkingDir,
		Labels:       c.Labels,
		StopSignal:   c.StopSignal,
		OS:           cfg.OS,
		Architecture: cfg.Architecture,
		Author:       cfg.Author,
	}
	for p := range c.ExposedPorts {
		out.ExposedPorts = append(out.ExposedPorts, p)
	}
	sort.Strings(out.ExposedPorts)
	for v := range c.Volumes {
		out.Volumes = append(out.Volumes, v)
	}
	sort.Strings(out.Volumes)
	return out
}

// buildLayers pairs layers with the non-empty history entries that made them.
func buildLayers(cfg *v1.ConfigFile, layers []v1.Layer) ([]Layer, []History) {
	out := make([]Layer, len(layers))
	for i, l := range layers {
		out[i].Index = i
		if d, err := l.Digest(); err == nil {
			out[i].Digest = d.String()
		}
		if d, err := l.DiffID(); err == nil {
			out[i].DiffID = d.String()
		}
		if sz, err := l.Size(); err == nil {
			out[i].Size = sz
		}
		if mt, err := l.MediaType(); err == nil {
			out[i].MediaType = string(mt)
		}
	}
	hist := make([]History, 0, len(cfg.History))
	li := 0
	for _, h := range cfg.History {
		e := History{CreatedBy: scrubText(h.CreatedBy), EmptyLayer: h.EmptyLayer, LayerIndex: -1}
		if !h.Created.IsZero() {
			e.Created = h.Created.UTC().Format(time.RFC3339)
		}
		if !h.EmptyLayer && li < len(out) {
			e.LayerIndex = li
			out[li].CreatedBy = e.CreatedBy
			out[li].Created = e.Created
			out[li].Comment = h.Comment
			li++
		}
		hist = append(hist, e)
	}
	return out, hist
}

// classify maps registry and network errors to an ErrorKind.
func classify(err error, msg string) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	var te *transport.Error
	if errors.As(err, &te) {
		switch te.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return newErr(ErrAuth, "the registry refused access: no pull secret, docker login or cloud credential grants it", err)
		case http.StatusNotFound:
			return newErr(ErrNotFound, "the image or tag does not exist in the registry", err)
		case http.StatusTooManyRequests:
			return newErr(ErrUnreachable, "the registry is rate limiting requests", err)
		}
		for _, d := range te.Errors {
			switch d.Code {
			case transport.UnauthorizedErrorCode, transport.DeniedErrorCode:
				return newErr(ErrAuth, "the registry refused access: no pull secret, docker login or cloud credential grants it", err)
			case transport.ManifestUnknownErrorCode, transport.NameUnknownErrorCode, transport.BlobUnknownErrorCode:
				return newErr(ErrNotFound, "the image or tag does not exist in the registry", err)
			}
		}
		return newErr(ErrUnreachable, msg, err)
	}
	var ne net.Error
	if errors.As(err, &ne) || errors.Is(err, context.DeadlineExceeded) {
		return newErr(ErrUnreachable, "the registry could not be reached", err)
	}
	if strings.Contains(err.Error(), "no such host") || strings.Contains(err.Error(), "connection refused") {
		return newErr(ErrUnreachable, "the registry could not be reached", err)
	}
	return newErr(ErrInternal, msg, err)
}
