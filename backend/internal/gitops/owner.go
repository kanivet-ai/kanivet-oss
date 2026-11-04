package gitops

import "strings"

// Owner is the GitOps object that manages a resource.
type Owner struct {
	// Tool is "flux" or "argocd".
	Tool string    `json:"tool"`
	Ref  ObjectRef `json:"ref"`
	// Via names the label or annotation the owner was read from.
	Via string `json:"via"`
}

const (
	labelKustomizeName      = "kustomize.toolkit.fluxcd.io/name"
	labelKustomizeNamespace = "kustomize.toolkit.fluxcd.io/namespace"
	labelHelmName           = "helm.toolkit.fluxcd.io/name"
	labelHelmNamespace      = "helm.toolkit.fluxcd.io/namespace"
	annotArgoTrackingID     = "argocd.argoproj.io/tracking-id"
	labelArgoInstance       = "app.kubernetes.io/instance"
	annotArgoInstance       = "argocd.argoproj.io/instance"
)

// OwnerOf reads the GitOps owner from a resource's labels and annotations.
// argoNamespace is where Argo CD keeps Applications (usually "argocd"); it is
// used when the tracking value carries no namespace.
//
// Order: Flux labels are authoritative (the controllers stamp them on every
// applied object). Argo's tracking-id annotation comes next. The
// app.kubernetes.io/instance label is only trusted when argoTrustInstance is
// set, because Helm charts set it too and it would mislabel plain Helm
// releases as Argo apps.
func OwnerOf(labels, annotations map[string]string, argoNamespace string, argoTrustInstance bool) *Owner {
	if n := labels[labelHelmName]; n != "" {
		return &Owner{Tool: "flux", Via: labelHelmName, Ref: ObjectRef{APIVersion: GroupHelm + "/v2", Kind: "HelmRelease", Name: n, Namespace: labels[labelHelmNamespace]}}
	}
	if n := labels[labelKustomizeName]; n != "" {
		return &Owner{Tool: "flux", Via: labelKustomizeName, Ref: ObjectRef{APIVersion: GroupKustomize + "/v1", Kind: "Kustomization", Name: n, Namespace: labels[labelKustomizeNamespace]}}
	}
	if argoNamespace == "" {
		argoNamespace = "argocd"
	}
	if id := annotations[annotArgoTrackingID]; id != "" {
		app := id
		if i := strings.Index(id, ":"); i >= 0 {
			app = id[:i]
		}
		if ref, ok := argoAppRef(app, argoNamespace); ok {
			return &Owner{Tool: "argocd", Via: annotArgoTrackingID, Ref: ref}
		}
	}
	if v := annotations[annotArgoInstance]; v != "" {
		if ref, ok := argoAppRef(v, argoNamespace); ok {
			return &Owner{Tool: "argocd", Via: annotArgoInstance, Ref: ref}
		}
	}
	if argoTrustInstance {
		if v := labels[labelArgoInstance]; v != "" {
			if ref, ok := argoAppRef(v, argoNamespace); ok {
				return &Owner{Tool: "argocd", Via: labelArgoInstance, Ref: ref}
			}
		}
	}
	return nil
}

// argoAppRef parses "<app>" or "<ns>_<app>" (apps in any namespace).
func argoAppRef(v, defaultNS string) (ObjectRef, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return ObjectRef{}, false
	}
	ns, name := defaultNS, v
	if i := strings.Index(v, "_"); i > 0 && i < len(v)-1 {
		ns, name = v[:i], v[i+1:]
	}
	return ObjectRef{APIVersion: "argoproj.io/v1alpha1", Kind: "Application", Namespace: ns, Name: name}, true
}
