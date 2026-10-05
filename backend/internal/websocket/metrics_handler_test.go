package websocket

import (
	"context"
	jsonv2 "encoding/json/v2"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kanivet/backend/internal/metrics"
	"github.com/kanivet/backend/internal/websocket/core"
)

// fakeConn records what a stream sends.
type fakeConn struct {
	mu      sync.Mutex
	sent    []MetricsStreamResponse
	sendErr func() error
}

func (c *fakeConn) ID() core.ConnectionID { return "conn-1" }

func (c *fakeConn) Send(b []byte) error {
	if c.sendErr != nil {
		if err := c.sendErr(); err != nil {
			return err
		}
	}
	var msg struct {
		Payload MetricsStreamResponse `json:"payload"`
	}
	if err := jsonv2.Unmarshal(b, &msg); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, msg.Payload)
	return nil
}

func (c *fakeConn) messages() []MetricsStreamResponse {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]MetricsStreamResponse(nil), c.sent...)
}

// fakeQuerier answers with answer, recording each query.
type fakeQuerier struct {
	mu      sync.Mutex
	queries []metrics.MetricQuery
	cached  []bool
	answer  func(ctx context.Context, n int, q metrics.MetricQuery) (*metrics.MetricResponse, error)
}

func (f *fakeQuerier) QueryMetrics(ctx context.Context, _, _ string, q metrics.MetricQuery) (*metrics.MetricResponse, error) {
	f.mu.Lock()
	f.queries = append(f.queries, q)
	f.cached = append(f.cached, metrics.CacheOnly(ctx))
	n := len(f.queries)
	f.mu.Unlock()
	if metrics.CacheOnly(ctx) {
		return nil, metrics.ErrCacheMiss
	}
	return f.answer(ctx, n, q)
}

func (f *fakeQuerier) QueryWorkloadMetrics(context.Context, string, string, metrics.WorkloadMetricQuery) (*metrics.WorkloadMetricResponse, error) {
	return &metrics.WorkloadMetricResponse{}, nil
}

func (f *fakeQuerier) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.queries)
}

func series(v float64) *metrics.MetricResponse {
	return &metrics.MetricResponse{Timestamps: []int64{1}, Values: metrics.Samples{v}}
}

func eventually(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func request(t *testing.T, payload string) MetricsStreamRequest {
	t.Helper()
	var req MetricsStreamRequest
	if err := jsonv2.Unmarshal([]byte(payload), &req); err != nil {
		t.Fatal(err)
	}
	return req
}

// Payloads exactly as frontend/src/services/api/metrics.ts sends them for a
// NodeMetrics card. The stop used to leave nodeName out, so it addressed
// another topic and the node's poller ran until the app closed.
func TestNodeStreamStopsWithTheFrontendsStop(t *testing.T) {
	q := &fakeQuerier{answer: func(context.Context, int, metrics.MetricQuery) (*metrics.MetricResponse, error) {
		return series(1), nil
	}}
	h := &MetricsStreamHandler{metricsService: q}
	conn := &fakeConn{}
	start := request(t, `{"action":"start","cluster":"c","namespace":"","pod":"","nodeName":"node-1","metricType":"cpu","timeRange":"15m","streamingRate":5}`)
	stop := request(t, `{"action":"stop","cluster":"c","namespace":"","pod":"","nodeName":"node-1","metricType":"cpu","timeRange":"15m"}`)
	if a, b := h.buildTopic(start), h.buildTopic(stop); a != b {
		t.Fatalf("start topic %q, stop topic %q", a, b)
	}
	if err := h.handle(context.Background(), conn, start); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return len(conn.messages()) == 1 })
	if err := h.handle(context.Background(), conn, stop); err != nil {
		t.Fatal(err)
	}
	h.streams.Range(func(key, _ any) bool {
		t.Fatalf("stream %v still running", key)
		return true
	})
}

// Two container charts of one pod on one connection used to share a topic,
// so each replaced the other's stream.
func TestStreamTopicsTellTargetsApart(t *testing.T) {
	h := &MetricsStreamHandler{}
	topics := map[string]bool{}
	for _, payload := range []string{
		`{"cluster":"c","namespace":"apps","pod":"web-0","metricType":"cpu","timeRange":"15m"}`,
		`{"cluster":"c","namespace":"apps","pod":"web-0","container":"app","metricType":"cpu","timeRange":"15m"}`,
		`{"cluster":"c","namespace":"apps","pod":"web-0","container":"sidecar","metricType":"cpu","timeRange":"15m"}`,
		`{"cluster":"c","namespace":"apps","podNames":["web-0","web-1"],"metricType":"cpu","timeRange":"15m"}`,
		`{"cluster":"c","namespace":"apps","podNames":["api-0"],"metricType":"cpu","timeRange":"15m"}`,
	} {
		topics[h.buildTopic(request(t, payload))] = true
	}
	if len(topics) != 5 {
		t.Fatalf("topics collide: %v", topics)
	}
	a := h.buildTopic(request(t, `{"cluster":"c","namespace":"apps","podNames":["web-1","web-0"],"metricType":"cpu","timeRange":"15m"}`))
	if !topics[a] || a != "workload_metrics:c:apps:cpu:15m:web-0,web-1" {
		t.Fatalf("workload topic %q depends on pod order", a)
	}
}

// One slow refresh used to stop the stream for good and send "metrics
// provider unavailable: query timed out", which greyed out every chart of the
// cluster for a minute.
func TestSlowRefreshNeitherStopsNorStacks(t *testing.T) {
	release := make(chan struct{})
	q := &fakeQuerier{answer: func(ctx context.Context, n int, _ metrics.MetricQuery) (*metrics.MetricResponse, error) {
		if n == 3 { // the second live refresh hangs past many ticks
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return series(float64(n)), nil
	}}
	conn := &fakeConn{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ended := make(chan struct{})
	go runStream(ctx, conn, 5*time.Millisecond, func() { close(ended) }, "metrics", "t", func(ctx context.Context) (*metrics.MetricResponse, error) {
		return q.QueryMetrics(ctx, "c", "", metrics.MetricQuery{MetricType: "cpu"})
	}, func(d *metrics.MetricResponse) bool { return len(d.Values) > 0 }, nil)

	eventually(t, func() bool { return q.count() == 3 })
	time.Sleep(50 * time.Millisecond) // ten ticks while the refresh hangs
	if n := q.count(); n != 3 {
		t.Fatalf("%d queries: ticks stacked refreshes behind the slow one", n)
	}
	close(release)
	eventually(t, func() bool { return q.count() > 4 })
	for _, m := range conn.messages() {
		if m.Error != "" {
			t.Fatalf("a slow refresh was reported: %q", m.Error)
		}
	}
	select {
	case <-ended:
		t.Fatal("stream ended")
	default:
	}
	cancel()
	<-ended
}

// Transient failures are retried within the refresh, the chart keeps its last
// data through a few failed refreshes, and when they are reported the text
// does not read as "provider unavailable".
func TestTransientFailuresKeepTheChart(t *testing.T) {
	old := metricsRetryPause
	metricsRetryPause = time.Millisecond
	t.Cleanup(func() { metricsRetryPause = old })
	failing := false
	var mu sync.Mutex
	q := &fakeQuerier{answer: func(context.Context, int, metrics.MetricQuery) (*metrics.MetricResponse, error) {
		mu.Lock()
		defer mu.Unlock()
		if failing {
			return nil, &metrics.HTTPStatusError{Store: "prometheus", Code: 503}
		}
		return series(1), nil
	}}
	conn := &fakeConn{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runStream(ctx, conn, time.Millisecond, func() {}, "metrics", "t", func(ctx context.Context) (*metrics.MetricResponse, error) {
		return q.QueryMetrics(ctx, "c", "", metrics.MetricQuery{})
	}, func(d *metrics.MetricResponse) bool { return len(d.Values) > 0 }, nil)
	eventually(t, func() bool { return len(conn.messages()) == 1 })
	mu.Lock()
	failing = true
	mu.Unlock()
	eventually(t, func() bool { return len(conn.messages()) == 2 })
	msgs := conn.messages()
	if msgs[0].Error != "" || !msgs[1].Transient || msgs[1].Error != metricsSlowMessage {
		t.Fatalf("messages %+v", msgs)
	}
	for _, unavailable := range []string{"unavailable", "timed out", "not found in cluster", "connection refused", "eof"} {
		if strings.Contains(strings.ToLower(msgs[1].Error), unavailable) {
			t.Fatalf("%q reads as provider unavailable", msgs[1].Error)
		}
	}
	// 1 good refresh, then metricsReportAfter failed ones of metricsAttempts each.
	if n := q.count() - 1; n < 1+metricsReportAfter*metricsAttempts {
		t.Fatalf("%d queries", n)
	}
}

// A query the store refused is reported at once and not asked again within
// the refresh.
func TestRefusedQueryIsNotRetried(t *testing.T) {
	q := &fakeQuerier{answer: func(context.Context, int, metrics.MetricQuery) (*metrics.MetricResponse, error) {
		return nil, &metrics.QueryError{Type: "bad_data", Message: "parse error"}
	}}
	conn := &fakeConn{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runStream(ctx, conn, time.Hour, func() {}, "metrics", "t", func(ctx context.Context) (*metrics.MetricResponse, error) {
		return q.QueryMetrics(ctx, "c", "", metrics.MetricQuery{})
	}, func(d *metrics.MetricResponse) bool { return len(d.Values) > 0 }, nil)
	eventually(t, func() bool { return len(conn.messages()) == 1 })
	if m := conn.messages()[0]; m.Transient || !strings.Contains(m.Error, "bad_data") {
		t.Fatalf("message %+v", m)
	}
	if n := q.count(); n != 2 { // the cache preview and one live query
		t.Fatalf("%d queries", n)
	}
}

// A chart that was open before paints from the cache straight away, marked
// stale, then the live answer follows; unchanged refreshes are not resent;
// and the other of CPU and memory is warmed for the next click.
func TestStreamPaintsFromCacheThenLive(t *testing.T) {
	var mu sync.Mutex
	var warmed []string
	conn := &fakeConn{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	fetch := func(ctx context.Context) (*metrics.MetricResponse, error) {
		if metrics.CacheOnly(ctx) {
			return series(1), nil
		}
		mu.Lock()
		defer mu.Unlock()
		calls++
		return series(2), nil
	}
	warm := func(ctx context.Context) (*metrics.MetricResponse, error) {
		mu.Lock()
		defer mu.Unlock()
		warmed = append(warmed, "memory")
		return series(3), nil
	}
	go runStream(ctx, conn, time.Millisecond, func() {}, "metrics", "t", fetch,
		func(d *metrics.MetricResponse) bool { return len(d.Values) > 0 }, []func(context.Context) (*metrics.MetricResponse, error){warm})
	eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return calls > 5 && len(warmed) == 1
	})
	msgs := conn.messages()
	if len(msgs) != 2 || !msgs[0].Stale || msgs[0].Data.Values[0] != 1 || msgs[1].Stale || msgs[1].Data.Values[0] != 2 {
		t.Fatalf("messages %+v", msgs)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(warmed) != 1 {
		t.Fatalf("warmed %v, want once", warmed)
	}
}

// A full send queue used to end the stream; only a closed connection does.
func TestStreamOutlivesABusyConnection(t *testing.T) {
	var mu sync.Mutex
	sends := 0
	conn := &fakeConn{sendErr: func() error {
		mu.Lock()
		defer mu.Unlock()
		sends++
		switch sends {
		case 1:
			return core.ErrRateLimitExceeded
		case 3:
			return core.ErrConnectionClosed
		}
		return nil
	}}
	n := 0
	ended := make(chan struct{})
	go runStream(context.Background(), conn, time.Millisecond, func() { close(ended) }, "metrics", "t", func(ctx context.Context) (*metrics.MetricResponse, error) {
		if metrics.CacheOnly(ctx) {
			return nil, metrics.ErrCacheMiss
		}
		n++
		return series(float64(n)), nil
	}, func(d *metrics.MetricResponse) bool { return len(d.Values) > 0 }, nil)
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not end when the connection closed")
	}
	if len(conn.messages()) != 1 {
		t.Fatalf("delivered %d updates, want the one between the busy and the closed send", len(conn.messages()))
	}
}

func TestTransientClassification(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{&metrics.HTTPStatusError{Code: 503}, true},
		{&metrics.HTTPStatusError{Code: 429}, true},
		{&metrics.HTTPStatusError{Code: 404}, false},
		{&metrics.QueryError{Type: "bad_data"}, false},
		{context.DeadlineExceeded, true},
		{errors.Join(errors.New("prometheus not found in cluster"), metrics.ErrNoHistorySource), false},
		{errors.New("failed to query prometheus: read: connection reset by peer"), true},
		{errors.New("no ready pod behind monitoring/prometheus"), false},
	} {
		if got := metrics.IsTransient(tc.err); got != tc.want {
			t.Errorf("IsTransient(%v) = %v", tc.err, got)
		}
	}
}
