package images

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	infoTimeout  = 45 * time.Second
	filesTimeout = 5 * time.Minute
)

// Handler exposes the Service over HTTP.
type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register mounts the routes under g (e.g. /api/v1).
func (h *Handler) Register(g *gin.RouterGroup) {
	g.GET("/images/info", h.info)
	g.GET("/images/status", h.status)
	g.GET("/images/files", h.files)
	g.GET("/images/file", h.file)
	g.GET("/images/search", h.search)
	g.GET("/images/layer-changes", h.layerChanges)
}

func pullContext(c *gin.Context) PullContext {
	pc := PullContext{
		Cluster:        c.Query("cluster"),
		Namespace:      c.Query("namespace"),
		Pod:            c.Query("pod"),
		Container:      c.Query("container"),
		ServiceAccount: c.Query("serviceAccount"),
		ImageID:        c.Query("imageID"),
		Platform:       c.Query("platform"),
	}
	if s := c.Query("pullSecrets"); s != "" {
		for _, n := range strings.Split(s, ",") {
			if n = strings.TrimSpace(n); n != "" {
				pc.PullSecrets = append(pc.PullSecrets, n)
			}
		}
	}
	return pc
}

func fail(c *gin.Context, err error) {
	kind := KindOf(err)
	status := http.StatusInternalServerError
	switch kind {
	case ErrInvalid:
		status = http.StatusBadRequest
	case ErrNotFound:
		status = http.StatusNotFound
	case ErrTooLarge:
		status = http.StatusRequestEntityTooLarge
	case ErrAuth, ErrUnreachable:
		// Not 401: the UI reserves 401 for its own session.
		status = http.StatusBadGateway
	}
	msg := err.Error()
	if e, ok := err.(*Error); ok {
		msg = e.Message
		if e.Err != nil && kind != ErrAuth {
			msg = e.Message + ": " + e.Err.Error()
		}
	}
	c.JSON(status, gin.H{"error": msg, "kind": kind})
}

func (h *Handler) run(c *gin.Context, timeout time.Duration, fn func(ctx context.Context, ref string, pc PullContext) (any, error)) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
	defer cancel()
	out, err := fn(ctx, c.Query("ref"), pullContext(c))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func (h *Handler) info(c *gin.Context) {
	h.run(c, infoTimeout, func(ctx context.Context, ref string, pc PullContext) (any, error) {
		return h.svc.ImageInfo(ctx, ref, pc)
	})
}

func (h *Handler) status(c *gin.Context) {
	h.run(c, infoTimeout, func(ctx context.Context, ref string, pc PullContext) (any, error) {
		return h.svc.Status(ctx, ref, pc)
	})
}

func (h *Handler) files(c *gin.Context) {
	h.run(c, filesTimeout, func(ctx context.Context, ref string, pc PullContext) (any, error) {
		return h.svc.ListDir(ctx, ref, pc, c.DefaultQuery("path", "/"))
	})
}

func (h *Handler) file(c *gin.Context) {
	limit, _ := strconv.ParseInt(c.Query("limit"), 10, 64)
	h.run(c, filesTimeout, func(ctx context.Context, ref string, pc PullContext) (any, error) {
		return h.svc.ReadFile(ctx, ref, pc, c.Query("path"), limit)
	})
}

func (h *Handler) search(c *gin.Context) {
	h.run(c, filesTimeout, func(ctx context.Context, ref string, pc PullContext) (any, error) {
		return h.svc.Search(ctx, ref, pc, c.Query("q"))
	})
}

func (h *Handler) layerChanges(c *gin.Context) {
	layer, err := strconv.Atoi(c.Query("layer"))
	if err != nil {
		fail(c, newErr(ErrInvalid, "layer must be a number", nil))
		return
	}
	h.run(c, filesTimeout, func(ctx context.Context, ref string, pc PullContext) (any, error) {
		return h.svc.LayerChanges(ctx, ref, pc, layer)
	})
}
