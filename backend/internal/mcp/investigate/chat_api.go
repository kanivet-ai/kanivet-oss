package investigate

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

func chatErr(c *gin.Context, err error, m *ChatManager) {
	code := http.StatusBadRequest
	body := gin.H{"error": err.Error()}
	switch {
	case errors.Is(err, ErrChatNotFound):
		code = http.StatusNotFound
	case errors.Is(err, ErrNoAgent):
		code = http.StatusPreconditionFailed
		body["agents"] = m.detect()
	case errors.Is(err, ErrBusy), errors.Is(err, ErrDirty):
		code = http.StatusConflict
	}
	if errors.Is(err, ErrDirty) {
		body["code"] = "dirty"
	}
	c.JSON(code, body)
}

// RegisterChatRoutes adds /ai/chats/* and /ai/code-locations/*.
func (m *ChatManager) RegisterChatRoutes(v1 *gin.RouterGroup) {
	g := v1.Group("/ai")
	g.GET("/chats", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"chats": m.List()}) })
	g.POST("/chats", func(c *gin.Context) {
		var in NewChatInput
		if err := c.ShouldBindJSON(&in); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if in.Mode == ModeCode && in.Repo != "" && !in.AllowDirty && !in.NewBranch {
			if st, err := GetRepoStatus(c.Request.Context(), in.Repo); err == nil && (len(st.Dirty) > 0 || len(st.Untracked) > 0) {
				c.JSON(http.StatusConflict, gin.H{"error": ErrDirty.Error(), "code": "dirty", "status": st})
				return
			}
		}
		chat, err := m.Create(in)
		if err != nil {
			chatErr(c, err, m)
			return
		}
		c.JSON(http.StatusOK, chatView(chat, 0))
	})
	g.GET("/chats/:id", func(c *gin.Context) {
		since, err := strconv.ParseInt(c.DefaultQuery("since", "-1"), 10, 64)
		if err != nil {
			since = -1
		}
		chat, ver, err := m.Get(c.Param("id"), since)
		if err != nil {
			chatErr(c, err, m)
			return
		}
		if chat == nil {
			c.JSON(http.StatusOK, gin.H{"unchanged": true, "version": ver})
			return
		}
		c.JSON(http.StatusOK, chatView(chat, ver))
	})
	g.PATCH("/chats/:id", func(c *gin.Context) {
		var body struct {
			Title string `json:"title"`
		}
		_ = c.ShouldBindJSON(&body)
		if err := m.Rename(c.Param("id"), body.Title); err != nil {
			chatErr(c, err, m)
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	g.DELETE("/chats/:id", func(c *gin.Context) {
		if err := m.Delete(c.Param("id")); err != nil {
			chatErr(c, err, m)
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	g.POST("/chats/:id/messages", func(c *gin.Context) {
		var body struct {
			Text    string        `json:"text"`
			Context []ContextChip `json:"context"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if err := m.Send(c.Param("id"), body.Text, body.Context); err != nil {
			chatErr(c, err, m)
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	g.POST("/chats/:id/cancel", func(c *gin.Context) {
		if err := m.Cancel(c.Param("id")); err != nil {
			chatErr(c, err, m)
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	// Live-cluster proposals: dry-run, then apply only on an explicit click.
	g.POST("/chats/:id/proposals/:msg/:pid/:action", func(c *gin.Context) {
		id, msg, pid := c.Param("id"), c.Param("msg"), c.Param("pid")
		var p *Proposal
		var err error
		switch c.Param("action") {
		case "dry-run":
			p, err = m.DryRunProposal(id, msg, pid)
		case "apply":
			p, err = m.ApplyProposal(id, msg, pid)
		case "reject":
			p, err = m.RejectProposal(id, msg, pid)
		default:
			c.JSON(http.StatusNotFound, gin.H{"error": "unknown action"})
			return
		}
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "proposal": p})
			return
		}
		c.JSON(http.StatusOK, gin.H{"proposal": p})
	})

	// Code review of a "fix in code" chat.
	g.GET("/chats/:id/diff", func(c *gin.Context) {
		files, w, err := m.Diff(c.Param("id"))
		if err != nil {
			chatErr(c, err, m)
			return
		}
		if files == nil {
			files = []FileDiff{}
		}
		c.JSON(http.StatusOK, gin.H{"files": files, "work": w})
	})
	g.POST("/chats/:id/discard", func(c *gin.Context) {
		var body struct {
			Paths []string `json:"paths"`
		}
		_ = c.ShouldBindJSON(&body)
		done, err := m.Discard(c.Param("id"), body.Paths)
		if err != nil {
			chatErr(c, err, m)
			return
		}
		c.JSON(http.StatusOK, gin.H{"discarded": done})
	})
	g.POST("/chats/:id/commit", func(c *gin.Context) {
		var body struct {
			Branch  string `json:"branch"`
			Message string `json:"message"`
		}
		_ = c.ShouldBindJSON(&body)
		sha, err := m.Commit(c.Param("id"), body.Branch, body.Message)
		if err != nil {
			chatErr(c, err, m)
			return
		}
		c.JSON(http.StatusOK, gin.H{"sha": sha})
	})
	g.POST("/chats/:id/open-editor", func(c *gin.Context) {
		var body struct {
			Editor string `json:"editor"`
			Path   string `json:"path"`
		}
		_ = c.ShouldBindJSON(&body)
		w, err := m.codeWork(c.Param("id"))
		if err != nil {
			chatErr(c, err, m)
			return
		}
		url, err := OpenInEditor(body.Editor, w.Repo, body.Path)
		if err != nil {
			chatErr(c, err, m)
			return
		}
		c.JSON(http.StatusOK, gin.H{"url": url})
	})

	// Code locations.
	g.GET("/code-locations", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"locations": m.locs.List()})
	})
	g.POST("/code-locations", func(c *gin.Context) {
		var body struct {
			Path string `json:"path"`
		}
		_ = c.ShouldBindJSON(&body)
		loc, err := m.locs.Add(body.Path)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"location": loc, "locations": m.locs.List()})
	})
	g.DELETE("/code-locations", func(c *gin.Context) {
		if err := m.locs.Remove(c.Query("path")); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"locations": m.locs.List()})
	})
	g.GET("/code-locations/suggest", func(c *gin.Context) {
		h, s := m.SuggestFor(c.Query("cluster"), c.Query("kind"), c.Query("namespace"), c.Query("name"))
		if s == nil {
			s = []Suggestion{}
		}
		c.JSON(http.StatusOK, gin.H{"hints": h, "suggestions": s})
	})
	g.GET("/repo-status", func(c *gin.Context) {
		st, err := GetRepoStatus(c.Request.Context(), c.Query("path"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, st)
	})
}

type chatResponse struct {
	*Chat
	Version     int64    `json:"version"`
	Permissions []string `json:"permissions"`
}

func chatView(c *Chat, ver int64) chatResponse {
	return chatResponse{Chat: c, Version: ver, Permissions: c.Permissions()}
}
