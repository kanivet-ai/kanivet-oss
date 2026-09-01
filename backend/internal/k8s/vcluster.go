package k8s

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func waitForVClusterReady(cfg *rest.Config, total time.Duration) error {
	probeCfg := *cfg
	probeCfg.Timeout = 2 * time.Second
	deadline := time.Now().Add(total)
	delay := 200 * time.Millisecond
	var lastErr error
	for time.Now().Before(deadline) {
		dc, err := discovery.NewDiscoveryClientForConfig(&probeCfg)
		if err != nil {
			return fmt.Errorf("discovery client: %w", err)
		}
		if _, err := dc.ServerVersion(); err == nil {
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(delay)
		if delay < 1*time.Second {
			delay *= 2
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("timeout")
	}
	return fmt.Errorf("vcluster apiserver probe failed: %w", lastErr)
}

const VClusterIDPrefix = "vcluster:"

type VClusterInfo struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Phase     string `json:"phase"`
	Ready     bool   `json:"ready"`
}

type VClusterConnection struct {
	ID            string       `json:"id"`
	Host          string       `json:"host"`
	Namespace     string       `json:"namespace"`
	Name          string       `json:"name"`
	LocalPort     int          `json:"localPort"`
	PortForwardID string       `json:"-"`
	Config        *rest.Config `json:"-"`
}

func vclusterID(host, namespace, name string) string {
	return fmt.Sprintf("%s%s:%s:%s", VClusterIDPrefix, host, namespace, name)
}

func IsVClusterID(name string) bool {
	return strings.HasPrefix(name, VClusterIDPrefix)
}

func parseVClusterID(id string) (host, namespace, name string, ok bool) {
	if !IsVClusterID(id) {
		return "", "", "", false
	}
	rest := strings.TrimPrefix(id, VClusterIDPrefix)
	iName := strings.LastIndex(rest, ":")
	if iName < 0 {
		return "", "", "", false
	}
	iNs := strings.LastIndex(rest[:iName], ":")
	if iNs < 0 {
		return "", "", "", false
	}
	return rest[:iNs], rest[iNs+1 : iName], rest[iName+1:], true
}

func (c *Client) ListVClusters(host string) ([]VClusterInfo, error) {
	client, err := c.GetClientForCluster(host)
	if err != nil {
		return nil, fmt.Errorf("get client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	results := make(map[string]*VClusterInfo)

	stsList, stsErr := client.AppsV1().StatefulSets("").List(ctx, metav1.ListOptions{LabelSelector: "app=vcluster"})
	if stsErr != nil {
		log.Printf("[VCLUSTER] StatefulSet listing failed for %s: %v", host, stsErr)
	} else {
		for i := range stsList.Items {
			s := &stsList.Items[i]
			results[s.Namespace+"/"+s.Name] = vclusterInfoFromSTS(s)
		}
	}

	deployList, deployErr := client.AppsV1().Deployments("").List(ctx, metav1.ListOptions{LabelSelector: "app=vcluster"})
	if deployErr != nil {
		log.Printf("[VCLUSTER] Deployment listing failed for %s: %v", host, deployErr)
	} else {
		for i := range deployList.Items {
			d := &deployList.Items[i]
			key := d.Namespace + "/" + d.Name
			if _, exists := results[key]; exists {
				continue
			}
			results[key] = vclusterInfoFromDeployment(d)
		}
	}

	if stsErr != nil && deployErr != nil {
		return nil, fmt.Errorf("list vclusters: sts=%v deploy=%v", stsErr, deployErr)
	}

	out := make([]VClusterInfo, 0, len(results))
	for _, vc := range results {
		out = append(out, *vc)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func vclusterInfoFromSTS(s *appsv1.StatefulSet) *VClusterInfo {
	ready := s.Status.ReadyReplicas > 0 && s.Status.ReadyReplicas == s.Status.Replicas
	phase := "Pending"
	if ready {
		phase = "Ready"
	} else if s.Status.ReadyReplicas > 0 {
		phase = "Progressing"
	}
	return &VClusterInfo{Name: s.Name, Namespace: s.Namespace, Phase: phase, Ready: ready}
}

func vclusterInfoFromDeployment(d *appsv1.Deployment) *VClusterInfo {
	ready := d.Status.ReadyReplicas > 0 && d.Status.ReadyReplicas == d.Status.Replicas
	phase := "Pending"
	if ready {
		phase = "Ready"
	} else if d.Status.ReadyReplicas > 0 {
		phase = "Progressing"
	}
	return &VClusterInfo{Name: d.Name, Namespace: d.Namespace, Phase: phase, Ready: ready}
}

func (c *Client) ConnectVCluster(host, namespace, name string) (*VClusterConnection, error) {
	id := vclusterID(host, namespace, name)

	sup := c.getOrCreateSupervisor(id, host, namespace, name)
	cfg, err := sup.WaitHealthy(context.Background(), 25*time.Second)
	if err != nil {
		return nil, fmt.Errorf("vcluster_not_ready: %w", err)
	}

	conn := &VClusterConnection{
		ID:        id,
		Host:      host,
		Namespace: namespace,
		Name:      name,
		LocalPort: sup.currentPort,
		Config:    cfg,
	}
	c.mu.Lock()
	c.vclusterCache[id] = conn
	c.mu.Unlock()
	return conn, nil
}

func (c *Client) getOrCreateSupervisor(id, host, namespace, name string) *VClusterSupervisor {
	c.mu.Lock()
	if existing, ok := c.VClusterSupervisors[id]; ok {
		c.mu.Unlock()
		return existing
	}
	sup := NewVClusterSupervisor(c, id, host, namespace, name)
	c.VClusterSupervisors[id] = sup
	c.mu.Unlock()
	c.attachStatusBroadcast(sup)
	sup.start()
	return sup
}

func (c *Client) GetVClusterSupervisor(id string) (*VClusterSupervisor, bool) {
	c.mu.RLock()
	sup, ok := c.VClusterSupervisors[id]
	c.mu.RUnlock()
	return sup, ok
}

func (c *Client) attachStatusBroadcast(sup *VClusterSupervisor) {
	_, ch := sup.Subscribe()
	go func() {
		for status := range ch {
			c.broadcastVClusterStatus(status)
		}
	}()
}

func (c *Client) SubscribeVClusterStatus() (uint64, <-chan VClusterStatus) {
	c.vclusterStatusMu.Lock()
	id := c.vclusterStatusSubID + 1
	c.vclusterStatusSubID = id
	ch := make(chan VClusterStatus, 32)
	c.vclusterStatusSubs[id] = ch
	c.vclusterStatusMu.Unlock()
	return id, ch
}

func (c *Client) UnsubscribeVClusterStatus(id uint64) {
	c.vclusterStatusMu.Lock()
	if ch, ok := c.vclusterStatusSubs[id]; ok {
		delete(c.vclusterStatusSubs, id)
		close(ch)
	}
	c.vclusterStatusMu.Unlock()
}

func (c *Client) broadcastVClusterStatus(status VClusterStatus) {
	c.vclusterStatusMu.RLock()
	subs := make([]chan VClusterStatus, 0, len(c.vclusterStatusSubs))
	for _, ch := range c.vclusterStatusSubs {
		subs = append(subs, ch)
	}
	c.vclusterStatusMu.RUnlock()
	for _, ch := range subs {
		select {
		case ch <- status:
		default:
		}
	}
}

func resolveVClusterPod(ctx context.Context, client kubernetes.Interface, namespace, name string) (string, int, error) {
	svc, err := client.CoreV1().Services(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", 0, fmt.Errorf("service %s/%s: %w", namespace, name, err)
	}
	if len(svc.Spec.Selector) == 0 {
		return "", 0, fmt.Errorf("service %s has no selector", name)
	}
	var port *corev1.ServicePort
	for i := range svc.Spec.Ports {
		p := &svc.Spec.Ports[i]
		if p.Name == "https" || p.Port == 443 {
			port = p
			break
		}
	}
	if port == nil && len(svc.Spec.Ports) > 0 {
		port = &svc.Spec.Ports[0]
	}
	if port == nil {
		return "", 0, fmt.Errorf("service %s has no ports", name)
	}

	selector := metav1.FormatLabelSelector(&metav1.LabelSelector{MatchLabels: svc.Spec.Selector})
	pods, err := client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return "", 0, fmt.Errorf("list pods: %w", err)
	}

	var chosen *corev1.Pod
	for i := range pods.Items {
		p := &pods.Items[i]
		if p.Status.Phase != corev1.PodRunning {
			continue
		}
		ready := false
		for _, cond := range p.Status.Conditions {
			if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
				ready = true
				break
			}
		}
		if ready {
			chosen = p
			break
		}
	}
	if chosen == nil {
		return "", 0, fmt.Errorf("no ready pods for vcluster %s", name)
	}

	target := 0
	if port.TargetPort.IntVal != 0 {
		target = int(port.TargetPort.IntVal)
	} else if port.TargetPort.StrVal != "" {
		named := port.TargetPort.StrVal
		for _, ctr := range chosen.Spec.Containers {
			for _, cp := range ctr.Ports {
				if cp.Name == named {
					target = int(cp.ContainerPort)
					break
				}
			}
			if target != 0 {
				break
			}
		}
	}
	if target == 0 {
		target = int(port.Port)
	}
	return chosen.Name, target, nil
}

func (c *Client) DisconnectVCluster(id string) error {
	c.mu.Lock()
	sup := c.VClusterSupervisors[id]
	delete(c.VClusterSupervisors, id)
	delete(c.vclusterCache, id)
	delete(c.configs, id)
	delete(c.clients, id)
	delete(c.dynamic, id)
	delete(c.interactive, id)
	delete(c.metadata, id)
	delete(c.bulkMetadata, id)
	delete(c.discovery, id)
	c.mu.Unlock()

	if sup != nil {
		sup.stop()
	}
	c.resourceNameMu.Lock()
	prefix := id + ":"
	for k := range c.resourceNameCache {
		if strings.HasPrefix(k, prefix) {
			delete(c.resourceNameCache, k)
		}
	}
	c.resourceNameMu.Unlock()
	log.Printf("[VCLUSTER] Disconnected %s", id)
	return nil
}

func (c *Client) ShutdownVClusters() {
	c.mu.Lock()
	ids := make([]string, 0, len(c.VClusterSupervisors))
	for id := range c.VClusterSupervisors {
		ids = append(ids, id)
	}
	c.mu.Unlock()
	for _, id := range ids {
		_ = c.DisconnectVCluster(id)
	}
}

func (c *Client) EvictVClusterCaches(id string) {
	c.evictVClusterCaches(id)
}

func (c *Client) evictVClusterCaches(id string) {
	c.mu.Lock()
	delete(c.configs, id)
	delete(c.clients, id)
	delete(c.dynamic, id)
	delete(c.interactive, id)
	delete(c.metadata, id)
	delete(c.bulkMetadata, id)
	delete(c.discovery, id)
	c.mu.Unlock()
}

func (c *Client) ensureVClusterAlive(id string) (*rest.Config, error) {
	c.mu.RLock()
	sup := c.VClusterSupervisors[id]
	c.mu.RUnlock()

	if sup == nil {
		host, namespace, name, ok := parseVClusterID(id)
		if !ok {
			return nil, fmt.Errorf("invalid vcluster id %s", id)
		}
		sup = c.getOrCreateSupervisor(id, host, namespace, name)
	}

	if cfg := sup.currentRestConfig(); cfg != nil && sup.Status().State == VClusterStateHealthy {
		return cfg, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cfg, err := sup.WaitHealthy(ctx, 25*time.Second)
	if err != nil {
		return nil, fmt.Errorf("vcluster_not_ready: %w", err)
	}
	return cfg, nil
}
