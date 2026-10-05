package k8s

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Sentinel errors for ResolveServicePod, checked with errors.Is by callers
// that map them to HTTP status codes.
var (
	ErrServiceNotFound         = errors.New("service not found")
	ErrServiceNoSelector       = errors.New("service has no selector")
	ErrServicePortNotFound     = errors.New("service port not found")
	ErrNoPodsMatchSelector     = errors.New("no pods matching service selector")
	ErrNoRunningPodsForService = errors.New("no running pods found for service")
	ErrNoReadyPodsForService   = errors.New("no ready pods found for service")
	ErrNamedPortNotFound       = errors.New("named port not found in pod containers")
)

// ResolveServicePod picks the best running, ready pod behind a Service and
// resolves the container port a forward to requestedPort should target.
// Used by both the user-facing service port-forward and, without any HTTP
// round trip, by backend code that needs a pod to dial directly (such as the
// NATS live-client pool).
func ResolveServicePod(ctx context.Context, kube kubernetes.Interface, namespace, serviceName string, requestedPort int32) (podName string, targetPort int, err error) {
	svc, err := kube.CoreV1().Services(namespace).Get(ctx, serviceName, metav1.GetOptions{})
	if err != nil {
		return "", 0, fmt.Errorf("%w: %s/%s: %v", ErrServiceNotFound, namespace, serviceName, err)
	}
	if len(svc.Spec.Selector) == 0 {
		return "", 0, fmt.Errorf("%w: %s/%s", ErrServiceNoSelector, namespace, serviceName)
	}

	var servicePort *v1.ServicePort
	for i := range svc.Spec.Ports {
		if svc.Spec.Ports[i].Port == requestedPort {
			servicePort = &svc.Spec.Ports[i]
			break
		}
	}
	if servicePort == nil {
		return "", 0, fmt.Errorf("%w: port %d not found on service %s/%s", ErrServicePortNotFound, requestedPort, namespace, serviceName)
	}

	selector := metav1.FormatLabelSelector(&metav1.LabelSelector{MatchLabels: svc.Spec.Selector})
	pods, err := kube.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return "", 0, fmt.Errorf("list pods for service %s/%s: %w", namespace, serviceName, err)
	}
	if len(pods.Items) == 0 {
		return "", 0, fmt.Errorf("%w: %s", ErrNoPodsMatchSelector, selector)
	}

	selectedPod, err := selectServicePod(pods.Items)
	if err != nil {
		return "", 0, err
	}

	targetPort, err = resolveTargetPort(servicePort, selectedPod)
	if err != nil {
		return "", 0, err
	}
	return selectedPod.Name, targetPort, nil
}

type podCandidate struct {
	pod   *v1.Pod
	ready bool
	score int
}

func selectServicePod(pods []v1.Pod) (*v1.Pod, error) {
	candidates := make([]podCandidate, 0, len(pods))
	runningCount := 0
	readyCount := 0

	for i := range pods {
		pod := &pods[i]
		if pod.Status.Phase != v1.PodRunning {
			continue
		}
		runningCount++
		ready := false
		for _, cond := range pod.Status.Conditions {
			if cond.Type == v1.PodReady && cond.Status == v1.ConditionTrue {
				ready = true
				break
			}
		}
		if ready {
			readyCount++
		}
		score := 0
		if ready {
			score += 100
		}
		if pod.DeletionTimestamp == nil {
			score += 10
		}
		candidates = append(candidates, podCandidate{pod: pod, ready: ready, score: score})
	}

	if len(candidates) == 0 {
		return nil, fmt.Errorf("%w (found %d total pods, none in Running phase)", ErrNoRunningPodsForService, len(pods))
	}
	if readyCount == 0 {
		return nil, fmt.Errorf("%w (found %d running pods, none ready)", ErrNoReadyPodsForService, runningCount)
	}

	slices.SortFunc(candidates, func(a, b podCandidate) int {
		if a.score != b.score {
			return cmp.Compare(b.score, a.score)
		}
		return cmp.Compare(a.pod.Name, b.pod.Name)
	})
	return candidates[0].pod, nil
}

func resolveTargetPort(servicePort *v1.ServicePort, pod *v1.Pod) (int, error) {
	if servicePort.TargetPort.IntVal != 0 {
		return int(servicePort.TargetPort.IntVal), nil
	}
	if servicePort.TargetPort.StrVal != "" {
		portName := servicePort.TargetPort.StrVal
		for _, container := range pod.Spec.Containers {
			for _, port := range container.Ports {
				if port.Name == portName {
					return int(port.ContainerPort), nil
				}
			}
		}
		return 0, fmt.Errorf("%w: %s", ErrNamedPortNotFound, portName)
	}
	return int(servicePort.Port), nil
}
