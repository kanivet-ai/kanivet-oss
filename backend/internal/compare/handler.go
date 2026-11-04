package compare

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

// Handler serves the compare API.
type Handler struct {
	service *Service
}

func NewHandler(s *Service) *Handler { return &Handler{service: s} }

type compareRequest struct {
	A       Ref     `json:"a"`
	B       Ref     `json:"b"`
	Options Options `json:"options"`
}

// Compare handles POST /compare {a, b, options}.
func (h *Handler) Compare(c *gin.Context) {
	var req compareRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	res, err := h.service.Compare(c.Request.Context(), req.A, req.B, req.Options)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, res)
}

// Baselines handles GET /compare/baselines?cluster&group&version&kind&namespace&name.
func (h *Handler) Baselines(c *gin.Context) {
	ref := Ref{
		Cluster:   c.Query("cluster"),
		Group:     c.Query("group"),
		Version:   c.Query("version"),
		Kind:      c.Query("kind"),
		Namespace: c.Query("namespace"),
		Name:      c.Query("name"),
	}
	b, err := h.service.Baselines(c.Request.Context(), ref)
	if err != nil {
		status := http.StatusInternalServerError
		var se *SideError
		if errors.As(err, &se) {
			status = se.Status
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, b)
}
