package handlers

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
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
	followRetryDelay = 2 * time.Second
	maxFollowRetries = 30
)

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

func NewLogsHandler(k8sClient k8s.Interface) core.MessageHandler {
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
	// drain: cancel it and let CompareAndDelete keep ownership straight.
	if existing, exists := h.activeStreams.Load(streamKey); exists {
		if stream, ok := existing.(*logStream); ok {
			stream.cancel()
		}
	}

	streamCtx, cancel := context.WithCancel(ctx)
	stream := &logStream{cancel: cancel, done: make(chan struct{})}
	h.activeStreams.Store(streamKey, stream)

	h.sendConnected(conn, payload.Key)

	go func() {
		defer close(stream.done)
		defer h.activeStreams.CompareAndDelete(streamKey, stream)
		defer cancel()

		if err := h.run(streamCtx, conn, payload); err != nil {
			if !errors.IsNotFound(err) && streamCtx.Err() == nil {
				log.Printf("Error streaming logs for %s: %v", payload.Key, err)
				h.sendError(conn, payload.Key, fmt.Sprintf("Failed to stream logs: %v", err))
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

func (h *LogsHandler) run(ctx context.Context, conn *core.Connection, p logsPayload) error {
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
	follow := p.Follow && !p.Previous

	var podNames []string
	var selector string

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
		h.sendPods(conn, p.Key, pods.Items)
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
	h.sendEntryBatches(conn, p.Key, initial, &seq)

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
		go h.watchPods(ctx, conn, client, p, selector, tail, startFollow)
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
		h.sendEntryBatches(conn, p.Key, buffer, &seq)
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

	entries := make([]logEntry, 0, tail)
	scanner := bufio.NewScanner(stream)
	scanner.Buffer(make([]byte, 64*1024), maxLineBytes)
	for scanner.Scan() {
		if ctx.Err() != nil {
			return entries, nil
		}
		line := scanner.Text()
		if line == "" {
			continue
		}
		entries = append(entries, parseLogLine(line, pod, container))
	}
	return entries, nil
}

func followPod(ctx context.Context, client kubernetes.Interface, namespace, pod, container string, since time.Time, tailForNew int64, out chan<- logEntry) {
	lastSeen := since
	failures := 0

	for ctx.Err() == nil && failures < maxFollowRetries {
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
			failures++
			select {
			case <-ctx.Done():
				return
			case <-time.After(followRetryDelay):
			}
			continue
		}

		got := false
		scanner := bufio.NewScanner(stream)
		scanner.Buffer(make([]byte, 64*1024), maxLineBytes)
		for scanner.Scan() {
			if ctx.Err() != nil {
				break
			}
			line := scanner.Text()
			if line == "" {
				continue
			}
			e := parseLogLine(line, pod, container)
			if !lastSeen.IsZero() && !e.timestamp.After(lastSeen) {
				continue
			}
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
		stream.Close()

		if ctx.Err() != nil {
			return
		}
		if _, err := client.CoreV1().Pods(namespace).Get(ctx, pod, metav1.GetOptions{}); err != nil {
			return
		}
		if !got {
			failures++
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(followRetryDelay):
		}
	}
}

func (h *LogsHandler) watchPods(ctx context.Context, conn *core.Connection, client kubernetes.Interface, p logsPayload, selector string, tail int64, startFollow func(pod string, since time.Time, tailForNew int64)) {
	w, err := client.CoreV1().Pods(p.Namespace).Watch(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return
	}
	defer w.Stop()

	var lastSent string
	resend := func() {
		pods, err := client.CoreV1().Pods(p.Namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
		if err != nil {
			return
		}
		infos := podInfoList(pods.Items)
		encoded, _ := json.Marshal(infos)
		if string(encoded) == lastSent {
			return
		}
		lastSent = string(encoded)
		h.send(conn, map[string]any{"type": "pods", "key": p.Key, "pods": infos})
	}

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-w.ResultChan():
			if !ok {
				return
			}
			pod, ok := ev.Object.(*corev1.Pod)
			if !ok {
				continue
			}
			if ev.Type == watch.Added {
				startFollow(pod.Name, time.Time{}, tail)
			}
			resend()
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

func (h *LogsHandler) sendPods(conn *core.Connection, key string, pods []corev1.Pod) {
	h.send(conn, map[string]any{"type": "pods", "key": key, "pods": podInfoList(pods)})
}

func (h *LogsHandler) sendContainers(conn *core.Connection, key string, pods []corev1.Pod) {
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
	h.send(conn, map[string]any{"type": "containers", "key": key, "containers": containers})
}

func (h *LogsHandler) sendEntryBatches(conn *core.Connection, key string, entries []logEntry, seq *int) {
	for start := 0; start < len(entries); start += logBatchSize {
		end := min(start+logBatchSize, len(entries))
		lines := make([]map[string]any, 0, end-start)
		for _, e := range entries[start:end] {
			lines = append(lines, map[string]any{
				"data":      e.data,
				"podName":   e.podName,
				"container": e.container,
				"timestamp": e.timestamp.Format(time.RFC3339Nano),
			})
		}
		h.send(conn, map[string]any{"type": "batch", "key": key, "lines": lines, "sequence": *seq})
		*seq++
	}
}

func (h *LogsHandler) sendConnected(conn *core.Connection, key string) {
	h.send(conn, map[string]any{"type": "connected", "key": key})
}

func (h *LogsHandler) sendEnd(conn *core.Connection, key string) {
	h.send(conn, map[string]any{"type": "end", "key": key})
}

func (h *LogsHandler) sendError(conn *core.Connection, key, errorMsg string) {
	h.send(conn, map[string]any{"type": "error", "key": key, "error": errorMsg})
}

func (h *LogsHandler) send(conn *core.Connection, payload map[string]any) {
	msg := core.NewOutgoingMessage("logs", payload)
	if data, err := msg.Marshal(); err == nil {
		if err := conn.Send(data); err != nil {
			log.Printf("Failed to send logs message: %v", err)
		}
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
