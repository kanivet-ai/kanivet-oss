package network

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	authzv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
)

// DefaultProbeImage runs the probe; it only needs a shell and nc.
const DefaultProbeImage = "busybox:1.36.1"

// ProbeLabel marks probe pods, so leftovers can be found and removed.
const ProbeLabel = "kanivet.io/network-probe"

// ErrNotConfirmed is returned when a probe is asked for without the explicit
// confirmation the UI collects.
var ErrNotConfirmed = errors.New("the live probe creates a pod: confirm it explicitly")

// ProbeRequest asks for a live connection attempt from the source's
// namespace to the destination. It creates a pod, so Confirm must be true.
type ProbeRequest struct {
	Source      Ref   `json:"source"`
	Destination Ref   `json:"destination"`
	Port        int32 `json:"port"`
	// MimicLabels gives the probe pod the source pod's labels, so policies
	// that select the source by label also select the probe. The probe is
	// then owned by the source pod (so no controller adopts it) and never
	// becomes ready (so no Service sends it traffic).
	MimicLabels bool   `json:"mimicLabels"`
	Image       string `json:"image,omitempty"`
	Confirm     bool   `json:"confirm"`
}

// ProbeResult is what the probe saw.
type ProbeResult struct {
	// Status is reached | failed | timeout | forbidden | error.
	Status     string `json:"status"`
	Reached    bool   `json:"reached"`
	Detail     string `json:"detail"`
	Output     string `json:"output,omitempty"`
	Namespace  string `json:"namespace"`
	PodName    string `json:"podName,omitempty"`
	Target     string `json:"target"`
	DurationMs int64  `json:"durationMs"`
	Cleaned    bool   `json:"cleaned"`
	// Command is the kubectl equivalent, for running it by hand.
	Command string `json:"command"`
	Labels  bool   `json:"mimicLabels"`
}

// probeTarget works out where the probe connects: a Service by DNS name, a
// pod or workload by its first ready pod's IP, an address as given.
func (s *Snapshot) probeTarget(req ProbeRequest) (src *corev1.Pod, host string, port int32, err error) {
	req.Source.Kind = normalizeKind(req.Source.Kind)
	pods := runningFirst(s.podsOf(req.Source))
	if len(pods) == 0 {
		return nil, "", 0, fmt.Errorf("no running pods for the source %s", describeRef(req.Source))
	}
	src = pods[0]
	port = req.Port
	switch normalizeKind(req.Destination.Kind) {
	case "IP":
		host = req.Destination.IP
	case "Service":
		svc := s.service(req.Destination.Namespace, req.Destination.Name)
		if svc == nil {
			return nil, "", 0, fmt.Errorf("service %s/%s not found", req.Destination.Namespace, req.Destination.Name)
		}
		if port == 0 && len(svc.Spec.Ports) == 1 {
			port = svc.Spec.Ports[0].Port
		}
		host = svc.Name + "." + svc.Namespace + ".svc"
	default:
		d := req.Destination
		d.Kind = normalizeKind(d.Kind)
		dp := runningFirst(s.podsOf(d))
		if len(dp) == 0 || dp[0].Status.PodIP == "" {
			return nil, "", 0, fmt.Errorf("no running pod with an IP for %s", describeRef(d))
		}
		host = dp[0].Status.PodIP
		if port == 0 {
			if ports := declaredPorts(dp[0], corev1.ProtocolTCP); len(ports) == 1 {
				port = ports[0]
			}
		}
	}
	if host == "" || port <= 0 || port > 65535 {
		return nil, "", 0, errors.New("a destination and a TCP port are required")
	}
	return src, host, port, nil
}

// probeLabels are the source pod's labels without the ones controllers use
// to recognise their own pods.
func probeLabels(src *corev1.Pod, id string, mimic bool) map[string]string {
	l := map[string]string{ProbeLabel: id, "app.kubernetes.io/managed-by": "kanivet"}
	if !mimic {
		return l
	}
	for k, v := range src.Labels {
		switch k {
		case "pod-template-hash", "controller-revision-hash", "statefulset.kubernetes.io/pod-name", "pod-template-generation",
			"batch.kubernetes.io/controller-uid", "controller-uid", "batch.kubernetes.io/job-name", "job-name":
			continue
		}
		l[k] = v
	}
	l[ProbeLabel] = id
	l["app.kubernetes.io/managed-by"] = "kanivet"
	return l
}

func probePod(src *corev1.Pod, id, image, host string, port int32, mimic bool) *corev1.Pod {
	script := fmt.Sprintf("if nc -z -w 3 %s %d; then echo KANIVET_PROBE_OPEN; else echo KANIVET_PROBE_CLOSED; exit 1; fi", host, port)
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "kanivet-probe-" + id,
			Namespace:   src.Namespace,
			Labels:      probeLabels(src, id, mimic),
			Annotations: map[string]string{"kanivet.io/probe-target": fmt.Sprintf("%s:%d", host, port)},
		},
		Spec: corev1.PodSpec{
			RestartPolicy:                 corev1.RestartPolicyNever,
			ActiveDeadlineSeconds:         ptr.To[int64](60),
			TerminationGracePeriodSeconds: ptr.To[int64](0),
			AutomountServiceAccountToken:  ptr.To(false),
			EnableServiceLinks:            ptr.To(false),
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot:   ptr.To(true),
				RunAsUser:      ptr.To[int64](65534),
				SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
			},
			Containers: []corev1.Container{{
				Name:    "probe",
				Image:   image,
				Command: []string{"sh", "-c", script},
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("10m"), corev1.ResourceMemory: resource.MustParse("16Mi")},
					Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m"), corev1.ResourceMemory: resource.MustParse("32Mi")},
				},
				SecurityContext: &corev1.SecurityContext{
					AllowPrivilegeEscalation: ptr.To(false),
					ReadOnlyRootFilesystem:   ptr.To(true),
					Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
				},
				// Never ready: a Service whose selector the mimicked labels
				// match must not route to the probe.
				ReadinessProbe: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{Exec: &corev1.ExecAction{Command: []string{"false"}}}, PeriodSeconds: 30},
			}},
		},
	}
	if mimic {
		// A controller-owned pod is never adopted by a ReplicaSet or Job
		// whose selector it matches, and goes away with the source pod.
		p.OwnerReferences = []metav1.OwnerReference{{APIVersion: "v1", Kind: "Pod", Name: src.Name, UID: src.UID, Controller: ptr.To(true), BlockOwnerDeletion: ptr.To(false)}}
	}
	return p
}

func probeCommand(ns, image, host string, port int32) string {
	return fmt.Sprintf("kubectl run kanivet-probe --rm -i --restart=Never -n %s --image=%s -- nc -z -w 3 %s %d", ns, image, host, port)
}

// runProbe creates the probe pod, waits for it, reads its verdict and
// deletes it whatever happens.
func runProbe(ctx context.Context, cs kubernetes.Interface, snap *Snapshot, req ProbeRequest) (*ProbeResult, error) {
	if !req.Confirm {
		return nil, ErrNotConfirmed
	}
	src, host, port, err := snap.probeTarget(req)
	if err != nil {
		return nil, err
	}
	image := strings.TrimSpace(req.Image)
	if image == "" {
		image = DefaultProbeImage
	}
	res := &ProbeResult{Namespace: src.Namespace, Target: fmt.Sprintf("%s:%d", host, port), Command: probeCommand(src.Namespace, image, host, port), Labels: req.MimicLabels}

	ssar := &authzv1.SelfSubjectAccessReview{Spec: authzv1.SelfSubjectAccessReviewSpec{ResourceAttributes: &authzv1.ResourceAttributes{Namespace: src.Namespace, Verb: "create", Resource: "pods"}}}
	if r, err := cs.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, ssar, metav1.CreateOptions{}); err == nil && !r.Status.Allowed {
		res.Status = "forbidden"
		res.Detail = "You may not create pods in " + src.Namespace + "; run the command below with an account that can."
		return res, nil
	}

	id := randomID()
	pod := probePod(src, id, image, host, port, req.MimicLabels)
	start := time.Now()
	created, err := cs.CoreV1().Pods(src.Namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		res.Status = "error"
		res.Detail = "Creating the probe pod failed: " + err.Error()
		return res, nil
	}
	res.PodName = created.Name
	defer func() {
		dctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err := cs.CoreV1().Pods(src.Namespace).Delete(dctx, created.Name, metav1.DeleteOptions{GracePeriodSeconds: ptr.To[int64](0)})
		res.Cleaned = err == nil
	}()

	wctx, cancel := context.WithTimeout(ctx, 75*time.Second)
	defer cancel()
	var phase corev1.PodPhase
	var waitReason string
	for {
		p, err := cs.CoreV1().Pods(src.Namespace).Get(wctx, created.Name, metav1.GetOptions{})
		if err == nil {
			phase = p.Status.Phase
			for _, st := range p.Status.ContainerStatuses {
				if st.State.Waiting != nil {
					waitReason = st.State.Waiting.Reason
				}
			}
			if phase == corev1.PodSucceeded || phase == corev1.PodFailed {
				break
			}
			if waitReason == "ErrImagePull" || waitReason == "ImagePullBackOff" || waitReason == "InvalidImageName" {
				break
			}
		}
		select {
		case <-wctx.Done():
			res.DurationMs = time.Since(start).Milliseconds()
			res.Status = "timeout"
			res.Detail = "The probe pod did not finish in time"
			if waitReason != "" {
				res.Detail += " (" + waitReason + ")"
			}
			res.Detail += "."
			return res, nil
		case <-time.After(time.Second):
		}
	}
	res.DurationMs = time.Since(start).Milliseconds()
	if waitReason == "ErrImagePull" || waitReason == "ImagePullBackOff" || waitReason == "InvalidImageName" {
		res.Status = "error"
		res.Detail = "The probe image " + image + " could not be pulled (" + waitReason + "). Set a mirror image the cluster can pull."
		return res, nil
	}
	res.Output = readLogs(wctx, cs, src.Namespace, created.Name)
	switch {
	case strings.Contains(res.Output, "KANIVET_PROBE_OPEN"):
		res.Status, res.Reached = "reached", true
		res.Detail = fmt.Sprintf("A TCP connection to %s succeeded from %s.", res.Target, src.Namespace)
	case strings.Contains(res.Output, "KANIVET_PROBE_CLOSED"):
		res.Status = "failed"
		res.Detail = fmt.Sprintf("A TCP connection to %s failed or timed out after 3s: blocked by policy, nothing listening, or unresolvable.", res.Target)
	default:
		res.Status = "error"
		res.Detail = "The probe ended (" + string(phase) + ") without a verdict."
	}
	return res, nil
}

func readLogs(ctx context.Context, cs kubernetes.Interface, ns, name string) string {
	rc, err := cs.CoreV1().Pods(ns).GetLogs(name, &corev1.PodLogOptions{Container: "probe", LimitBytes: ptr.To[int64](4096)}).Stream(ctx)
	if err != nil {
		return ""
	}
	defer rc.Close()
	b, _ := io.ReadAll(io.LimitReader(rc, 4096))
	return strings.TrimSpace(string(b))
}

func randomID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
