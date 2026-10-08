// Tier-2 NATS HTTP endpoints: everything that needs a real nats.go client
// connection (backend/internal/nats.LivePool), as opposed to nats_handlers.go
// which stays pure HTTP-monitor-proxy, no NATS credentials, no port-forward.
package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nats-io/nats.go/jetstream"
)

// natsRawMessage mirrors jetstream.RawStreamMsg with JSON field names this
// app's frontend expects. Data stays a []byte field: encoding/json already
// renders a []byte as a base64 string, which is the one wire format this
// package uses for every raw message body, in HTTP responses and websocket
// broadcasts alike.
type natsRawMessage struct {
	Subject  string              `json:"subject"`
	Sequence uint64              `json:"sequence"`
	Headers  map[string][]string `json:"headers,omitempty"`
	Data     []byte              `json:"data"`
	Time     time.Time           `json:"time"`
}

func rawMessageFrom(msg *jetstream.RawStreamMsg) natsRawMessage {
	return natsRawMessage{
		Subject:  msg.Subject,
		Sequence: msg.Sequence,
		Headers:  map[string][]string(msg.Header),
		Data:     msg.Data,
		Time:     msg.Time,
	}
}

func (h *Handler) GetNatsStreamMessage(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}
	stream := c.Param("stream")
	seq, err := strconv.ParseUint(c.Query("seq"), 10, 64)
	if err != nil {
		h.respond(c, http.StatusBadRequest, nil, errors.New("seq must be a positive integer"))
		return
	}
	ctx, cancel := requestContext(c)
	defer cancel()
	msg, err := h.natsLive.GetStreamMsg(ctx, cluster, stream, seq)
	if err != nil {
		h.respond(c, statusForNatsLiveError(err), nil, err)
		return
	}
	h.respond(c, http.StatusOK, gin.H{"message": rawMessageFrom(msg)}, nil)
}

func (h *Handler) GetNatsStreamLastMessage(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}
	stream := c.Param("stream")
	subject := c.Query("subject")
	if subject == "" {
		h.respond(c, http.StatusBadRequest, nil, errors.New("subject query parameter is required"))
		return
	}
	ctx, cancel := requestContext(c)
	defer cancel()
	msg, err := h.natsLive.GetLastStreamMsg(ctx, cluster, stream, subject)
	if err != nil {
		h.respond(c, statusForNatsLiveError(err), nil, err)
		return
	}
	h.respond(c, http.StatusOK, gin.H{"message": rawMessageFrom(msg)}, nil)
}

func (h *Handler) GetNatsConsumerInfo(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}
	stream := c.Param("stream")
	consumer := c.Param("consumer")
	ctx, cancel := requestContext(c)
	defer cancel()
	info, err := h.natsLive.GetConsumerInfo(ctx, cluster, stream, consumer)
	if err != nil {
		h.respond(c, statusForNatsLiveError(err), nil, err)
		return
	}
	h.respond(c, http.StatusOK, gin.H{"consumer": info}, nil)
}

// natsKVEntry mirrors jetstream.KeyValueEntry (an interface, so it has no
// JSON tags of its own) with the field names this app's frontend expects.
type natsKVEntry struct {
	Bucket    string    `json:"bucket"`
	Key       string    `json:"key"`
	Value     []byte    `json:"value"`
	Revision  uint64    `json:"revision"`
	Created   time.Time `json:"created"`
	Operation string    `json:"operation"`
}

func kvEntryFrom(entry jetstream.KeyValueEntry) natsKVEntry {
	return natsKVEntry{
		Bucket:    entry.Bucket(),
		Key:       entry.Key(),
		Value:     entry.Value(),
		Revision:  entry.Revision(),
		Created:   entry.Created(),
		Operation: entry.Operation().String(),
	}
}

func (h *Handler) ListNatsKVBuckets(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}
	ctx, cancel := requestContext(c)
	defer cancel()
	names, err := h.natsLive.ListKVBuckets(ctx, cluster)
	if err != nil {
		h.respond(c, statusForNatsLiveError(err), nil, err)
		return
	}
	h.respond(c, http.StatusOK, gin.H{"buckets": names}, nil)
}

func (h *Handler) ListNatsKVKeys(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}
	bucket := c.Param("bucket")
	ctx, cancel := requestContext(c)
	defer cancel()
	keys, err := h.natsLive.ListKVKeys(ctx, cluster, bucket)
	if err != nil {
		h.respond(c, statusForNatsLiveError(err), nil, err)
		return
	}
	h.respond(c, http.StatusOK, gin.H{"keys": keys}, nil)
}

func (h *Handler) GetNatsKVEntry(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}
	bucket := c.Param("bucket")
	key := c.Param("key")
	ctx, cancel := requestContext(c)
	defer cancel()
	entry, err := h.natsLive.GetKVEntry(ctx, cluster, bucket, key)
	if err != nil {
		h.respond(c, statusForNatsLiveError(err), nil, err)
		return
	}
	h.respond(c, http.StatusOK, gin.H{"entry": kvEntryFrom(entry)}, nil)
}

func (h *Handler) GetNatsKVHistory(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}
	bucket := c.Param("bucket")
	key := c.Param("key")
	ctx, cancel := requestContext(c)
	defer cancel()
	history, err := h.natsLive.GetKVHistory(ctx, cluster, bucket, key)
	if err != nil {
		h.respond(c, statusForNatsLiveError(err), nil, err)
		return
	}
	entries := make([]natsKVEntry, len(history))
	for i, entry := range history {
		entries[i] = kvEntryFrom(entry)
	}
	h.respond(c, http.StatusOK, gin.H{"history": entries}, nil)
}

func (h *Handler) ListNatsObjectStores(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}
	ctx, cancel := requestContext(c)
	defer cancel()
	names, err := h.natsLive.ListObjectStores(ctx, cluster)
	if err != nil {
		h.respond(c, statusForNatsLiveError(err), nil, err)
		return
	}
	h.respond(c, http.StatusOK, gin.H{"buckets": names}, nil)
}

func (h *Handler) ListNatsObjects(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}
	bucket := c.Param("bucket")
	ctx, cancel := requestContext(c)
	defer cancel()
	infos, err := h.natsLive.ListObjects(ctx, cluster, bucket)
	if err != nil {
		h.respond(c, statusForNatsLiveError(err), nil, err)
		return
	}
	h.respond(c, http.StatusOK, gin.H{"objects": infos}, nil)
}

// DownloadNatsObject streams an object's raw bytes directly - this is a read
// against the user's own cluster, so it needs no size cap beyond what any
// other file download in this app would need.
func (h *Handler) DownloadNatsObject(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}
	bucket := c.Param("bucket")
	name := c.Param("name")
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Minute)
	defer cancel()

	result, err := h.natsLive.GetObject(ctx, cluster, bucket, name)
	if err != nil {
		h.respond(c, statusForNatsLiveError(err), nil, err)
		return
	}
	defer func() { _ = result.Close() }()

	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	c.Header("Content-Type", "application/octet-stream")
	if _, err := io.Copy(c.Writer, result); err != nil {
		log.Printf("[Nats] download object %s/%s failed mid-stream: %v", bucket, name, err)
	}
}

// A cold call pays for the pool's one-time connect: a port-forward plus
// credential discovery that may walk several namespaces and Secrets,
// potentially through extra network hops (e.g. a vcluster proxy sitting in
// front of the real API server). 45s gives that room; a cached pool
// connection on every later call is instant regardless.
func requestContext(c *gin.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(c.Request.Context(), 45*time.Second)
}

func statusForNatsLiveError(err error) int {
	switch {
	case errors.Is(err, jetstream.ErrStreamNotFound),
		errors.Is(err, jetstream.ErrMsgNotFound),
		errors.Is(err, jetstream.ErrConsumerNotFound),
		errors.Is(err, jetstream.ErrConsumerDoesNotExist),
		errors.Is(err, jetstream.ErrBucketNotFound),
		errors.Is(err, jetstream.ErrKeyNotFound),
		errors.Is(err, jetstream.ErrObjectNotFound):
		return http.StatusNotFound
	default:
		return http.StatusBadGateway
	}
}
