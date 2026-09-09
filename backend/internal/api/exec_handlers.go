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
	"strings"
	"time"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
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

func (h *Handler) detectAndCreateShellExecutor(clientset kubernetes.Interface, config *rest.Config, namespace, pod, container string, conn *websocket.Conn) (remotecommand.Executor, error) {
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

	_ = conn.WriteMessage(websocket.TextMessage, []byte("Detecting available shell...\r\n"))

	for i, cmd := range shellCommands {
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
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				err = testExec.StreamWithContext(ctx, remotecommand.StreamOptions{
					Stdout: &stdout,
					Stderr: &stderr,
				})
				if err == nil && strings.TrimSpace(stdout.String()) == "test" {
					_ = conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("Connected using %s\r\n", shellPath)))
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

	clientset, config, err := h.k8s.GetClientAndConfig(cluster)
	if err != nil {
		log.Printf("Failed to get cluster client: %v", err)
		_ = conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("Error: Failed to connect to cluster: %v", err)))
		return
	}

	exec, err := h.detectAndCreateShellExecutor(clientset, config, namespace, pod, container, conn)
	if err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("\r\nError: %s\r\n", err.Error())))
		return
	}

	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	resizeChan := make(chan remotecommand.TerminalSize, 10)

	go func() {
		defer func() { _ = stdoutWriter.Close() }()
		err := exec.StreamWithContext(ctx, remotecommand.StreamOptions{
			Stdin:             stdinReader,
			Stdout:            stdoutWriter,
			Stderr:            stdoutWriter,
			Tty:               true,
			TerminalSizeQueue: &terminalSizeQueue{resizeChan: resizeChan},
		})
		if err != nil {
			log.Printf("Exec stream error: %v", err)
			_ = conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("Exec error: %v", err)))
		}
	}()

	go func() {
		time.Sleep(500 * time.Millisecond)
		_, _ = stdinWriter.Write([]byte("export PS1='$ ' && export PROMPT='$ '\r"))
		time.Sleep(50 * time.Millisecond)
		_, _ = stdinWriter.Write([]byte("PS1='$ '\r"))
		time.Sleep(50 * time.Millisecond)
		_, _ = stdinWriter.Write([]byte("clear\r"))
	}()

	go func() {
		buf := make([]byte, 1024)
		for {
			select {
			case <-ctx.Done():
				return
			default:
				n, err := stdoutReader.Read(buf)
				if err != nil {
					if err != io.EOF {
						log.Printf("Read error: %v", err)
					}
					return
				}
				if n > 0 {
					if err := conn.WriteMessage(websocket.BinaryMessage, buf[:n]); err != nil {
						log.Printf("Write error: %v", err)
						return
					}
				}
			}
		}
	}()

	for {
		messageType, data, err := conn.ReadMessage()
		if err != nil {
			log.Printf("Read message error: %v", err)
			break
		}

		switch messageType {
		case websocket.TextMessage:
			var msg map[string]interface{}
			if err := jsonv2.Unmarshal(data, &msg); err == nil {
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
				_, _ = stdinWriter.Write(data)
			}
		case websocket.BinaryMessage:
			_, _ = stdinWriter.Write(data)
		}
	}
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

	clientset, config, err := h.k8s.GetClientAndConfig(cluster)
	if err != nil {
		log.Printf("Failed to get cluster client: %v", err)
		_ = conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("Error: Failed to connect to cluster: %v", err)))
		return
	}

	_, err = clientset.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
	if err != nil {
		log.Printf("Failed to get node: %v", err)
		_ = conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("Error: Failed to get node: %v", err)))
		return
	}

	var debugPodName string
	var debugNamespace = "default"

	pods, err := clientset.CoreV1().Pods("").List(context.Background(), metav1.ListOptions{
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

		_, err = clientset.CoreV1().Pods(debugNamespace).Create(context.Background(), debugPod, metav1.CreateOptions{})
		if err != nil {
			log.Printf("Failed to create debug pod: %v", err)
			_ = conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("Error: Failed to create debug pod: %v", err)))
			return
		}

		_ = conn.WriteMessage(websocket.TextMessage, []byte("Creating debug pod...\r\n"))

		for i := 0; i < 30; i++ {
			pod, err := clientset.CoreV1().Pods(debugNamespace).Get(context.Background(), debugPodName, metav1.GetOptions{})
			if err == nil && pod.Status.Phase == v1.PodRunning {
				break
			}
			time.Sleep(time.Second)
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
		_ = conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("Error: Failed to create executor: %v", err)))
		return
	}

	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	resizeChan := make(chan remotecommand.TerminalSize, 10)

	go func() {
		defer func() { _ = stdoutWriter.Close() }()
		err := exec.StreamWithContext(ctx, remotecommand.StreamOptions{
			Stdin:             stdinReader,
			Stdout:            stdoutWriter,
			Stderr:            stdoutWriter,
			Tty:               true,
			TerminalSizeQueue: &terminalSizeQueue{resizeChan: resizeChan},
		})
		if err != nil {
			log.Printf("Exec stream error: %v", err)
			_ = conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("Exec error: %v", err)))
		}
	}()

	go func() {
		time.Sleep(500 * time.Millisecond)
		_, _ = stdinWriter.Write([]byte("export PS1='$ ' && export PROMPT='$ '\r"))
		time.Sleep(50 * time.Millisecond)
		_, _ = stdinWriter.Write([]byte("PS1='$ '\r"))
		time.Sleep(50 * time.Millisecond)
		_, _ = stdinWriter.Write([]byte("clear\r"))
	}()

	go func() {
		buf := make([]byte, 1024)
		for {
			select {
			case <-ctx.Done():
				return
			default:
				n, err := stdoutReader.Read(buf)
				if err != nil {
					if err != io.EOF {
						log.Printf("Read error: %v", err)
					}
					return
				}
				if n > 0 {
					if err := conn.WriteMessage(websocket.BinaryMessage, buf[:n]); err != nil {
						log.Printf("Write error: %v", err)
						return
					}
				}
			}
		}
	}()

	for {
		messageType, data, err := conn.ReadMessage()
		if err != nil {
			log.Printf("Read message error: %v", err)
			break
		}

		switch messageType {
		case websocket.TextMessage:
			var msg map[string]interface{}
			if err := jsonv2.Unmarshal(data, &msg); err == nil {
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
				_, _ = stdinWriter.Write(data)
			}
		case websocket.BinaryMessage:
			_, _ = stdinWriter.Write(data)
		}
	}
}
