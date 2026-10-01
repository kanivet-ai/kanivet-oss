package metrics

import (
	"strings"
	"testing"
	"time"
)

func TestParsePromResultMatrix(t *testing.T) {
	body := []byte(`{"status":"success","data":{"resultType":"matrix","result":[
		{"metric":{"pod":"a"},"values":[[1700000000,"0.5"],[1700000300.5,"NaN"],[1700000600,"1.25e-3"]]},
		{"metric":{"pod":"b"},"values":[]}
	]}}`)
	got, err := parsePromResult(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 series, got %d", len(got))
	}
	a := got[0]
	if a.Labels["pod"] != "a" || len(a.Values) != 2 {
		t.Fatalf("series a = %+v", a)
	}
	if a.Times[1] != 1700000600 || a.Values[1] != 0.00125 {
		t.Fatalf("second sample = %d %v", a.Times[1], a.Values[1])
	}
}

func TestParsePromResultVector(t *testing.T) {
	body := []byte(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1700000000,"42"]}]}}`)
	got, err := parsePromResult(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Values[0] != 42 {
		t.Fatalf("got %+v", got)
	}
}

func TestParsePromResultError(t *testing.T) {
	body := []byte(`{"status":"error","errorType":"bad_data","error":"parse error"}`)
	if _, err := parsePromResult(body); err == nil {
		t.Fatal("want error")
	}
}

func TestQueryErrorTooLarge(t *testing.T) {
	_, err := parsePromResult([]byte(`{"status":"error","errorType":"execution","error":"expanding series: the query exceeded the maximum number of samples (limit: 50000000)"}`))
	qe, ok := err.(*QueryError)
	if !ok || !qe.TooLarge() {
		t.Fatalf("want a too-large QueryError, got %v", err)
	}
}

func TestParseRetryAfter(t *testing.T) {
	if d := parseRetryAfter("7"); d != 7*time.Second {
		t.Fatalf("seconds: %v", d)
	}
	if d := parseRetryAfter(""); d != 0 {
		t.Fatalf("empty: %v", d)
	}
}

func TestParsePromStreamFiltersAndSkipsUnknownFields(t *testing.T) {
	body := `{"status":"success","warnings":["x"],"data":{"resultType":"matrix","stats":{"a":[1,{"b":2}]},"result":[
		{"metric":{"pod":"gone"},"values":[[1,"1"],[2,"2"]]},
		{"values":[[1,"+Inf"],[2,"3"]],"metric":{"pod":"live"},"extra":{"k":[1]}}
	]},"infos":[]}`
	keep := func(l map[string]string) bool { return l["pod"] == "live" }
	got, err := parsePromStream(strings.NewReader(body), keep, 2)
	if err != nil {
		t.Fatal(err)
	}
	// Labels may come after the samples: the series is still filtered.
	if len(got) != 1 || got[0].Labels["pod"] != "live" || len(got[0].Values) != 1 || got[0].Values[0] != 3 {
		t.Fatalf("got %+v", got)
	}
}

func TestParsePromStreamRejectsTruncatedBody(t *testing.T) {
	body := `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":[[1,"1"],[2,`
	if _, err := parsePromStream(strings.NewReader(body), nil, 1); err == nil {
		t.Fatal("a truncated response parsed")
	}
}
