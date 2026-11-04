# images

Container image inspection: config, layers, merged filesystem and file
previews, fetched straight from the registry with go-containerregistry.

## Service API (wrap these as MCP tools)

```go
svc, _ := images.NewService(k8sClient, podCache, "~/.kanivet/images", 0)

svc.ImageInfo(ctx, ref, pc)                 // *ImageInfo: digest, platforms, config, layers+history, tag status
svc.ListDir(ctx, ref, pc, "/etc")           // *DirListing of the merged filesystem
svc.ReadFile(ctx, ref, pc, "/etc/os-release", 0) // *FileContent (text preview, binary flag, truncation)
svc.Search(ctx, ref, pc, "nginx.conf")      // *SearchResult (path substring, max 500)
svc.LayerChanges(ctx, ref, pc, layerIndex)  // *LayerChanges: added / modified / deleted
svc.Status(ctx, ref, pc)                    // *IndexStatus: indexing progress
```

`pc` is a `PullContext{Cluster, Namespace, Pod, Container, PullSecrets,
ServiceAccount, ImageID, Platform}`; every field is optional. Errors are
`*images.Error` with a `Kind` (`auth`, `not_found`, `unreachable`, `invalid`,
`internal`).

HTTP: `GET /api/v1/images/{info,status,files,file,search,layer-changes}` with
`ref` plus the pull-context fields as query parameters (`path`, `q`, `layer`,
`limit` where relevant).

## Behaviour

- **Auth** (first non-anonymous wins): the pod's `imagePullSecrets` and its
  service account's, read server-side and never returned; the local docker
  config and credential helpers; gcloud (GCR/Artifact Registry); AWS ECR via
  the default AWS profile; ACR via the local Azure login. `ImageInfo.AuthMethod`
  names the source used.
- **What runs**: with a pod, the container status `imageID` gives the running
  digest; that digest is inspected (not whatever the tag points at now), and
  `Tag.Moved` reports whether the tag has since moved. Multi-arch indexes
  resolve to the pod's node platform (or `Platform`).
- **Layers are lazy**: `ImageInfo` reads only manifest and config. The first
  filesystem call streams every layer once (3 at a time), indexing the tar
  headers and teeing the compressed blob to disk. Indexes and blobs live under
  `~/.kanivet/images` (blobs LRU-evicted past 2 GiB; a blob over half the cap
  is streamed without caching). A blob is published only after its digest
  verifies.
- **Merge**: OCI whiteouts (`.wh.name`) and opaque dirs (`.wh..wh..opq`) are
  applied bottom-up; each node remembers the layer that last wrote it, so
  previews read only that layer.
- **Scrubbing**: env values whose names look secret, token-shaped values and
  URL passwords are replaced with `••••••`; history lines get the same.
