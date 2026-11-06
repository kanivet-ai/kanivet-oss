package clusterinfo

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func node(name, providerID string, labels map[string]string) corev1.Node {
	return corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels}, Spec: corev1.NodeSpec{ProviderID: providerID}}
}

func TestDetect(t *testing.T) {
	cases := []struct {
		name      string
		s         Signals
		want      string
		managed   bool
		cpVisible bool
		minConf   float64
		dist      string
	}{
		{"eks", Signals{
			Nodes:      []corev1.Node{node("ip-10-0-0-1.ec2.internal", "aws:///us-east-1a/i-0abc", map[string]string{"eks.amazonaws.com/nodegroup": "ng-1"})},
			GitVersion: "v1.31.4-eks-2d5f260", APIHost: "https://ABC.gr7.us-east-1.eks.amazonaws.com",
			KubeSystemPods: []string{"aws-node-x", "coredns-1"},
		}, EKS, true, false, 0.9, "EKS"},
		{"eks karpenter only", Signals{
			Nodes:      []corev1.Node{node("ip-1", "aws:///us-east-1a/i-1", map[string]string{"karpenter.k8s.aws/instance-family": "m5"})},
			GitVersion: "v1.30.0-eks-abc",
		}, EKS, true, false, 0.5, "EKS"},
		{"eks fargate", Signals{
			Nodes:      []corev1.Node{node("fargate-ip-1", "aws:///us-east-1a/x", map[string]string{"eks.amazonaws.com/compute-type": "fargate"})},
			GitVersion: "v1.31.4-eks-2d5f260",
		}, EKS, true, false, 0.5, "EKS (Fargate)"},
		{"gke", Signals{
			Nodes:      []corev1.Node{node("gke-a-default-1", "gce://proj/us-central1-a/gke-a-default-1", map[string]string{"cloud.google.com/gke-nodepool": "default-pool"})},
			GitVersion: "v1.30.5-gke.1014001", APIHost: "https://34.1.2.3",
		}, GKE, true, false, 0.9, "GKE"},
		{"gke autopilot", Signals{
			Nodes:      []corev1.Node{node("gk3-a", "gce://p/z/gk3-a", map[string]string{"cloud.google.com/gke-nodepool": "pool-1", "cloud.google.com/gke-provisioning": "autopilot"})},
			GitVersion: "v1.30.5-gke.1014001",
		}, GKE, true, false, 0.9, "GKE (Autopilot)"},
		{"aks", Signals{
			Nodes:   []corev1.Node{node("aks-nodepool1-1-vmss000000", "azure:///subscriptions/x/resourceGroups/y", map[string]string{"kubernetes.azure.com/cluster": "MC_rg_c_westeurope"})},
			APIHost: "https://c-dns-1234.hcp.westeurope.azmk8s.io:443", GitVersion: "v1.30.3",
		}, AKS, true, false, 0.9, "AKS"},
		{"openshift", Signals{
			Nodes:     []corev1.Node{node("master-0", "aws:///us-east-1a/i-9", map[string]string{"node.openshift.io/os_id": "rhcos"})},
			APIGroups: []string{"apps", "config.openshift.io", "route.openshift.io"}, GitVersion: "v1.29.8+f10c92d",
		}, OpenShift, false, false, 0.6, "OpenShift"},
		{"k3s", Signals{
			Nodes:      []corev1.Node{node("pi-1", "k3s://pi-1", map[string]string{"node.kubernetes.io/instance-type": "k3s"})},
			GitVersion: "v1.30.4+k3s1",
		}, K3s, false, false, 0.9, "k3s"},
		{"rke2", Signals{
			Nodes:      []corev1.Node{node("rke-1", "", map[string]string{"node.kubernetes.io/instance-type": "rke2"})},
			GitVersion: "v1.30.4+rke2r1",
		}, RKE2, false, false, 0.9, "RKE2"},
		{"kind", Signals{
			Nodes:      []corev1.Node{node("demo-control-plane", "kind://docker/demo/demo-control-plane", nil)},
			GitVersion: "v1.31.6", APIHost: "https://127.0.0.1:52345",
			KubeSystemPods: []string{"kube-apiserver-demo-control-plane", "etcd-demo-control-plane"},
		}, Kind, false, true, 0.8, "kind"},
		{"minikube", Signals{
			Nodes:          []corev1.Node{node("minikube", "", map[string]string{"minikube.k8s.io/name": "minikube"})},
			GitVersion:     "v1.31.0",
			KubeSystemPods: []string{"kube-apiserver-minikube"},
		}, Minikube, false, true, 0.7, "minikube"},
		{"docker desktop", Signals{
			Nodes:      []corev1.Node{node("docker-desktop", "", nil)},
			GitVersion: "v1.30.2", APIHost: "https://kubernetes.docker.internal:6443",
			KubeSystemPods: []string{"kube-apiserver-docker-desktop"},
		}, DockerDesktop, false, true, 0.8, "Docker Desktop"},
		{"kubeadm", Signals{
			Nodes:          []corev1.Node{node("cp-1", "", map[string]string{"node-role.kubernetes.io/control-plane": ""})},
			GitVersion:     "v1.31.1",
			KubeSystemPods: []string{"kube-apiserver-cp-1", "etcd-cp-1"},
		}, Kubeadm, false, true, 0.2, "kubeadm"},
		{"nothing", Signals{}, Unknown, false, false, 0, "Unknown"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Detect(c.s)
			if got.Provider != c.want {
				t.Fatalf("provider %q, want %q (evidence %+v)", got.Provider, c.want, got.Evidence)
			}
			if got.ManagedControlPlane != c.managed || got.ControlPlaneVisible != c.cpVisible {
				t.Errorf("managed=%v visible=%v", got.ManagedControlPlane, got.ControlPlaneVisible)
			}
			if got.Confidence < c.minConf || got.Confidence > 1 {
				t.Errorf("confidence %v below %v", got.Confidence, c.minConf)
			}
			if got.Distribution != c.dist {
				t.Errorf("distribution %q, want %q", got.Distribution, c.dist)
			}
		})
	}
}

func TestVersionAndOverride(t *testing.T) {
	info := Detect(Signals{GitVersion: "v1.36.0-eks-123", Nodes: []corev1.Node{node("n", "aws:///x", nil)}})
	if info.Version != "1.36" {
		t.Fatalf("version %q", info.Version)
	}
	o := ApplyOverride(info, Kubeadm)
	if o.Provider != Kubeadm || o.ManagedControlPlane || !o.Overridden || len(o.Evidence) == 0 {
		t.Fatalf("override %+v", o)
	}
}
