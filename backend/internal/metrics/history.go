package metrics

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unsafe"
)

// historyTimeout bounds one raw history query. Long windows over a busy
// namespace move tens of megabytes through the port-forward, far beyond what
// the 5s chart client allows.
const historyTimeout = 90 * time.Second

// HistorySeries is one series returned by a raw PromQL query. Values holds
// one sample per entry of Times (unix seconds); NaN and Inf are dropped.
// float32 holds 7 significant digits, ample for cores and bytes, at half the
// memory of a window's worth of samples.
type HistorySeries struct {
	Labels map[string]string
	Times  []int64
	Values []float32
}

type seriesFilterKey struct{}

// WithSeriesFilter makes history queries made with ctx skip series keep
// rejects while the response is parsed, before any of their samples are
// stored.
func WithSeriesFilter(ctx context.Context, keep func(labels map[string]string) bool) context.Context {
	return context.WithValue(ctx, seriesFilterKey{}, keep)
}

func seriesFilter(ctx context.Context) func(map[string]string) bool {
	keep, _ := ctx.Value(seriesFilterKey{}).(func(map[string]string) bool)
	return keep
}

type partialKey struct{}

// WithPartialFlag makes history queries made with ctx set partial when the
// store answers with only part of the data. Thanos (by default) and a
// VictoriaMetrics cluster answer that way, as a success, when one of the
// stores behind them is down: fine to show, but not to keep as complete.
func WithPartialFlag(ctx context.Context, partial *atomic.Bool) context.Context {
	return context.WithValue(ctx, partialKey{}, partial)
}

// MarkPartial sets the flag WithPartialFlag put in ctx, if any: the answer to
// the query made with ctx was partial. QueryRange and QueryInstant call it.
func MarkPartial(ctx context.Context) {
	if p, _ := ctx.Value(partialKey{}).(*atomic.Bool); p != nil {
		p.Store(true)
	}
}

// ErrNoHistorySource means the cluster has no Prometheus-compatible store, so
// only point-in-time metrics (metrics-server) are available.
var ErrNoHistorySource = errors.New("no Prometheus-compatible metrics store in this cluster")

type historyProviderKey struct{}

// WithHistoryProvider pins history queries to the cluster's selected provider.
// Auto (or an omitted preference) keeps normal discovery order.
func WithHistoryProvider(ctx context.Context, provider string) context.Context {
	if provider == "auto" {
		provider = ""
	}
	return context.WithValue(ctx, historyProviderKey{}, provider)
}

func HistoryProvider(ctx context.Context) string {
	provider, _ := ctx.Value(historyProviderKey{}).(string)
	return provider
}

// HistorySource honors the provider selected in ctx. Auto tries
// Prometheus (or Thanos/VictoriaMetrics) first, then Mimir/Cortex, the same
// order chart queries use. When neither is usable it returns the best
// not-found info, whose Reason says why, and metricsServer reports whether a
// point-in-time source exists.
func (s *Service) HistorySource(ctx context.Context, cluster string) (info *ProviderInfo, metricsServer bool) {
	var fallback *ProviderInfo
	order := []string{"prometheus", "mimir"}
	if selected := HistoryProvider(ctx); selected != "" {
		switch selected {
		case "prometheus", "mimir":
			order = []string{selected}
		case "metrics-server":
			return &ProviderInfo{Type: selected, Reason: "metrics-server provides current values only, not usage history"}, true
		case "disabled":
			return &ProviderInfo{Type: selected, Reason: "metrics are disabled for this cluster"}, false
		case "custom":
			return &ProviderInfo{Type: selected, Reason: "custom metrics URLs are not supported by rightsizing"}, false
		default:
			return &ProviderInfo{Type: selected, Reason: "unknown metrics provider: " + selected}, false
		}
	}
	for _, name := range order {
		p, ok := s.providers[name]
		if !ok {
			continue
		}
		pi, err := p.Detect(cluster)
		if err != nil {
			if fallback == nil {
				fallback = &ProviderInfo{Type: name, Reason: "detection failed: " + trimErr(err)}
			}
			continue
		}
		if pi.Found {
			return pi, false
		}
		if fallback == nil || (fallback.Reason == "" && pi.Reason != "") {
			fallback = pi
		}
	}
	if ms, ok := s.providers["metrics-server"]; ok && HistoryProvider(ctx) == "" {
		if pi, err := ms.Detect(cluster); err == nil && pi.Found {
			metricsServer = true
		}
	}
	if fallback == nil {
		fallback = &ProviderInfo{Reason: ErrNoHistorySource.Error()}
	}
	return fallback, metricsServer
}

// historyAPI is implemented by providers that speak the Prometheus HTTP API.
type historyAPI interface {
	promGet(ctx context.Context, cluster, path string, params url.Values) (io.ReadCloser, error)
}

func (s *Service) historyProvider(ctx context.Context, cluster string) (historyAPI, error) {
	info, _ := s.HistorySource(ctx, cluster)
	if info == nil || !info.Found {
		return nil, ErrNoHistorySource
	}
	if info.NeedsTenant {
		return nil, fmt.Errorf("%s needs a tenant (X-Scope-OrgID) before it returns data", info.Type)
	}
	api, ok := s.providers[info.Type].(historyAPI)
	if !ok {
		return nil, ErrNoHistorySource
	}
	return api, nil
}

// QueryRange runs a raw PromQL range query against the cluster's history
// store.
func (s *Service) QueryRange(ctx context.Context, cluster, query string, start, end time.Time, step time.Duration) ([]HistorySeries, error) {
	api, err := s.historyProvider(ctx, cluster)
	if err != nil {
		return nil, err
	}
	params := url.Values{}
	params.Set("query", query)
	params.Set("start", strconv.FormatInt(start.Unix(), 10))
	params.Set("end", strconv.FormatInt(end.Unix(), 10))
	params.Set("step", strconv.FormatInt(int64(step/time.Second), 10))
	body, err := api.promGet(ctx, cluster, "/api/v1/query_range", params)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	points := 1
	if step > 0 {
		points = int(end.Sub(start)/step) + 1
	}
	res, partial, err := parsePromStream(body, seriesFilter(ctx), points)
	if partial {
		MarkPartial(ctx)
	}
	return res, err
}

// QueryInstant runs a raw PromQL instant query at the given time.
func (s *Service) QueryInstant(ctx context.Context, cluster, query string, at time.Time) ([]HistorySeries, error) {
	api, err := s.historyProvider(ctx, cluster)
	if err != nil {
		return nil, err
	}
	params := url.Values{}
	params.Set("query", query)
	params.Set("time", strconv.FormatInt(at.Unix(), 10))
	body, err := api.promGet(ctx, cluster, "/api/v1/query", params)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	res, partial, err := parsePromStream(body, seriesFilter(ctx), 1)
	if partial {
		MarkPartial(ctx)
	}
	return res, err
}

// parsePromStream decodes a Prometheus API response as it arrives. Busy
// namespaces answer with tens of megabytes; reading it whole and decoding
// through reflection used to cost several bytes of garbage per byte read.
// Here each sample's two values are parsed straight from the decoder's
// buffer, series keep rejects are skipped without storing anything, and the
// sample arrays start at the expected point count.
//
// partial reports a successful answer the store says is incomplete: Thanos
// adds warnings, a VictoriaMetrics cluster sets isPartial. Prometheus' infos
// are notes about the query, not missing data, and don't count.
func parsePromStream(r io.Reader, keep func(map[string]string) bool, points int) (out []HistorySeries, partial bool, err error) {
	dec := jsontext.NewDecoder(r)
	sc := &sampleScratch{times: make([]int64, 0, points), values: make([]float32, 0, points)}
	var status, errType, em string
	bad := func(err error) error { return fmt.Errorf("unexpected response from metrics store: %w", err) }
	if err := expect(dec, '{'); err != nil {
		return nil, false, bad(err)
	}
	for dec.PeekKind() != '}' {
		key, err := readString(dec)
		if err != nil {
			return nil, false, bad(err)
		}
		switch key {
		case "status":
			status, err = readString(dec)
		case "errorType":
			errType, err = readString(dec)
		case "error":
			em, err = readString(dec)
		case "data":
			out, err = parseData(dec, keep, sc)
		case "warnings":
			var n int
			n, err = countArray(dec)
			partial = partial || n > 0
		case "isPartial":
			var v jsontext.Value
			v, err = dec.ReadValue()
			partial = partial || v.Kind() == 't'
		default:
			err = dec.SkipValue()
		}
		if err != nil {
			return nil, false, bad(err)
		}
	}
	if status != "success" {
		return nil, false, &QueryError{Type: errType, Message: em}
	}
	return out, partial, nil
}

// countArray skips a JSON array (or null) and returns how many elements it
// had.
func countArray(dec *jsontext.Decoder) (int, error) {
	if dec.PeekKind() != '[' {
		return 0, dec.SkipValue()
	}
	if err := expect(dec, '['); err != nil {
		return 0, err
	}
	n := 0
	for dec.PeekKind() != ']' {
		if err := dec.SkipValue(); err != nil {
			return 0, err
		}
		n++
	}
	return n, expect(dec, ']')
}

// sampleScratch collects one series' samples before they're copied out at
// their exact size. Series are often far shorter than the range (pods that
// came and went, sparse event counts), so sizing each to the range would
// hold mostly empty arrays.
type sampleScratch struct {
	times  []int64
	values []float32
}

func parseData(dec *jsontext.Decoder, keep func(map[string]string) bool, sc *sampleScratch) ([]HistorySeries, error) {
	if err := expect(dec, '{'); err != nil {
		return nil, err
	}
	var out []HistorySeries
	for dec.PeekKind() != '}' {
		key, err := readString(dec)
		if err != nil {
			return nil, err
		}
		if key != "result" {
			if err := dec.SkipValue(); err != nil {
				return nil, err
			}
			continue
		}
		if dec.PeekKind() != '[' { // a scalar or string result: nothing to keep
			if err := dec.SkipValue(); err != nil {
				return nil, err
			}
			continue
		}
		if err := expect(dec, '['); err != nil {
			return nil, err
		}
		for dec.PeekKind() != ']' {
			s, ok, err := parseSeries(dec, keep, sc)
			if err != nil {
				return nil, err
			}
			if ok {
				out = append(out, s)
			}
		}
		if err := expect(dec, ']'); err != nil {
			return nil, err
		}
	}
	return out, expect(dec, '}')
}

func parseSeries(dec *jsontext.Decoder, keep func(map[string]string) bool, sc *sampleScratch) (HistorySeries, bool, error) {
	var s HistorySeries
	wanted := true
	sc.times, sc.values = sc.times[:0], sc.values[:0]
	if err := expect(dec, '{'); err != nil {
		return s, false, err
	}
	for dec.PeekKind() != '}' {
		key, err := readString(dec)
		if err != nil {
			return s, false, err
		}
		switch key {
		case "metric":
			if s.Labels, err = readLabels(dec); err != nil {
				return s, false, err
			}
			wanted = keep == nil || keep(s.Labels)
		case "values":
			if !wanted {
				err = dec.SkipValue()
				break
			}
			if err = expect(dec, '['); err != nil {
				return s, false, err
			}
			for dec.PeekKind() != ']' {
				if err = readSample(dec, sc); err != nil {
					return s, false, err
				}
			}
			err = expect(dec, ']')
		case "value":
			if !wanted {
				err = dec.SkipValue()
				break
			}
			err = readSample(dec, sc)
		default:
			err = dec.SkipValue()
		}
		if err != nil {
			return s, false, err
		}
	}
	if wanted {
		s.Times = slices.Clone(sc.times)
		s.Values = slices.Clone(sc.values)
	}
	return s, wanted, expect(dec, '}')
}

// readSample reads one [timestamp, "value"] pair, dropping NaN and Inf.
func readSample(dec *jsontext.Decoder, s *sampleScratch) error {
	if err := expect(dec, '['); err != nil {
		return err
	}
	tv, err := dec.ReadValue()
	if err != nil {
		return err
	}
	t, err := strconv.ParseFloat(unsafe.String(unsafe.SliceData(tv), len(tv)), 64)
	if err != nil {
		return fmt.Errorf("bad sample time %q", tv)
	}
	vv, err := dec.ReadValue()
	if err != nil {
		return err
	}
	raw := []byte(vv)
	if len(raw) >= 2 && raw[0] == '"' {
		raw = raw[1 : len(raw)-1]
	}
	v, perr := strconv.ParseFloat(unsafe.String(unsafe.SliceData(raw), len(raw)), 64)
	if err := expect(dec, ']'); err != nil {
		return err
	}
	if perr != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return nil
	}
	s.times = append(s.times, int64(t))
	s.values = append(s.values, float32(v))
	return nil
}

func readLabels(dec *jsontext.Decoder) (map[string]string, error) {
	if err := expect(dec, '{'); err != nil {
		return nil, err
	}
	labels := map[string]string{}
	for dec.PeekKind() != '}' {
		k, err := readString(dec)
		if err != nil {
			return nil, err
		}
		v, err := readString(dec)
		if err != nil {
			return nil, err
		}
		labels[k] = v
	}
	return labels, expect(dec, '}')
}

func readString(dec *jsontext.Decoder) (string, error) {
	tok, err := dec.ReadToken()
	if err != nil {
		return "", err
	}
	if tok.Kind() != '"' {
		return "", fmt.Errorf("expected a string, got %v", tok.Kind())
	}
	return tok.String(), nil
}

func expect(dec *jsontext.Decoder, kind jsontext.Kind) error {
	tok, err := dec.ReadToken()
	if err != nil {
		return err
	}
	if tok.Kind() != kind {
		return fmt.Errorf("expected %v, got %v", kind, tok.Kind())
	}
	return nil
}

// parsePromResult decodes a whole response held in memory.
func parsePromResult(body []byte) ([]HistorySeries, error) {
	res, _, err := parsePromStream(bytes.NewReader(body), nil, 1)
	return res, err
}

// HTTPStatusError is a non-2xx answer from the metrics store.
type HTTPStatusError struct {
	Store string
	Code  int
	// RetryAfter is the store's Retry-After hint, zero when it gave none.
	RetryAfter time.Duration
}

func (e *HTTPStatusError) Error() string { return fmt.Sprintf("%s returned http %d", e.Store, e.Code) }

// Retryable reports whether the store may answer if asked again: overload
// (429) and server-side failures (5xx).
func (e *HTTPStatusError) Retryable() bool {
	return e.Code == http.StatusTooManyRequests || e.Code >= 500
}

// parseRetryAfter reads a Retry-After header in seconds or as an HTTP date.
func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		return max(0, time.Until(t))
	}
	return 0
}

// QueryError is the store refusing or failing a query it understood, such as
// one that would read more samples than it allows.
type QueryError struct {
	Type    string
	Message string
}

func (e *QueryError) Error() string { return fmt.Sprintf("query failed: %s %s", e.Type, e.Message) }

// TooLarge reports whether the store refused the query for its size, which a
// smaller time range fixes.
func (e *QueryError) TooLarge() bool {
	m := strings.ToLower(e.Message)
	for _, s := range []string{"max samples", "maximum number of samples", "too many samples", "exceeded the limit", "query too large", "max-fetched", "fetched chunks", "fetched series", "max_fetched"} {
		if strings.Contains(m, s) {
			return true
		}
	}
	return false
}

// historyClient shares the port-forward's transport but allows long queries.
func historyClient(base *http.Client) *http.Client {
	client := *base
	client.Timeout = historyTimeout
	return &client
}

// promBody hands back the response body to stream, or an error for a status
// that has no API answer in it. The Prometheus API answers 400/422 with a
// JSON error worth parsing.
func promBody(resp *http.Response, what string) (io.ReadCloser, error) {
	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusUnprocessableEntity {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10)) // let the connection be reused
		resp.Body.Close()
		return nil, &HTTPStatusError{Store: what, Code: resp.StatusCode, RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"))}
	}
	return resp.Body, nil
}

// chartQueryParams aligns the range to the step, so successive refreshes
// land on the same sample grid and Mimir's results cache can reuse them.
func chartQueryParams(query, window, stepText string) (url.Values, error) {
	var p PrometheusProvider
	rangeDuration := p.parseTimeRange(window)
	if stepText == "" {
		stepText = p.calculateStep(rangeDuration)
	}
	step, err := time.ParseDuration(stepText)
	if err != nil {
		secs, e := strconv.ParseInt(stepText, 10, 64)
		if e != nil {
			return nil, fmt.Errorf("invalid metrics step %q", stepText)
		}
		step = time.Duration(secs) * time.Second
	}
	if step < time.Second || step%time.Second != 0 {
		return nil, fmt.Errorf("metrics step must be a positive whole number of seconds")
	}
	end := time.Now().Unix() / int64(step/time.Second) * int64(step/time.Second)
	start := end - int64(rangeDuration/step)*int64(step/time.Second)
	return url.Values{"query": {query}, "start": {strconv.FormatInt(start, 10)}, "end": {strconv.FormatInt(end, 10)}, "step": {strconv.FormatInt(int64(step/time.Second), 10)}}, nil
}

// querySource keys chart answers by the store they came from.
func querySource(cluster string, info *ProviderInfo, tenant string) string {
	return queryHash(cluster, info.Type, info.URL, info.Namespace, info.Service, strconv.Itoa(int(info.Port)), info.Path, tenant)
}

// errPortForwardDropped marks a query that failed because its port-forward
// died. The dead tunnel has already been stopped and the next request opens a
// new one; retrying is left to the caller, so rightsizing's retry budget
// counts it like any other retry.
var errPortForwardDropped = errors.New("port-forward dropped")

// HistorySourceKey identifies the store that answers history queries for a
// cluster: its provider type, the Service and port it reaches and, for Mimir,
// the tenant. History cached from one store must never be served for another,
// even when the provider type is the same. Empty when no store is usable.
func (s *Service) HistorySourceKey(ctx context.Context, cluster string) string {
	info, _ := s.HistorySource(ctx, cluster)
	if info == nil || !info.Found {
		return ""
	}
	tenant := ""
	if mp, ok := s.providers[info.Type].(*MimirProvider); ok && mp.tenantLookup != nil {
		tenant = mp.tenantLookup(cluster)
	}
	return strings.Join([]string{info.Type, info.URL, info.Namespace, info.Service, strconv.Itoa(int(info.Port)), info.Path, tenant}, "|")
}

func (p *PrometheusProvider) promGet(ctx context.Context, cluster, path string, params url.Values) (io.ReadCloser, error) {
	info, err := p.Detect(cluster)
	if err != nil || !info.Found {
		return nil, fmt.Errorf("prometheus not found in cluster: %w", ErrNoHistorySource)
	}
	pfInfo, err := p.getOrCreatePortForward(cluster, info)
	if err != nil {
		return nil, err
	}
	u := fmt.Sprintf("http://localhost:%d%s%s?%s", pfInfo.PortForward.LocalPort, pfInfo.BasePath, path, params.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := historyClient(pfInfo.HTTPClient).Do(req)
	if err != nil {
		// The recreate is throttled per cluster so a misconfigured target
		// can't make us churn port-forwards on every failed query.
		if isPortForwardLikelyDead(err) && p.shouldRecreate(cluster) {
			if p.portForwardPool.CompareAndDelete(prometheusPoolKey(cluster, info), pfInfo) {
				_ = p.k8s.StopPortForward(pfInfo.PortForward.ID)
				p.maybeInvalidateProvider(cluster)
			}
			return nil, fmt.Errorf("failed to query prometheus: %w: %w", errPortForwardDropped, err)
		}
		return nil, fmt.Errorf("failed to query prometheus: %w", err)
	}
	p.recordQuerySuccess(cluster)
	return promBody(resp, "prometheus")
}

func (p *MimirProvider) promGet(ctx context.Context, cluster, path string, params url.Values) (io.ReadCloser, error) {
	info, err := p.Detect(cluster)
	if err != nil || !info.Found {
		return nil, fmt.Errorf("mimir not detected in cluster: %w", ErrNoHistorySource)
	}
	pfInfo, err := p.getOrCreatePortForward(cluster, info)
	if err != nil {
		return nil, err
	}
	u := fmt.Sprintf("http://localhost:%d/prometheus%s?%s", pfInfo.PortForward.LocalPort, path, params.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	if p.tenantLookup != nil {
		if tenant := p.tenantLookup(cluster); tenant != "" {
			req.Header.Set("X-Scope-OrgID", tenant)
		}
	}
	resp, err := historyClient(pfInfo.HTTPClient).Do(req)
	if err != nil {
		// Only a dead tunnel is dropped, and it is stopped first: deleting a
		// live one from the pool would leave it running with nothing to reap
		// it. Missing gateway credentials or a slow query say nothing about
		// the tunnel.
		var authErr *MimirAuthError
		if !errors.As(err, &authErr) && isPortForwardLikelyDead(err) {
			if p.portForwardPool.CompareAndDelete(mimirPoolKey(cluster, info), pfInfo) {
				_ = p.k8s.StopPortForward(pfInfo.PortForward.ID)
			}
			return nil, fmt.Errorf("failed to query mimir: %w: %w", errPortForwardDropped, err)
		}
		return nil, fmt.Errorf("failed to query mimir: %w", err)
	}
	return promBody(resp, "mimir")
}
