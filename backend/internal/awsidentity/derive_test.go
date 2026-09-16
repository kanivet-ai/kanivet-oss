package awsidentity

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func podWithEnv(env ...corev1.EnvVar) *corev1.Pod {
	return &corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Env: env}}}}
}

func findCheck(checks []Check, action, resource string) *Check {
	for i := range checks {
		if checks[i].Action == action && checks[i].Resource == resource {
			return &checks[i]
		}
	}
	return nil
}

func TestDeriveChecks(t *testing.T) {
	const region, account = "eu-west-1", "123456789012"

	tests := []struct {
		name string
		pod  *corev1.Pod
		want []derivedCheck
		len  int
	}{
		{
			name: "s3 csi volume",
			pod: &corev1.Pod{Spec: corev1.PodSpec{Volumes: []corev1.Volume{{
				Name: "data",
				VolumeSource: corev1.VolumeSource{CSI: &corev1.CSIVolumeSource{
					Driver:           s3CSIDriver,
					VolumeAttributes: map[string]string{"bucketName": "my-data-bucket"},
				}},
			}}}},
			want: []derivedCheck{
				{action: "s3:ListBucket", resource: "arn:aws:s3:::my-data-bucket"},
				{action: "s3:GetObject", resource: "arn:aws:s3:::my-data-bucket/*"},
			},
			len: 2,
		},
		{
			name: "sqs queue url to arn",
			pod:  podWithEnv(corev1.EnvVar{Name: "ORDERS_QUEUE_URL", Value: "https://sqs.us-east-1.amazonaws.com/123456789012/orders"}),
			want: []derivedCheck{
				{action: "sqs:ReceiveMessage", resource: "arn:aws:sqs:us-east-1:123456789012:orders"},
				{action: "sqs:SendMessage", resource: "arn:aws:sqs:us-east-1:123456789012:orders"},
			},
			len: 2,
		},
		{
			name: "dynamodb table",
			pod:  podWithEnv(corev1.EnvVar{Name: "DYNAMODB_TABLE", Value: "sessions"}),
			want: []derivedCheck{
				{action: "dynamodb:GetItem", resource: "arn:aws:dynamodb:eu-west-1:123456789012:table/sessions"},
				{action: "dynamodb:Query", resource: "arn:aws:dynamodb:eu-west-1:123456789012:table/sessions"},
			},
			len: 2,
		},
		{
			name: "secret arn literal",
			pod:  podWithEnv(corev1.EnvVar{Name: "DB_SECRET_ARN", Value: "arn:aws:secretsmanager:eu-west-1:123456789012:secret:prod/db-AbC123"}),
			want: []derivedCheck{{action: "secretsmanager:GetSecretValue", resource: "arn:aws:secretsmanager:eu-west-1:123456789012:secret:prod/db-AbC123"}},
			len:  1,
		},
		{
			name: "secret name builds arn",
			pod:  podWithEnv(corev1.EnvVar{Name: "DB_SECRET_NAME", Value: "prod/db"}),
			want: []derivedCheck{{action: "secretsmanager:GetSecretValue", resource: "arn:aws:secretsmanager:eu-west-1:123456789012:secret:prod/db-AbCdEf"}},
			len:  1,
		},
		{
			name: "literal arns by service",
			pod: podWithEnv(
				corev1.EnvVar{Name: "KEY", Value: "arn:aws:kms:eu-west-1:123456789012:key/abc"},
				corev1.EnvVar{Name: "EVENTS_TOPIC_ARN", Value: "arn:aws:sns:eu-west-1:123456789012:events"},
				corev1.EnvVar{Name: "CONFIG", Value: "arn:aws:ssm:eu-west-1:123456789012:parameter/app/config"},
			),
			want: []derivedCheck{
				{action: "kms:Decrypt", resource: "arn:aws:kms:eu-west-1:123456789012:key/abc"},
				{action: "sns:Publish", resource: "arn:aws:sns:eu-west-1:123456789012:events"},
				{action: "ssm:GetParameter", resource: "arn:aws:ssm:eu-west-1:123456789012:parameter/app/config"},
			},
			len: 3,
		},
		{
			name: "s3 uri with prefix",
			pod:  podWithEnv(corev1.EnvVar{Name: "S3_BUCKET", Value: "s3://reports/2026"}),
			want: []derivedCheck{
				{action: "s3:ListBucket", resource: "arn:aws:s3:::reports"},
				{action: "s3:GetObject", resource: "arn:aws:s3:::reports/2026/*"},
			},
			len: 2,
		},
		{
			name: "dedup across containers and skip valueFrom",
			pod: &corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{
				{Name: "a", Env: []corev1.EnvVar{{Name: "BUCKET", Value: "shared"}}},
				{Name: "b", Env: []corev1.EnvVar{
					{Name: "BUCKET_NAME", Value: "shared"},
					{Name: "OTHER_BUCKET", ValueFrom: &corev1.EnvVarSource{}},
				}},
			}}},
			want: []derivedCheck{{action: "s3:ListBucket", resource: "arn:aws:s3:::shared"}},
			len:  2,
		},
		{
			name: "ignores unrelated env",
			pod:  podWithEnv(corev1.EnvVar{Name: "LOG_LEVEL", Value: "debug"}, corev1.EnvVar{Name: "BUCKET", Value: "Not A Bucket"}),
			len:  0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			checks := DeriveChecks(tc.pod, region, account)
			if len(checks) != tc.len {
				t.Fatalf("got %d checks, want %d: %+v", len(checks), tc.len, checks)
			}
			for _, want := range tc.want {
				got := findCheck(checks, want.action, want.resource)
				if got == nil {
					t.Fatalf("missing check %s on %s in %+v", want.action, want.resource, checks)
				}
				if got.ID == "" || got.Service == "" {
					t.Fatalf("check missing id/service: %+v", got)
				}
			}
		})
	}
}

func TestDeriveChecksStableIDs(t *testing.T) {
	pod := podWithEnv(corev1.EnvVar{Name: "BUCKET", Value: "stable"})
	first := DeriveChecks(pod, "eu-west-1", "123456789012")
	second := DeriveChecks(pod, "eu-west-1", "123456789012")
	if first[0].ID != second[0].ID || first[0].ID != "env:app:BUCKET:s3:ListBucket:arn:aws:s3:::stable" {
		t.Fatalf("unexpected id %q / %q", first[0].ID, second[0].ID)
	}
	if first[0].Source.Container != "app" || first[0].Source.Kind != "env" {
		t.Fatalf("unexpected source %+v", first[0].Source)
	}
}
