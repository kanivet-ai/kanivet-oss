package metrics

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestParseServerTiming(t *testing.T) {
	cases := []struct {
		header string
		time   time.Duration
		bytes  int64
	}{
		{"querier_wall_time;dur=10.5, response_time;dur=24.5, bytes_processed;val=42177", 24500 * time.Microsecond, 42177},
		{"", 0, 0},
		{`cache;desc="hit"`, 0, 0},
		{"response_time;dur=abc, bytes_processed;val=-4", 0, 0},
		{"response_time;dur=3", 3 * time.Millisecond, 0},
		{`bytes_processed;val="900"`, 0, 900},
	}
	for _, c := range cases {
		gotTime, gotBytes := parseServerTiming(c.header)
		if gotTime != c.time || gotBytes != c.bytes {
			t.Errorf("%q: got %v, %d; want %v, %d", c.header, gotTime, gotBytes, c.time, c.bytes)
		}
	}
}

func TestRecordResponse(t *testing.T) {
	info := &ResponseInfo{}
	ctx := WithResponseInfo(context.Background(), info)
	resp := &http.Response{Header: http.Header{
		"Server-Timing": {"response_time;dur=250, bytes_processed;val=1000"},
		"Date":          {"Mon, 05 Oct 2026 17:39:22 GMT"},
	}}
	RecordResponse(ctx, resp)
	st, b, date, rcv, single := info.Snapshot()
	if st != 250*time.Millisecond || b != 1000 || !single || rcv.IsZero() {
		t.Fatalf("got %v %d %v %v single=%v", st, b, date, rcv, single)
	}
	if date.Unix() != time.Date(2026, 10, 5, 17, 39, 22, 0, time.UTC).Unix() {
		t.Fatalf("date %v", date)
	}
	// A second response makes it no longer describe one query.
	RecordResponse(ctx, resp)
	if _, _, _, _, single := info.Snapshot(); single {
		t.Fatal("two responses must not read as one")
	}
	// Without a ResponseInfo in the context nothing happens.
	RecordResponse(context.Background(), resp)
}
