// Package nats detects NATS deployments in a cluster (NATS Helm chart, NACK
// JetStream controllers, or the legacy nats-operator) and reads their live
// monitoring data through the Kubernetes API server's service proxy - the
// same connection model the Argo CD integration uses, so no port-forward and
// no NATS credentials are needed for read-only monitoring.
package nats

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/metadata"
)

const (
	// nackStreamCRD is present when the NACK JetStream controllers are installed.
	nackStreamCRD = "streams.jetstream.nats.io"
	// legacyOperatorCRD is present when the deprecated nats-io/nats-operator is installed.
	legacyOperatorCRD = "natsclusters.nats.io"

	defaultMonitorPortName = "monitor"
	defaultMonitorPort     = int32(8222)
)

// candidateNamespaces are checked, in order, for a Service named "nats"
// before falling back to a cluster-wide label search.
var candidateNamespaces = []string{"nats", "nats-io", "default"}

type Detection struct {
	Installed        bool   `json:"installed"`
	HasMonitor       bool   `json:"hasMonitor"`
	HasJetStreamCRDs bool   `json:"hasJetStreamCrds"`
	LegacyOperator   bool   `json:"legacyOperator"`
	Namespace        string `json:"namespace,omitempty"`
	ServiceName      string `json:"serviceName,omitempty"`
	MonitorPort      int32  `json:"monitorPort,omitempty"`
}

// Detect looks for a NATS deployment without requiring any prior
// configuration: a CRD probe for NACK's JetStream controllers, a CRD probe
// for the legacy nats-operator, and a Service search (well-known namespaces
// first, then the standard "app.kubernetes.io/name=nats" Helm chart label)
// for the monitoring port to talk to.
func Detect(ctx context.Context, kube kubernetes.Interface, meta metadata.Interface) (*Detection, error) {
	det := &Detection{}
	if meta == nil && kube == nil {
		return det, nil
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if meta != nil {
		crdGVR := schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}
		if _, err := meta.Resource(crdGVR).Get(timeoutCtx, nackStreamCRD, metav1.GetOptions{}); err == nil {
			det.HasJetStreamCRDs = true
			det.Installed = true
		}
		if _, err := meta.Resource(crdGVR).Get(timeoutCtx, legacyOperatorCRD, metav1.GetOptions{}); err == nil {
			det.LegacyOperator = true
			det.Installed = true
		}
	}

	if kube == nil {
		return det, nil
	}

	if svc := findMonitorService(timeoutCtx, kube); svc != nil {
		det.Installed = true
		det.HasMonitor = true
		det.Namespace = svc.namespace
		det.ServiceName = svc.name
		det.MonitorPort = svc.port
	}

	return det, nil
}

type monitorService struct {
	namespace string
	name      string
	port      int32
}

func findMonitorService(ctx context.Context, kube kubernetes.Interface) *monitorService {
	for _, ns := range candidateNamespaces {
		svc, err := kube.CoreV1().Services(ns).Get(ctx, "nats", metav1.GetOptions{})
		if err == nil && svc != nil {
			if ms := toMonitorService(svc); ms != nil {
				return ms
			}
		}
	}

	svcList, err := kube.CoreV1().Services(metav1.NamespaceAll).List(ctx, metav1.ListOptions{LabelSelector: "app.kubernetes.io/name=nats"})
	if err != nil || svcList == nil {
		return nil
	}
	for _, svc := range svcList.Items {
		if ms := toMonitorService(&svc); ms != nil {
			return ms
		}
	}
	return nil
}

func toMonitorService(svc *corev1.Service) *monitorService {
	port := monitorPort(svc)
	if port == 0 {
		return nil
	}
	return &monitorService{namespace: svc.Namespace, name: svc.Name, port: port}
}

func monitorPort(svc *corev1.Service) int32 {
	for _, p := range svc.Spec.Ports {
		if p.Name == defaultMonitorPortName {
			return p.Port
		}
	}
	for _, p := range svc.Spec.Ports {
		if p.Port == defaultMonitorPort {
			return p.Port
		}
	}
	return 0
}
