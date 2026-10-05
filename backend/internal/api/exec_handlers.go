package api

import (
	"bytes"
	"context"
	jsonv2 "encoding/json/v2"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"io"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"log"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

// execCheckOrigin allows the origins the Electron app really sends: none, the
// opaque "null" and "file://" origins of packaged builds, and the localhost dev
// server. It mirrors the main websocket transport.
func execCheckOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" || origin == "file://" || origin == "null" {
		return true
	}
	return strings.HasPrefix(origin, "http://localhost:")
}

var upgrader = websocket.Upgrader{
	CheckOrigin: execCheckOrigin,
}

const (
	execWriteTimeout = 10 * time.Second
	execPingInterval = 30 * time.Second
	execPongWait     = 60 * time.Second
)

// execConn serializes writes to a websocket connection: gorilla panics on
// concurrent writers, and several goroutines write to the terminal.
type execConn struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (c *execConn) write(messageType int, data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(execWriteTimeout))
	return c.conn.WriteMessage(messageType, data)
}

func (c *execConn) writeText(s string) error {
	return c.write(websocket.TextMessage, []byte(s))
}

// closeNormally sends a close frame and closes the connection, which also
// unblocks the reader.
func (c *execConn) closeNormally() {
	_ = c.conn.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		time.Now().Add(time.Second))
	_ = c.conn.Close()
}

type execMessage struct {
	messageType int
	data        []byte
}

// recoverExec keeps a panic in a session goroutine from killing the backend.
func recoverExec(what string) {
	if r := recover(); r != nil {
		log.Printf("panic in %s: %v\n%s", what, r, debug.Stack())
	}
}

// startExecReader reads the client's messages on its own goroutine so that a
// departed client is noticed (ctx is cancelled) even while the handler is
// busy detecting a shell or waiting on the pod. It also pings the client and
// enforces a read deadline that pongs and messages extend.
func startExecReader(conn *websocket.Conn) (context.Context, context.CancelFunc, <-chan execMessage) {
	ctx, cancel := context.WithCancel(context.Background())
	msgs := make(chan execMessage, 64)

	_ = conn.SetReadDeadline(time.Now().Add(execPongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(execPongWait))
	})

	go func() {
		defer close(msgs)
		defer cancel()
		defer recoverExec("exec reader")
		for {
			messageType, data, err := conn.ReadMessage()
			if err != nil {
				log.Printf("Read message error: %v", err)
				return
			}
			_ = conn.SetReadDeadline(time.Now().Add(execPongWait))
			select {
			case msgs <- execMessage{messageType, data}:
			case <-ctx.Done():
				return
			}
		}
	}()

	go func() {
		defer recoverExec("exec ping")
		ticker := time.NewTicker(execPingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(execWriteTimeout)); err != nil {
					cancel()
					return
				}
			}
		}
	}()

	return ctx, cancel, msgs
}

// runExecSession pipes the client's messages to the executor and its output
// back until either side ends. When the remote process ends the connection is
// closed, so the handler never waits on a dead shell.
func runExecSession(ctx context.Context, cancel context.CancelFunc, ec *execConn, msgs <-chan execMessage, exec remotecommand.Executor) {
	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()
	defer func() {
		cancel()
		_ = stdinWriter.Close()
		_ = stdoutReader.Close()
	}()

	resizeChan := make(chan remotecommand.TerminalSize, 10)

	// Unblock any stdin writer as soon as the session is over.
	go func() {
		<-ctx.Done()
		_ = stdinReader.Close()
	}()

	streamDone := make(chan struct{})
	go func() {
		defer close(streamDone)
		defer recoverExec("exec stream")
		defer func() {
			_ = stdinReader.Close()
			_ = stdoutWriter.Close()
		}()
		err := exec.StreamWithContext(ctx, remotecommand.StreamOptions{
			Stdin:             stdinReader,
			Stdout:            stdoutWriter,
			Stderr:            stdoutWriter,
			Tty:               true,
			TerminalSizeQueue: &terminalSizeQueue{resizeChan: resizeChan},
		})
		if err != nil && ctx.Err() == nil {
			log.Printf("Exec stream error: %v", err)
			_ = ec.writeText(fmt.Sprintf("Exec error: %v", err))
		}
	}()

	go func() {
		defer recoverExec("exec prompt init")
		for i, line := range []string{
			"export PS1='$ ' && export PROMPT='$ '\r",
			"PS1='$ '\r",
			"clear\r",
		} {
			wait := 50 * time.Millisecond
			if i == 0 {
				wait = 500 * time.Millisecond
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			if _, err := stdinWriter.Write([]byte(line)); err != nil {
				return
			}
		}
	}()

	pumpDone := make(chan struct{})
	go func() {
		defer close(pumpDone)
		defer recoverExec("exec output")
		buf := make([]byte, 1024)
		for {
			n, err := stdoutReader.Read(buf)
			if n > 0 {
				if werr := ec.write(websocket.BinaryMessage, buf[:n]); werr != nil {
					log.Printf("Write error: %v", werr)
					cancel()
					return
				}
			}
			if err != nil {
				if err != io.EOF && err != io.ErrClosedPipe {
					log.Printf("Read error: %v", err)
				}
				return
			}
		}
	}()

	// Once the remote side is done and its output is flushed, end the
	// connection so the client learns the shell exited.
	go func() {
		<-streamDone
		<-pumpDone
		ec.closeNormally()
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case m, ok := <-msgs:
			if !ok {
				return
			}
			switch m.messageType {
			case websocket.TextMessage:
				var msg map[string]interface{}
				if err := jsonv2.Unmarshal(m.data, &msg); err == nil {
					if msg["type"] == "resize" {
						if cols, ok := msg["cols"].(float64); ok {
							if rows, ok := msg["rows"].(float64); ok {
								select {
								case resizeChan <- remotecommand.TerminalSize{
									Width:  uint16(cols),
									Height: uint16(rows),
								}:
								default:
								}
							}
						}
					}
				} else {
					_, _ = stdinWriter.Write(m.data)
				}
			case websocket.BinaryMessage:
				_, _ = stdinWriter.Write(m.data)
			}
		}
	}
}

type terminalSizeQueue struct {
	resizeChan chan remotecommand.TerminalSize
}

func (t *terminalSizeQueue) Next() *remotecommand.TerminalSize {
	size, ok := <-t.resizeChan
	if !ok {
		return nil
	}
	return &size
}

func (h *Handler) detectAndCreateShellExecutor(ctx context.Context, clientset kubernetes.Interface, config *rest.Config, namespace, pod, container string, conn *execConn) (remotecommand.Executor, error) {
	shellCommands := [][]string{
		{"/bin/bash", "-i"},
		{"/bin/sh", "-i"},
		{"/usr/bin/bash", "-i"},
		{"/usr/bin/sh", "-i"},
		{"/bin/ash", "-i"},
		{"/bin/dash", "-i"},
		{"/bin/zsh", "-i"},
		{"/usr/bin/zsh", "-i"},
		{"/bin/ksh", "-i"},
		{"/usr/bin/ksh", "-i"},
		{"/bin/tcsh", "-i"},
		{"/usr/bin/tcsh", "-i"},
		{"/bin/csh", "-i"},
		{"/usr/bin/csh", "-i"},
		{"/bin/busybox", "sh", "-i"},
		{"/usr/bin/busybox", "sh", "-i"},
		{"/usr/local/bin/bash", "-i"},
		{"/usr/local/bin/sh", "-i"},
		{"/usr/local/bin/zsh", "-i"},
		{"/opt/bin/bash", "-i"},
		{"/opt/bin/sh", "-i"},
		{"cmd", "/c", "powershell"},
		{"powershell"},
		{"pwsh"},
		{"sh", "-i"},
		{"bash", "-i"},
		{"ash", "-i"},
		{"dash", "-i"},
	}

	var lastErr error
	var testedShells []string

	_ = conn.writeText("Detecting available shell...\r\n")

	for i, cmd := range shellCommands {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		shellPath := cmd[0]
		testedShells = append(testedShells, shellPath)

		req := clientset.CoreV1().RESTClient().Post().
			Resource("pods").
			Name(pod).
			Namespace(namespace).
			SubResource("exec")

		req.VersionedParams(&v1.PodExecOptions{
			Container: container,
			Command:   cmd,
			Stdin:     true,
			Stdout:    true,
			Stderr:    true,
			TTY:       true,
		}, scheme.ParameterCodec)

		executor, err := remotecommand.NewSPDYExecutor(config, "POST", req.URL())
		if err == nil {
			testReq := clientset.CoreV1().RESTClient().Post().
				Resource("pods").
				Name(pod).
				Namespace(namespace).
				SubResource("exec")

			var testCommand []string
			if shellPath == "cmd" || shellPath == "powershell" || shellPath == "pwsh" {
				testCommand = append(cmd, "echo", "test")
			} else {
				testCommand = append(cmd, "-c", "echo test")
			}

			testReq.VersionedParams(&v1.PodExecOptions{
				Container: container,
				Command:   testCommand,
				Stdin:     false,
				Stdout:    true,
				Stderr:    true,
				TTY:       false,
			}, scheme.ParameterCodec)

			testExec, err := remotecommand.NewSPDYExecutor(config, "POST", testReq.URL())
			if err == nil {
				var stdout, stderr bytes.Buffer
				probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
				err = testExec.StreamWithContext(probeCtx, remotecommand.StreamOptions{
					Stdout: &stdout,
					Stderr: &stderr,
				})
				cancel()
				if err == nil && strings.TrimSpace(stdout.String()) == "test" {
					_ = conn.writeText(fmt.Sprintf("Connected using %s\r\n", shellPath))
					log.Printf("Successfully connected to pod %s/%s using shell: %v", namespace, pod, cmd)
					return executor, nil
				}
			}
		}
		lastErr = err

		if (i+1)%5 == 0 {
			log.Printf("Tested %d/%d shell options for pod %s/%s", i+1, len(shellCommands), namespace, pod)
		}
	}

	errMsg := fmt.Sprintf("No available shell found in container after testing %d options", len(testedShells))
	if lastErr != nil {
		errMsg = fmt.Sprintf("%s. Last error: %v", errMsg, lastErr)
	}
	log.Printf("%s. Tested shells: %v", errMsg, testedShells)

	if len(testedShells) > 5 {
		testedShells = testedShells[:5]
	}
	return nil, fmt.Errorf("%s\r\nTested: %v\r\nContainer may not have a shell installed or may be using a custom entrypoint", errMsg, testedShells)
}

func (h *Handler) HandleExecWebSocket(c *gin.Context) {
	cluster := c.Query("cluster")
	namespace := c.Query("namespace")
	pod := c.Query("pod")
	container := c.Query("container")

	if cluster == "" || namespace == "" || pod == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cluster, namespace, and pod parameters are required"})
		return
	}

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("Failed to upgrade websocket: %v", err)
		return
	}
	defer func() { _ = conn.Close() }()

	ec := &execConn{conn: conn}
	ctx, cancel, msgs := startExecReader(conn)
	defer cancel()

	clientset, config, err := h.k8s.GetClientAndConfig(cluster)
	if err != nil {
		log.Printf("Failed to get cluster client: %v", err)
		_ = ec.writeText(fmt.Sprintf("Error: Failed to connect to cluster: %v", err))
		return
	}

	exec, err := h.detectAndCreateShellExecutor(ctx, clientset, config, namespace, pod, container, ec)
	if err != nil {
		if ctx.Err() == nil {
			_ = ec.writeText(fmt.Sprintf("\r\nError: %s\r\n", err.Error()))
		}
		return
	}

	runExecSession(ctx, cancel, ec, msgs, exec)
}

func (h *Handler) HandleNodeExecWebSocket(c *gin.Context) {
	cluster := c.Query("cluster")
	nodeName := c.Query("node")

	if cluster == "" || nodeName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cluster and node parameters are required"})
		return
	}

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("Failed to upgrade websocket: %v", err)
		return
	}
	defer func() { _ = conn.Close() }()

	ec := &execConn{conn: conn}
	ctx, cancel, msgs := startExecReader(conn)
	defer cancel()

	clientset, config, err := h.k8s.GetClientAndConfig(cluster)
	if err != nil {
		log.Printf("Failed to get cluster client: %v", err)
		_ = ec.writeText(fmt.Sprintf("Error: Failed to connect to cluster: %v", err))
		return
	}

	_, err = clientset.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		log.Printf("Failed to get node: %v", err)
		_ = ec.writeText(fmt.Sprintf("Error: Failed to get node: %v", err))
		return
	}

	var debugPodName string
	var debugNamespace = "default"

	pods, err := clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("spec.nodeName=%s", nodeName),
		LabelSelector: "app=node-debugger",
	})
	if err == nil && len(pods.Items) > 0 {
		for _, pod := range pods.Items {
			if pod.Status.Phase == v1.PodRunning {
				debugPodName = pod.Name
				debugNamespace = pod.Namespace
				break
			}
		}
	}

	if debugPodName == "" {
		debugPodName = fmt.Sprintf("node-debugger-%s-%d", nodeName, time.Now().Unix())
		debugPod := &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      debugPodName,
				Namespace: debugNamespace,
				Labels:    map[string]string{"app": "node-debugger", "node": nodeName},
			},
			Spec: v1.PodSpec{
				NodeName:    nodeName,
				HostPID:     true,
				HostIPC:     true,
				HostNetwork: true,
				Containers: []v1.Container{
					{
						Name:    "debugger",
						Image:   "alpine",
						Command: []string{"sh", "-c", "sleep infinity"},
						SecurityContext: &v1.SecurityContext{
							Privileged: func(b bool) *bool { return &b }(true),
						},
						VolumeMounts: []v1.VolumeMount{
							{Name: "host-root", MountPath: "/host"},
						},
					},
				},
				Volumes: []v1.Volume{
					{
						Name: "host-root",
						VolumeSource: v1.VolumeSource{
							HostPath: &v1.HostPathVolumeSource{Path: "/"},
						},
					},
				},
				RestartPolicy: v1.RestartPolicyNever,
				Tolerations:   []v1.Toleration{{Operator: v1.TolerationOpExists}},
			},
		}

		// Not tied to ctx: a create the server may already have accepted
		// should not be abandoned half-way by a client that just left.
		_, err = clientset.CoreV1().Pods(debugNamespace).Create(context.Background(), debugPod, metav1.CreateOptions{})
		if err != nil {
			log.Printf("Failed to create debug pod: %v", err)
			_ = ec.writeText(fmt.Sprintf("Error: Failed to create debug pod: %v", err))
			return
		}

		_ = ec.writeText("Creating debug pod...\r\n")

		for i := 0; i < 30; i++ {
			pod, err := clientset.CoreV1().Pods(debugNamespace).Get(ctx, debugPodName, metav1.GetOptions{})
			if err == nil && pod.Status.Phase == v1.PodRunning {
				break
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
		}
	}

	req := clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(debugPodName).
		Namespace(debugNamespace).
		SubResource("exec")

	command := []string{"chroot", "/host", "/bin/sh", "-c", "bash || sh"}
	req.VersionedParams(&v1.PodExecOptions{
		Container: "debugger",
		Command:   command,
		Stdin:     true,
		Stdout:    true,
		Stderr:    true,
		TTY:       true,
	}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(config, "POST", req.URL())
	if err != nil {
		log.Printf("Failed to create executor: %v", err)
		_ = ec.writeText(fmt.Sprintf("Error: Failed to create executor: %v", err))
		return
	}

	runExecSession(ctx, cancel, ec, msgs, exec)
}
