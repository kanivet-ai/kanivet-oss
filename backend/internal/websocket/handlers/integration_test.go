package handlers_test

import (
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kanivet/backend/internal/k8s"
	"github.com/kanivet/backend/internal/websocket/handlers"
	ws "github.com/kanivet/backend/internal/websocket"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func int32Ptr(i int32) *int32 { return &i }

func seedFakeClientset() *fake.Clientset {
	return fake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "default"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "kube-system"}},

		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
			Status: corev1.NodeStatus{
				Conditions: []corev1.NodeCondition{
					{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
				},
				Allocatable: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("4"),
					corev1.ResourceMemory: resource.MustParse("16Gi"),
				},
				NodeInfo: corev1.NodeSystemInfo{Architecture: "amd64"},
			},
		},
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "node-2"},
			Status: corev1.NodeStatus{
				Conditions: []corev1.NodeCondition{
					{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
				},
				Allocatable: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("4"),
					corev1.ResourceMemory: resource.MustParse("16Gi"),
				},
				NodeInfo: corev1.NodeSystemInfo{Architecture: "amd64"},
			},
		},

		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "pod-1", Namespace: "default"},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "app",
						Image: "nginx:latest",
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("100m"),
								corev1.ResourceMemory: resource.MustParse("128Mi"),
							},
						},
					},
				},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "pod-2", Namespace: "default"},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: "app", Image: "redis:latest"},
				},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "pod-pending", Namespace: "default"},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: "app", Image: "busybox"},
				},
			},
			Status: corev1.PodStatus{Phase: corev1.PodPending},
		},

		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "default"},
			Spec: appsv1.DeploymentSpec{
				Replicas: int32Ptr(2),
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "web"}},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{{Name: "web", Image: "nginx"}},
					},
				},
			},
			Status: appsv1.DeploymentStatus{Replicas: 2, ReadyReplicas: 2},
		},

		&appsv1.StatefulSet{
			ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "default"},
			Spec: appsv1.StatefulSetSpec{
				Replicas: int32Ptr(1),
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "db"}},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "db"}},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{{Name: "db", Image: "postgres"}},
					},
				},
			},
			Status: appsv1.StatefulSetStatus{Replicas: 1, ReadyReplicas: 1},
		},

		&appsv1.DaemonSet{
			ObjectMeta: metav1.ObjectMeta{Name: "monitor", Namespace: "kube-system"},
			Status: appsv1.DaemonSetStatus{
				DesiredNumberScheduled: 2,
				NumberReady:            2,
			},
		},

		&corev1.Event{
			ObjectMeta:     metav1.ObjectMeta{Name: "event-1", Namespace: "default"},
			Type:           "Normal",
			Reason:         "Scheduled",
			Message:        "Successfully assigned pod-1",
			LastTimestamp:   metav1.NewTime(time.Now().Add(-1 * time.Minute)),
			InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "pod-1", Namespace: "default"},
		},
		&corev1.Event{
			ObjectMeta:     metav1.ObjectMeta{Name: "event-2", Namespace: "default"},
			Type:           "Warning",
			Reason:         "BackOff",
			Message:        "Back-off restarting failed container",
			LastTimestamp:   metav1.NewTime(time.Now().Add(-30 * time.Second)),
			InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "pod-pending", Namespace: "default"},
		},

		&batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{Name: "job-1", Namespace: "default"},
			Status:     batchv1.JobStatus{Succeeded: 1},
		},
	)
}

func dialWS(t *testing.T, serverURL string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(serverURL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("failed to dial websocket: %v", err)
	}
	return conn
}

func readJSON(t *testing.T, conn *websocket.Conn, timeout time.Duration) map[string]interface{} {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(timeout))
	var msg map[string]interface{}
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("failed to read JSON: %v", err)
	}
	return msg
}

func TestDashboardHandlerIntegration(t *testing.T) {
	fakeClient := seedFakeClientset()
	mockK8s := &k8s.MockClient{
		TypedClient:    fakeClient,
		ClusterName:    "test-cluster",
		ClusterVersion: "1.30",
	}

	server := ws.NewServer()
	dashHandler := handlers.NewDashboardHandler(mockK8s, server.Hub())
	server.RegisterHandler("dashboard", dashHandler)

	testServer := httptest.NewServer(server)
	defer testServer.Close()

	conn := dialWS(t, testServer.URL)
	defer conn.Close()

	startMsg := map[string]interface{}{
		"type": "dashboard",
		"payload": map[string]interface{}{
			"action":  "start",
			"cluster": "test-cluster",
		},
	}
	if err := conn.WriteJSON(startMsg); err != nil {
		t.Fatalf("failed to send start: %v", err)
	}

	resp := readJSON(t, conn, 5*time.Second)
	if resp["type"] != "dashboard" {
		t.Fatalf("expected type 'dashboard', got %v", resp["type"])
	}

	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatal("expected 'data' field in response")
	}

	podStatus, ok := data["podStatus"].(map[string]interface{})
	if !ok {
		t.Fatal("expected podStatus in data")
	}
	if podStatus["running"].(float64) != 2 {
		t.Errorf("expected 2 running pods, got %v", podStatus["running"])
	}
	if podStatus["pending"].(float64) != 1 {
		t.Errorf("expected 1 pending pod, got %v", podStatus["pending"])
	}

	nodeStatus, ok := data["nodeStatus"].(map[string]interface{})
	if !ok {
		t.Fatal("expected nodeStatus in data")
	}
	if nodeStatus["ready"].(float64) != 2 {
		t.Errorf("expected 2 ready nodes, got %v", nodeStatus["ready"])
	}

	workloadStatus, ok := data["workloadStatus"].(map[string]interface{})
	if !ok {
		t.Fatal("expected workloadStatus in data")
	}
	deployments := workloadStatus["deployments"].(map[string]interface{})
	if deployments["total"].(float64) != 1 {
		t.Errorf("expected 1 deployment, got %v", deployments["total"])
	}
	if deployments["healthy"].(float64) != 1 {
		t.Errorf("expected 1 healthy deployment, got %v", deployments["healthy"])
	}

	events, ok := data["events"].([]interface{})
	if !ok {
		t.Fatal("expected events in data")
	}
	if len(events) < 1 {
		t.Errorf("expected at least 1 event, got %d", len(events))
	}

	clusterInfo, ok := data["clusterInfo"].(map[string]interface{})
	if !ok {
		t.Fatal("expected clusterInfo in data")
	}
	if clusterInfo["version"] != "1.30" {
		t.Errorf("expected version '1.30', got %v", clusterInfo["version"])
	}

	conn.WriteJSON(map[string]interface{}{
		"type": "dashboard",
		"payload": map[string]interface{}{
			"action":  "stop",
			"cluster": "test-cluster",
		},
	})
	dashHandler.Shutdown()
}

func TestDashboardHandlerMultipleSubscribers(t *testing.T) {
	fakeClient := seedFakeClientset()
	mockK8s := &k8s.MockClient{
		TypedClient:    fakeClient,
		ClusterName:    "test-cluster",
		ClusterVersion: "1.30",
	}

	server := ws.NewServer()
	dashHandler := handlers.NewDashboardHandler(mockK8s, server.Hub())
	server.RegisterHandler("dashboard", dashHandler)

	testServer := httptest.NewServer(server)
	defer testServer.Close()

	conn1 := dialWS(t, testServer.URL)
	defer conn1.Close()
	conn2 := dialWS(t, testServer.URL)
	defer conn2.Close()

	startMsg := map[string]interface{}{
		"type": "dashboard",
		"payload": map[string]interface{}{
			"action":  "start",
			"cluster": "test-cluster",
		},
	}
	conn1.WriteJSON(startMsg)
	conn2.WriteJSON(startMsg)

	resp1 := readJSON(t, conn1, 5*time.Second)
	resp2 := readJSON(t, conn2, 5*time.Second)

	if resp1["type"] != "dashboard" {
		t.Errorf("conn1: expected type 'dashboard', got %v", resp1["type"])
	}
	if resp2["type"] != "dashboard" {
		t.Errorf("conn2: expected type 'dashboard', got %v", resp2["type"])
	}

	dashHandler.Shutdown()
}

func TestDashboardHandlerMissingCluster(t *testing.T) {
	server := ws.NewServer()
	mockK8s := &k8s.MockClient{}
	dashHandler := handlers.NewDashboardHandler(mockK8s, server.Hub())
	server.RegisterHandler("dashboard", dashHandler)

	testServer := httptest.NewServer(server)
	defer testServer.Close()

	conn := dialWS(t, testServer.URL)
	defer conn.Close()

	conn.WriteJSON(map[string]interface{}{
		"type": "dashboard",
		"payload": map[string]interface{}{
			"action": "start",
		},
	})

	// Handler returns an error for missing cluster, but the hub does not
	// send error messages back to clients — it only logs/metrics. Verify
	// that no dashboard data is received (read should time out).
	conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	var resp map[string]interface{}
	err := conn.ReadJSON(&resp)
	if err == nil {
		t.Errorf("expected no response for missing cluster, but got: %v", resp)
	}

	dashHandler.Shutdown()
}

func TestDashboardHandlerResourceCapacity(t *testing.T) {
	fakeClient := seedFakeClientset()
	mockK8s := &k8s.MockClient{
		TypedClient:    fakeClient,
		ClusterName:    "test-cluster",
		ClusterVersion: "1.30",
	}

	server := ws.NewServer()
	dashHandler := handlers.NewDashboardHandler(mockK8s, server.Hub())
	server.RegisterHandler("dashboard", dashHandler)

	testServer := httptest.NewServer(server)
	defer testServer.Close()

	conn := dialWS(t, testServer.URL)
	defer conn.Close()

	conn.WriteJSON(map[string]interface{}{
		"type": "dashboard",
		"payload": map[string]interface{}{
			"action":  "start",
			"cluster": "test-cluster",
		},
	})

	resp := readJSON(t, conn, 5*time.Second)
	data := resp["data"].(map[string]interface{})

	capacity, ok := data["resourceCapacity"].(map[string]interface{})
	if !ok {
		t.Fatal("expected resourceCapacity in data")
	}

	cpu := capacity["cpu"].(map[string]interface{})
	if cpu["allocatable"].(float64) <= 0 {
		t.Errorf("expected positive allocatable CPU, got %v", cpu["allocatable"])
	}
	if cpu["unit"] != "cores" {
		t.Errorf("expected CPU unit 'cores', got %v", cpu["unit"])
	}

	mem := capacity["memory"].(map[string]interface{})
	if mem["allocatable"].(float64) <= 0 {
		t.Errorf("expected positive allocatable memory, got %v", mem["allocatable"])
	}

	dashHandler.Shutdown()
}

func TestDashboardHandlerStopStartLifecycle(t *testing.T) {
	fakeClient := seedFakeClientset()
	mockK8s := &k8s.MockClient{
		TypedClient:    fakeClient,
		ClusterName:    "test-cluster",
		ClusterVersion: "1.30",
	}

	server := ws.NewServer()
	dashHandler := handlers.NewDashboardHandler(mockK8s, server.Hub())
	server.RegisterHandler("dashboard", dashHandler)

	testServer := httptest.NewServer(server)
	defer testServer.Close()

	conn := dialWS(t, testServer.URL)
	defer conn.Close()

	conn.WriteJSON(map[string]interface{}{
		"type":    "dashboard",
		"payload": map[string]interface{}{"action": "start", "cluster": "test-cluster"},
	})
	resp1 := readJSON(t, conn, 5*time.Second)
	if resp1["type"] != "dashboard" {
		t.Fatalf("expected dashboard, got %v", resp1["type"])
	}

	conn.WriteJSON(map[string]interface{}{
		"type":    "dashboard",
		"payload": map[string]interface{}{"action": "stop", "cluster": "test-cluster"},
	})
	time.Sleep(200 * time.Millisecond)

	conn.WriteJSON(map[string]interface{}{
		"type":    "dashboard",
		"payload": map[string]interface{}{"action": "start", "cluster": "test-cluster"},
	})
	resp2 := readJSON(t, conn, 5*time.Second)
	if resp2["type"] != "dashboard" {
		t.Fatalf("expected dashboard after restart, got %v", resp2["type"])
	}

	dashHandler.Shutdown()
}

func TestLogsHandlerStartStop(t *testing.T) {
	fakeClient := seedFakeClientset()
	mockK8s := &k8s.MockClient{
		TypedClient: fakeClient,
		ClusterName: "test-cluster",
	}

	server := ws.NewServer()
	logsHandler := handlers.NewLogsHandler(mockK8s)
	server.RegisterHandler("logs", logsHandler)

	testServer := httptest.NewServer(server)
	defer testServer.Close()

	conn := dialWS(t, testServer.URL)
	defer conn.Close()

	conn.WriteJSON(map[string]interface{}{
		"type": "logs",
		"payload": map[string]interface{}{
			"action":    "start",
			"key":       "test-log-stream",
			"cluster":   "test-cluster",
			"namespace": "default",
			"name":      "pod-1",
			"container": "app",
			"follow":    false,
			"tailLines": 10,
		},
	})

	resp := readJSON(t, conn, 5*time.Second)
	if resp["type"] != "logs" {
		t.Fatalf("expected type 'logs', got %v", resp["type"])
	}
	payload := resp["payload"].(map[string]interface{})
	if payload["type"] != "connected" {
		t.Errorf("expected payload type 'connected', got %v", payload["type"])
	}

	conn.WriteJSON(map[string]interface{}{
		"type": "logs",
		"payload": map[string]interface{}{
			"action": "stop",
			"key":    "test-log-stream",
		},
	})
	time.Sleep(100 * time.Millisecond)
}

func TestLogsHandlerDeploymentPodDiscovery(t *testing.T) {
	fakeClient := fake.NewSimpleClientset(
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "default"},
			Spec: appsv1.DeploymentSpec{
				Replicas: int32Ptr(1),
				Selector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"app": "web"},
				},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "web"}},
					Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "web", Image: "nginx"}}},
				},
			},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "web-abc-123",
				Namespace: "default",
				Labels:    map[string]string{"app": "web"},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "web", Image: "nginx"}},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
	)

	mockK8s := &k8s.MockClient{TypedClient: fakeClient, ClusterName: "test-cluster"}
	server := ws.NewServer()
	logsHandler := handlers.NewLogsHandler(mockK8s)
	server.RegisterHandler("logs", logsHandler)

	testServer := httptest.NewServer(server)
	defer testServer.Close()

	conn := dialWS(t, testServer.URL)
	defer conn.Close()

	conn.WriteJSON(map[string]interface{}{
		"type": "logs",
		"payload": map[string]interface{}{
			"action":       "start",
			"key":          "deploy-logs",
			"cluster":      "test-cluster",
			"namespace":    "default",
			"name":         "web",
			"resourceType": "deployment",
			"follow":       false,
		},
	})

	resp := readJSON(t, conn, 5*time.Second)
	payload := resp["payload"].(map[string]interface{})
	if payload["type"] != "connected" {
		t.Errorf("expected 'connected', got %v", payload["type"])
	}

	resp2 := readJSON(t, conn, 5*time.Second)
	payload2 := resp2["payload"].(map[string]interface{})
	if payload2["type"] != "pods" {
		t.Errorf("expected 'pods' message, got %v", payload2["type"])
	}
	pods := payload2["pods"].([]interface{})
	if len(pods) != 1 {
		t.Errorf("expected 1 pod, got %d", len(pods))
	}
}

func TestLogsHandlerInvalidCluster(t *testing.T) {
	mockK8s := &k8s.MockClient{}

	server := ws.NewServer()
	logsHandler := handlers.NewLogsHandler(mockK8s)
	server.RegisterHandler("logs", logsHandler)

	testServer := httptest.NewServer(server)
	defer testServer.Close()

	conn := dialWS(t, testServer.URL)
	defer conn.Close()

	conn.WriteJSON(map[string]interface{}{
		"type": "logs",
		"payload": map[string]interface{}{
			"action":    "start",
			"key":       "fail-stream",
			"cluster":   "nonexistent",
			"namespace": "default",
			"name":      "pod-1",
			"container": "app",
			"follow":    false,
		},
	})

	resp := readJSON(t, conn, 5*time.Second)
	payload := resp["payload"].(map[string]interface{})
	if payload["type"] != "connected" {
		t.Errorf("expected 'connected', got %v", payload["type"])
	}

	resp2 := readJSON(t, conn, 5*time.Second)
	payload2 := resp2["payload"].(map[string]interface{})
	if payload2["type"] != "error" {
		t.Errorf("expected 'error', got %v", payload2["type"])
	}
}

func TestConcurrentLogStreams(t *testing.T) {
	fakeClient := seedFakeClientset()
	mockK8s := &k8s.MockClient{TypedClient: fakeClient, ClusterName: "test-cluster"}

	server := ws.NewServer()
	logsHandler := handlers.NewLogsHandler(mockK8s)
	server.RegisterHandler("logs", logsHandler)

	testServer := httptest.NewServer(server)
	defer testServer.Close()

	numClients := 10
	conns := make([]*websocket.Conn, numClients)
	for i := 0; i < numClients; i++ {
		conns[i] = dialWS(t, testServer.URL)
		defer conns[i].Close()
	}

	for i, conn := range conns {
		conn.WriteJSON(map[string]interface{}{
			"type": "logs",
			"payload": map[string]interface{}{
				"action":    "start",
				"key":       "stream",
				"cluster":   "test-cluster",
				"namespace": "default",
				"name":      "pod-1",
				"container": "app",
				"follow":    false,
			},
		})
		_ = i
	}

	var connectedCount int32
	for i, conn := range conns {
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		var resp map[string]interface{}
		if err := conn.ReadJSON(&resp); err != nil {
			t.Errorf("client %d: failed to read: %v", i, err)
			continue
		}
		payload := resp["payload"].(map[string]interface{})
		if payload["type"] == "connected" {
			atomic.AddInt32(&connectedCount, 1)
		}
	}

	if atomic.LoadInt32(&connectedCount) != int32(numClients) {
		t.Errorf("expected %d connected responses, got %d", numClients, connectedCount)
	}
}
