package awsidentity

import (
	"context"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	corev1 "k8s.io/api/core/v1"
)

const (
	annotationRoleArn           = "eks.amazonaws.com/role-arn"
	annotationAudience          = "eks.amazonaws.com/audience"
	annotationRegionalEndpoints = "eks.amazonaws.com/sts-regional-endpoints"
	annotationTokenExpiration   = "eks.amazonaws.com/token-expiration"

	envRoleArn              = "AWS_ROLE_ARN"
	envWebIdentityTokenFile = "AWS_WEB_IDENTITY_TOKEN_FILE"
	envContainerCredsURI    = "AWS_CONTAINER_CREDENTIALS_FULL_URI"
	envContainerAuthFile    = "AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE"

	podIdentityTokenVolume = "eks-pod-identity-token"
)

var irsaAnnotationKeys = []string{annotationRoleArn, annotationAudience, annotationRegionalEndpoints, annotationTokenExpiration}

func bindingFromServiceAccount(sa *corev1.ServiceAccount) *Binding {
	if sa == nil {
		return nil
	}
	roleArn := strings.TrimSpace(sa.Annotations[annotationRoleArn])
	if roleArn == "" {
		return nil
	}
	annotations := make(map[string]string)
	for _, key := range irsaAnnotationKeys {
		if value, ok := sa.Annotations[key]; ok {
			annotations[key] = value
		}
	}
	return &Binding{Mechanism: MechanismIRSA, RoleArn: roleArn, Annotations: annotations}
}

func podIdentityBinding(ctx context.Context, client *eks.Client, clusterName, namespace, serviceAccount string) (*Binding, error) {
	out, err := client.ListPodIdentityAssociations(ctx, &eks.ListPodIdentityAssociationsInput{
		ClusterName:    aws.String(clusterName),
		Namespace:      aws.String(namespace),
		ServiceAccount: aws.String(serviceAccount),
	})
	if err != nil {
		return nil, err
	}
	if len(out.Associations) == 0 {
		return nil, nil
	}
	described, err := client.DescribePodIdentityAssociation(ctx, &eks.DescribePodIdentityAssociationInput{
		ClusterName:   aws.String(clusterName),
		AssociationId: out.Associations[0].AssociationId,
	})
	if err != nil {
		return nil, err
	}
	assoc := described.Association
	if assoc == nil {
		return nil, nil
	}
	return &Binding{
		Mechanism:      MechanismPodIdentity,
		RoleArn:        aws.ToString(assoc.RoleArn),
		AssociationID:  aws.ToString(assoc.AssociationId),
		AssociationArn: aws.ToString(assoc.AssociationArn),
		Tags:           assoc.Tags,
	}, nil
}

func podEvidence(pod *corev1.Pod) *PodEvidence {
	if pod == nil {
		return nil
	}
	evidence := &PodEvidence{
		Name:           pod.Name,
		Mechanism:      MechanismNone,
		ServiceAccount: firstNonEmpty(pod.Spec.ServiceAccountName, "default"),
		Phase:          string(pod.Status.Phase),
	}

	for _, volume := range pod.Spec.Volumes {
		if volume.Name == podIdentityTokenVolume {
			evidence.TokenVolume = volume.Name
			evidence.Mechanism = MechanismPodIdentity
			continue
		}
		if volume.Projected == nil {
			continue
		}
		for _, source := range volume.Projected.Sources {
			if source.ServiceAccountToken != nil && source.ServiceAccountToken.Audience == defaultIRSAAudience {
				evidence.TokenVolume = volume.Name
				if evidence.Mechanism == MechanismNone {
					evidence.Mechanism = MechanismIRSA
				}
			}
		}
	}

	containers := append(append([]corev1.Container{}, pod.Spec.InitContainers...), pod.Spec.Containers...)
	injected := map[string]bool{}
	for _, container := range containers {
		matched := false
		for _, env := range container.Env {
			switch env.Name {
			case envRoleArn:
				matched = true
				injected[env.Name] = true
				if evidence.RoleArn == "" {
					evidence.RoleArn = env.Value
				}
				if evidence.Mechanism == MechanismNone {
					evidence.Mechanism = MechanismIRSA
				}
			case envWebIdentityTokenFile:
				matched = true
				injected[env.Name] = true
				if evidence.Mechanism == MechanismNone {
					evidence.Mechanism = MechanismIRSA
				}
			case envContainerCredsURI, envContainerAuthFile:
				matched = true
				injected[env.Name] = true
				evidence.Mechanism = MechanismPodIdentity
			}
		}
		if matched {
			evidence.Containers = append(evidence.Containers, container.Name)
		}
	}
	for name := range injected {
		evidence.InjectedEnv = append(evidence.InjectedEnv, name)
	}
	sort.Strings(evidence.InjectedEnv)
	return evidence
}
