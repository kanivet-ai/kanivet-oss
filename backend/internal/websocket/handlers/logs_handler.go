package handlers

import (
	"bufio"
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"log"
	"math/rand/v2"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kanivet/backend/internal/k8s"
	"github.com/kanivet/backend/internal/websocket/core"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
)

const (
	logBatchSize     = 200
	logFlushInterval = 100 * time.Millisecond
	maxLineBytes     = 1024 * 1024
	// logBatchBytes bounds the raw size of one batch message, so a batch of
	// long lines does not exceed the connection's message size limit.
	logBatchBytes = 512 * 1024
	// maxTailLines bounds the tail a client may ask for.
	maxTailLines = 100000
	// logSendRetryBound is how long a batch waits for a full send buffer.
	logSendRetryBound = 10 * time.Second
)

// followRetryDelay is the first wait before a pod's log stream is reopened;
// consecutive failures double it up to followMaxRetryDelay. Variables so
// tests can shrink them.
var (
	followRetryDelay    = 2 * time.Second
	followMaxRetryDelay = 30 * time.Second
	watchRetryDelay     = time.Second
	watchMaxRetryDelay  = 30 * time.Second
)

// errStreamSuperseded is returned for sends of a log stream that a newer
// start with the same key has replaced.
var errStreamSuperseded = stderrors.New("log stream superseded")

// logSender is where a log stream's messages go.
type logSender interface {
	Send(data []byte) error
}

// fencedConn is a log stream's view of its connection. Once fenced it sends
// nothing more, so a replaced stream cannot slip batches in after the new
// stream's "connected" (the client drops batches whose sequence is not above
// the last one it saw, which would eat the new stream's first batches).
type fencedConn struct {
	conn   *core.Connection
	mu     sync.Mutex
	fenced bool
}

func (f *fencedConn) Send(data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fenced {
		return errStreamSuperseded
	}
	return f.conn.Send(data)
}

func (f *fencedConn) fence() {
	f.mu.Lock()
	f.fenced = true
	f.mu.Unlock()
}

type LogsHandler struct {
	k8sClient     k8s.Interface
	activeStreams *sync.Map
}

type logStreamKey struct {
	connectionID string
	key          string
}

type logStream struct {
	cancel context.CancelFunc
	done   chan struct{}
	conn   *fencedConn
}

type logsPayload struct {
	Action            string `json:"action"`
	Key               string `json:"key"`
	Cluster           string `json:"cluster"`
	Namespace         string `json:"namespace"`
	Name              string `json:"name"`
	Container         string `json:"container,omitempty"`
	ResourceType      string `json:"resourceType,omitempty"`
	SelectedContainer string `json:"selectedContainer,omitempty"`
	TailLines         int    `json:"tailLines,omitempty"`
	Follow            bool   `json:"follow"`
	Previous          bool   `json:"previous,omitempty"`
}

type logEntry struct {
	data      string
	podName   string
	container string
	timestamp time.Time
}

func NewLogsHandler(k8sClient k8s.Interface) *LogsHandler {
	return &LogsHandler{
		k8sClient:     k8sClient,
		activeStreams: &sync.Map{},
	}
}

func (h *LogsHandler) MessageTypes() []core.MessageType {
	return []core.MessageType{"logs"}
}

func (h *LogsHandler) HandleMessage(ctx context.Context, conn *core.Connection, msg *core.IncomingMessage) error {
	var payload logsPayload
	if err := msg.UnmarshalPayload(&payload); err != nil {
		return fmt.Errorf("failed to unmarshal logs payload: %w", err)
	}

	switch payload.Action {
	case "start":
		return h.handleStart(ctx, conn, payload)
	case "stop":
		return h.handleStop(conn, payload.Key)
	default:
		return fmt.Errorf("unknown action: %s", payload.Action)
	}
}

func (h *LogsHandler) handleStart(ctx context.Context, conn *core.Connection, payload logsPayload) error {
	streamKey := logStreamKey{connectionID: string(conn.ID()), key: payload.Key}

	// Never block the connection's read loop waiting for the old stream to
	// drain: fence it (it can send nothing more) and cancel it, and let
	// CompareAndDelete keep ownership straight.
	if existing, exists := h.activeStreams.Load(streamKey); exists {
		if stream, ok := existing.(*logStream); ok {
			stream.conn.fence()
			stream.cancel()
		}
	}

	streamCtx, cancel := context.WithCancel(ctx)
	stream := &logStream{cancel: cancel, done: make(chan struct{}), conn: &fencedConn{conn: conn}}
	h.activeStreams.Store(streamKey, stream)

	h.sendConnected(conn, payload.Key)

	go func() {
		defer close(stream.done)
		defer h.activeStreams.CompareAndDelete(streamKey, stream)
		defer cancel()

		if err := h.run(streamCtx, stream.conn, payload); err != nil {
			if !errors.IsNotFound(err) && streamCtx.Err() == nil {
				log.Printf("Error streaming logs for %s: %v", payload.Key, err)
				h.sendError(stream.conn, payload.Key, fmt.Sprintf("Failed to stream logs: %v", err))
			}
		}
	}()

	return nil
}

func (h *LogsHandler) handleStop(conn *core.Connection, key string) error {
	streamKey := logStreamKey{connectionID: string(conn.ID()), key: key}
	if existing, exists := h.activeStreams.Load(streamKey); exists {
		if stream, ok := existing.(*logStream); ok {
			stream.cancel()
		}
	}
	return nil
}

func (h *LogsHandler) run(ctx context.Context, conn logSender, p logsPayload) error {
	client, err := h.k8sClient.GetClientForCluster(p.Cluster)
	if err != nil {
		return fmt.Errorf("failed to get client for cluster %s: %w", p.Cluster, err)
	}

	kind := strings.ToLower(p.ResourceType)
	if kind == "" {
		kind = "pod"
	}
	container := p.Container
	if container == "" {
		container = p.SelectedContainer
	}
	tail := int64(p.TailLines)
	if tail <= 0 {
		tail = 1000
	}
	tail = min(tail, maxTailLines)
	follow := p.Follow && !p.Previous

	var podNames []string
	var selector string
	var sentPods string

	if kind == "pod" {
		podNames = []string{p.Name}
		pod, err := client.CoreV1().Pods(p.Namespace).Get(ctx, p.Name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get pod: %w", err)
		}
		h.sendContainers(conn, p.Key, []corev1.Pod{*pod})
		if container == "" && len(pod.Spec.Containers) > 0 {
			container = pod.Spec.Containers[0].Name
		}
	} else {
		selector, err = workloadSelector(ctx, client, kind, p.Namespace, p.Name)
		if err != nil {
			return err
		}
		pods, err := client.CoreV1().Pods(p.Namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
		if err != nil {
			return fmt.Errorf("failed to list pods: %w", err)
		}
		sentPods = h.sendPods(conn, p.Key, pods.Items)
		h.sendContainers(conn, p.Key, pods.Items)
		for i := range pods.Items {
			podNames = append(podNames, pods.Items[i].Name)
		}
		if container == "" && len(pods.Items) > 0 && len(pods.Items[0].Spec.Containers) > 0 {
			container = pods.Items[0].Spec.Containers[0].Name
		}
	}

	fetchStart := time.Now()
	tailCtx := ctx
	if selector != "" {
		var cancelTail context.CancelFunc
		tailCtx, cancelTail = context.WithTimeout(ctx, 5*time.Second)
		defer cancelTail()
	}
	var mu sync.Mutex
	initial := make([]logEntry, 0, 1024)
	lastTs := make(map[string]time.Time)
	var fetchErr error
	var wg sync.WaitGroup

	for _, pod := range podNames {
		wg.Add(1)
		go func(pod string) {
			defer wg.Done()
			entries, err := fetchTail(tailCtx, client, p.Namespace, pod, container, tail, p.Previous)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				fetchErr = err
				return
			}
			initial = append(initial, entries...)
			if n := len(entries); n > 0 {
				lastTs[pod] = entries[n-1].timestamp
			}
		}(pod)
	}
	wg.Wait()

	if kind == "pod" && fetchErr != nil {
		return fetchErr
	}
	if len(initial) > 1 {
		sortLogEntries(initial)
	}

	seq := 1
	h.sendEntryBatches(ctx, conn, p.Key, initial, &seq)

	if !follow {
		h.sendEnd(conn, p.Key)
		return nil
	}

	out := make(chan logEntry, 4096)
	var streaming sync.Map
	var followWg sync.WaitGroup

	startFollow := func(pod string, since time.Time, tailForNew int64) {
		if _, loaded := streaming.LoadOrStore(pod, struct{}{}); loaded {
			return
		}
		followWg.Add(1)
		go func() {
			defer followWg.Done()
			defer streaming.Delete(pod)
			followPod(ctx, client, p.Namespace, pod, container, since, tailForNew, out)
		}()
	}

	for _, pod := range podNames {
		since := lastTs[pod]
		if since.IsZero() {
			since = fetchStart
		}
		startFollow(pod, since, 0)
	}

	if selector != "" {
		go h.watchPods(ctx, conn, client, p, selector, sentPods, tail, startFollow)
	}

	done := make(chan struct{})
	if selector == "" {
		go func() {
			followWg.Wait()
			close(done)
		}()
	}

	buffer := make([]logEntry, 0, logBatchSize)
	ticker := time.NewTicker(logFlushInterval)
	defer ticker.Stop()

	flush := func() {
		h.sendEntryBatches(ctx, conn, p.Key, buffer, &seq)
		buffer = buffer[:0]
	}

	for {
		select {
		case <-ctx.Done():
			flush()
			return nil
		case <-done:
			for {
				select {
				case e := <-out:
					buffer = append(buffer, e)
					continue
				default:
				}
				break
			}
			flush()
			h.sendEnd(conn, p.Key)
			return nil
		case e := <-out:
			buffer = append(buffer, e)
			if len(buffer) >= logBatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

func workloadSelector(ctx context.Context, client kubernetes.Interface, kind, namespace, name string) (string, error) {
	var sel *metav1.LabelSelector
	switch kind {
	case "deployment":
		obj, err := client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", fmt.Errorf("failed to get deployment: %w", err)
		}
		sel = obj.Spec.Selector
	case "statefulset":
		obj, err := client.AppsV1().StatefulSets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", fmt.Errorf("failed to get statefulset: %w", err)
		}
		sel = obj.Spec.Selector
	case "daemonset":
		obj, err := client.AppsV1().DaemonSets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", fmt.Errorf("failed to get daemonset: %w", err)
		}
		sel = obj.Spec.Selector
	case "replicaset":
		obj, err := client.AppsV1().ReplicaSets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", fmt.Errorf("failed to get replicaset: %w", err)
		}
		sel = obj.Spec.Selector
	case "job":
		obj, err := client.BatchV1().Jobs(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", fmt.Errorf("failed to get job: %w", err)
		}
		sel = obj.Spec.Selector
	default:
		return "", fmt.Errorf("unsupported resource type: %s", kind)
	}
	return metav1.FormatLabelSelector(sel), nil
}

func fetchTail(ctx context.Context, client kubernetes.Interface, namespace, pod, container string, tail int64, previous bool) ([]logEntry, error) {
	opts := &corev1.PodLogOptions{Timestamps: true, Previous: previous, TailLines: &tail}
	if container != "" {
		opts.Container = container
	}
	stream, err := client.CoreV1().Pods(namespace).GetLogs(pod, opts).Stream(ctx)
	if err != nil {
		return nil, err
	}
	defer stream.Close()

	// tail comes from the client: do not reserve memory for it up front.
	entries := make([]logEntry, 0, min(tail, 1024))
	reader := bufio.NewReaderSize(stream, 64*1024)
	for {
		line, err := readLogLine(reader)
		if line != "" {
			entries = append(entries, parseLogLine(line, pod, container))
		}
		if err != nil || ctx.Err() != nil {
			return entries, nil
		}
	}
}

// readLogLine reads one line. A line longer than maxLineBytes is cut to that
// length and the rest of it skipped: bufio.Scanner stops at such a line for
// good, and a followed stream that retries from the same timestamp would hit
// it again every time. It returns the line it has read together with a read
// error, so the caller sees the last line of a stream that ends without a
// newline.
func readLogLine(r *bufio.Reader) (string, error) {
	var line []byte
	for {
		chunk, isPrefix, err := r.ReadLine()
		if room := maxLineBytes - len(line); room > 0 {
			line = append(line, chunk[:min(len(chunk), room)]...)
		}
		if err != nil {
			return string(line), err
		}
		if !isPrefix {
			return string(line), nil
		}
	}
}

// retryWait is the wait before attempt number failures+1: base, doubling per
// consecutive failure, up to max.
func retryWait(base, max time.Duration, failures int) time.Duration {
	return min(base<<min(failures, 6), max)
}

func sleepOrDone(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func podFinished(pod *corev1.Pod) bool {
	return pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed
}

// podGone reports whether a pod no longer exists or has finished for good, so
// its logs will not change.
func podGone(ctx context.Context, client kubernetes.Interface, namespace, name string) bool {
	pod, err := client.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return errors.IsNotFound(err)
	}
	return podFinished(pod)
}

// followPod streams a pod's logs into out. It keeps reopening the stream with
// capped backoff for as long as ctx lives: a pod in CrashLoopBackOff has no
// log stream most of the time, and its next container start must still be
// followed. It returns when ctx ends or the pod is gone or finished.
func followPod(ctx context.Context, client kubernetes.Interface, namespace, pod, container string, since time.Time, tailForNew int64, out chan<- logEntry) {
	lastSeen := since
	failures := 0

	for ctx.Err() == nil {
		opts := &corev1.PodLogOptions{Follow: true, Timestamps: true}
		if container != "" {
			opts.Container = container
		}
		if !lastSeen.IsZero() {
			st := metav1.NewTime(lastSeen)
			opts.SinceTime = &st
		} else if tailForNew > 0 {
			opts.TailLines = &tailForNew
		}

		stream, err := client.CoreV1().Pods(namespace).GetLogs(pod, opts).Stream(ctx)
		if err != nil {
			if errors.IsNotFound(err) || ctx.Err() != nil {
				return
			}
			if podGone(ctx, client, namespace, pod) {
				return
			}
			failures++
			if !sleepOrDone(ctx, retryWait(followRetryDelay, followMaxRetryDelay, failures-1)) {
				return
			}
			continue
		}

		got := false
		reader := bufio.NewReaderSize(stream, 64*1024)
		for {
			line, rerr := readLogLine(reader)
			if line != "" {
				e := parseLogLine(line, pod, container)
				if lastSeen.IsZero() || e.timestamp.After(lastSeen) {
					lastSeen = e.timestamp
					got = true
					failures = 0
					select {
					case out <- e:
					case <-ctx.Done():
						stream.Close()
						return
					}
				}
			}
			if rerr != nil || ctx.Err() != nil {
				break
			}
		}
		stream.Close()

		if ctx.Err() != nil {
			return
		}
		if p, err := client.CoreV1().Pods(namespace).Get(ctx, pod, metav1.GetOptions{}); err != nil {
			if errors.IsNotFound(err) {
				return
			}
		} else if !got && podFinished(p) {
			return
		}
		if !got {
			failures++
		}
		if !sleepOrDone(ctx, retryWait(followRetryDelay, followMaxRetryDelay, failures)) {
			return
		}
	}
}

// watchPods keeps the client's pod list current and starts following pods
// that appear. The pod set is kept from list and watch events, so an event
// does not cost a list; a watch that ends resumes from its last
// resourceVersion and a too-old one relists. lastSent is what the client
// already has.
func (h *LogsHandler) watchPods(ctx context.Context, conn logSender, client kubernetes.Interface, p logsPayload, selector, lastSent string, tail int64, startFollow func(pod string, since time.Time, tailForNew int64)) {
	pods := client.CoreV1().Pods(p.Namespace)
	known := map[string]*corev1.Pod{}
	rv := ""
	needList := true
	failures := 0

	resend := func() {
		list := make([]corev1.Pod, 0, len(known))
		for _, pod := range known {
			list = append(list, *pod)
		}
		sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
		infos := podInfoList(list)
		encoded, _ := json.Marshal(infos)
		if string(encoded) == lastSent {
			return
		}
		lastSent = string(encoded)
		h.send(conn, map[string]any{"type": "pods", "key": p.Key, "pods": infos})
	}

	for ctx.Err() == nil {
		if needList {
			list, err := pods.List(ctx, metav1.ListOptions{LabelSelector: selector})
			if err != nil {
				failures++
				if !sleepOrDone(ctx, retryWait(watchRetryDelay, watchMaxRetryDelay, failures-1)) {
					return
				}
				continue
			}
			previous := known
			known = make(map[string]*corev1.Pod, len(list.Items))
			for i := range list.Items {
				pod := &list.Items[i]
				known[pod.Name] = pod
				if _, seen := previous[pod.Name]; !seen {
					startFollow(pod.Name, time.Time{}, tail)
				}
			}
			rv = list.ResourceVersion
			needList = false
			resend()
		}

		seconds := 300 + rand.Int64N(301)
		watchCtx, cancel := context.WithTimeout(ctx, time.Duration(seconds)*time.Second+30*time.Second)
		started := time.Now()
		w, err := pods.Watch(watchCtx, metav1.ListOptions{
			LabelSelector:       selector,
			ResourceVersion:     rv,
			AllowWatchBookmarks: true,
			TimeoutSeconds:      &seconds,
		})
		if err != nil {
			cancel()
			if errors.IsGone(err) || errors.IsResourceExpired(err) {
				needList = true
			}
			failures++
			if !sleepOrDone(ctx, retryWait(watchRetryDelay, watchMaxRetryDelay, failures-1)) {
				return
			}
			continue
		}

		events := 0
	loop:
		for {
			select {
			case <-ctx.Done():
				break loop
			case ev, ok := <-w.ResultChan():
				if !ok {
					break loop
				}
				events++
				if ev.Type == watch.Error {
					if status, ok := ev.Object.(*metav1.Status); ok && (status.Code == 410 || status.Reason == metav1.StatusReasonExpired || status.Reason == metav1.StatusReasonGone) {
						needList = true
					}
					break loop
				}
				pod, ok := ev.Object.(*corev1.Pod)
				if !ok {
					continue
				}
				if pod.ResourceVersion != "" {
					rv = pod.ResourceVersion
				}
				switch ev.Type {
				case watch.Bookmark:
					continue
				case watch.Added:
					known[pod.Name] = pod
					startFollow(pod.Name, time.Time{}, tail)
				case watch.Modified:
					known[pod.Name] = pod
				case watch.Deleted:
					delete(known, pod.Name)
				}
				resend()
			}
		}
		w.Stop()
		cancel()

		if ctx.Err() != nil {
			return
		}
		// A watch that closes at once with nothing delivered is a failure,
		// not a reason to reconnect in a tight loop.
		if events == 0 && time.Since(started) < time.Second {
			failures++
			if !sleepOrDone(ctx, retryWait(watchRetryDelay, watchMaxRetryDelay, failures-1)) {
				return
			}
		} else {
			failures = 0
		}
	}
}

func parseLogLine(line, pod, container string) logEntry {
	ts := time.Now()
	data := line
	if idx := strings.IndexByte(line, ' '); idx > 0 {
		if parsed, err := time.Parse(time.RFC3339Nano, line[:idx]); err == nil {
			ts = parsed
			data = line[idx+1:]
		}
	}
	return logEntry{data: data, podName: pod, container: container, timestamp: ts}
}

func podInfoList(pods []corev1.Pod) []map[string]any {
	infos := make([]map[string]any, 0, len(pods))
	for i := range pods {
		pod := &pods[i]
		var restarts int32
		for _, cs := range pod.Status.ContainerStatuses {
			restarts += cs.RestartCount
		}
		ready := false
		for _, cond := range pod.Status.Conditions {
			if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
				ready = true
			}
		}
		containers := make([]string, 0, len(pod.Spec.Containers))
		for _, c := range pod.Spec.Containers {
			containers = append(containers, c.Name)
		}
		infos = append(infos, map[string]any{
			"name":         pod.Name,
			"status":       string(pod.Status.Phase),
			"ready":        ready,
			"restartCount": restarts,
			"containers":   containers,
		})
	}
	return infos
}

// sendPods sends the pod list and returns its encoding, which watchPods
// compares against to avoid resending an unchanged list.
func (h *LogsHandler) sendPods(conn logSender, key string, pods []corev1.Pod) string {
	infos := podInfoList(pods)
	encoded, _ := json.Marshal(infos)
	_ = h.send(conn, map[string]any{"type": "pods", "key": key, "pods": infos})
	return string(encoded)
}

func (h *LogsHandler) sendContainers(conn logSender, key string, pods []corev1.Pod) {
	seen := make(map[string]bool)
	containers := make([]map[string]any, 0)
	add := func(name string, init bool) {
		if seen[name] {
			return
		}
		seen[name] = true
		containers = append(containers, map[string]any{"name": name, "init": init})
	}
	for i := range pods {
		for _, c := range pods[i].Spec.Containers {
			add(c.Name, false)
		}
	}
	for i := range pods {
		for _, c := range pods[i].Spec.InitContainers {
			add(c.Name, true)
		}
	}
	_ = h.send(conn, map[string]any{"type": "containers", "key": key, "containers": containers})
}

// sendEntryBatches sends entries in batches bounded by count and size. A batch
// the client's buffer cannot take yet is retried, and one the connection
// refuses as too large is split. The sequence advances only for a batch that
// was sent, so a lost one is not hidden behind a number the client never saw.
func (h *LogsHandler) sendEntryBatches(ctx context.Context, conn logSender, key string, entries []logEntry, seq *int) {
	for start := 0; start < len(entries); {
		end, size := start, 0
		for end < len(entries) && end-start < logBatchSize && (end == start || size+len(entries[end].data) <= logBatchBytes) {
			size += len(entries[end].data)
			end++
		}
		if !h.sendBatch(ctx, conn, key, entries[start:end], seq) && ctx.Err() != nil {
			return
		}
		start = end
	}
}

func (h *LogsHandler) sendBatch(ctx context.Context, conn logSender, key string, entries []logEntry, seq *int) bool {
	lines := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		lines = append(lines, map[string]any{
			"data":      e.data,
			"podName":   e.podName,
			"container": e.container,
			"timestamp": e.timestamp.Format(time.RFC3339Nano),
		})
	}
	err := h.sendRetry(ctx, conn, map[string]any{"type": "batch", "key": key, "lines": lines, "sequence": *seq})
	switch {
	case err == nil:
		*seq++
		return true
	case stderrors.Is(err, core.ErrInvalidMessage) && len(entries) > 1:
		mid := len(entries) / 2
		ok1 := h.sendBatch(ctx, conn, key, entries[:mid], seq)
		ok2 := h.sendBatch(ctx, conn, key, entries[mid:], seq)
		return ok1 && ok2
	default:
		log.Printf("Dropped a batch of %d log lines: %v", len(entries), err)
		return false
	}
}

func (h *LogsHandler) sendConnected(conn logSender, key string) {
	_ = h.send(conn, map[string]any{"type": "connected", "key": key})
}

func (h *LogsHandler) sendEnd(conn logSender, key string) {
	_ = h.sendRetry(context.Background(), conn, map[string]any{"type": "end", "key": key})
}

func (h *LogsHandler) sendError(conn logSender, key, errorMsg string) {
	_ = h.sendRetry(context.Background(), conn, map[string]any{"type": "error", "key": key, "error": errorMsg})
}

func (h *LogsHandler) send(conn logSender, payload map[string]any) error {
	msg := core.NewOutgoingMessage("logs", payload)
	data, err := msg.Marshal()
	if err != nil {
		return err
	}
	if err := conn.Send(data); err != nil {
		if !stderrors.Is(err, errStreamSuperseded) {
			log.Printf("Failed to send logs message: %v", err)
		}
		return err
	}
	return nil
}

// sendRetry is send, but waits (up to logSendRetryBound) while the
// connection's send buffer is full instead of dropping the message. A slow
// client then slows the stream down, which the log reader already tolerates.
func (h *LogsHandler) sendRetry(ctx context.Context, conn logSender, payload map[string]any) error {
	msg := core.NewOutgoingMessage("logs", payload)
	data, err := msg.Marshal()
	if err != nil {
		return err
	}
	deadline := time.Now().Add(logSendRetryBound)
	delay := time.Millisecond
	for {
		err := conn.Send(data)
		if !stderrors.Is(err, core.ErrRateLimitExceeded) || !time.Now().Before(deadline) {
			if err != nil && !stderrors.Is(err, errStreamSuperseded) && !stderrors.Is(err, core.ErrInvalidMessage) {
				log.Printf("Failed to send logs message: %v", err)
			}
			return err
		}
		if !sleepOrDone(ctx, delay) {
			return ctx.Err()
		}
		if delay < 50*time.Millisecond {
			delay *= 2
		}
	}
}

// Shutdown cancels every active log stream and waits for the goroutines
// driving them to finish. Callers that own the handler's lifetime should use
// it so that in-flight streams do not outlive the server they belong to.
func (h *LogsHandler) Shutdown() {
	var streams []*logStream

	h.activeStreams.Range(func(key, value any) bool {
		if stream, ok := value.(*logStream); ok {
			stream.cancel()
			streams = append(streams, stream)
		}
		h.activeStreams.Delete(key)
		return true
	})

	for _, stream := range streams {
		<-stream.done
	}
}

func (h *LogsHandler) OnConnectionClose(conn *core.Connection) {
	h.activeStreams.Range(func(key, value any) bool {
		if streamKey, ok := key.(logStreamKey); ok && streamKey.connectionID == string(conn.ID()) {
			if stream, ok := value.(*logStream); ok {
				stream.cancel()
			}
			h.activeStreams.Delete(key)
		}
		return true
	})
}

func sortLogEntries(entries []logEntry) {
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].timestamp.Before(entries[j].timestamp)
	})
}
