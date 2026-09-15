package awsidentity

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

const s3CSIDriver = "s3.csi.aws.com"

var (
	bucketEnvRe    = regexp.MustCompile(`(^|_)(S3_)?BUCKET(_NAME)?$`)
	queueURLEnvRe  = regexp.MustCompile(`QUEUE_URL$`)
	tableEnvRe     = regexp.MustCompile(`(^|_)(DYNAMODB_|DDB_)?TABLE(_NAME)?$`)
	secretEnvRe    = regexp.MustCompile(`(^|_)SECRET(_ARN|_NAME|_ID)?$`)
	topicEnvRe     = regexp.MustCompile(`TOPIC_ARN$`)
	parameterEnvRe = regexp.MustCompile(`(^|_)(SSM_)?PARAM(ETER)?(_NAME|_PATH)?$`)
	sqsURLRe       = regexp.MustCompile(`^https://sqs\.([a-z0-9-]+)\.amazonaws\.com(?:\.cn)?/(\d{12})/([A-Za-z0-9_.-]+)$`)
	bucketNameRe   = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)
)

type derivedCheck struct {
	service  string
	action   string
	resource string
}

type checkCollector struct {
	checks []Check
	seen   map[string]bool
}

func newCheckCollector() *checkCollector {
	return &checkCollector{seen: map[string]bool{}}
}

func (c *checkCollector) add(source CheckSource, items ...derivedCheck) {
	for _, item := range items {
		if item.action == "" || item.resource == "" {
			continue
		}
		key := strings.ToLower(item.action) + "|" + item.resource
		if c.seen[key] {
			continue
		}
		c.seen[key] = true
		c.checks = append(c.checks, Check{
			ID:       checkID(source, item.action, item.resource),
			Source:   source,
			Service:  item.service,
			Action:   item.action,
			Resource: item.resource,
		})
	}
}

func checkID(source CheckSource, action, resource string) string {
	parts := []string{source.Kind}
	if source.Container != "" {
		parts = append(parts, source.Container)
	}
	if source.Name != "" {
		parts = append(parts, source.Name)
	}
	parts = append(parts, action, resource)
	return strings.Join(parts, ":")
}

func DeriveChecks(pod *corev1.Pod, region, accountID string) []Check {
	collector := newCheckCollector()
	if pod == nil {
		return nil
	}

	for _, volume := range pod.Spec.Volumes {
		if volume.CSI == nil || volume.CSI.Driver != s3CSIDriver {
			continue
		}
		bucket := strings.TrimSpace(volume.CSI.VolumeAttributes["bucketName"])
		if bucket == "" {
			continue
		}
		collector.add(CheckSource{Kind: "volume", Name: volume.Name}, s3Checks(bucket, "")...)
	}

	containers := append(append([]corev1.Container{}, pod.Spec.InitContainers...), pod.Spec.Containers...)
	for _, container := range containers {
		for _, env := range container.Env {
			if env.ValueFrom != nil || strings.TrimSpace(env.Value) == "" {
				continue
			}
			source := CheckSource{Kind: "env", Name: env.Name, Container: container.Name}
			collector.add(source, deriveFromEnv(env.Name, strings.TrimSpace(env.Value), region, accountID)...)
		}
	}
	return collector.checks
}

func deriveFromEnv(name, value, region, accountID string) []derivedCheck {
	upper := strings.ToUpper(name)
	if strings.HasPrefix(value, "arn:aws") {
		return checksForArn(value)
	}

	switch {
	case bucketEnvRe.MatchString(upper):
		bucket, prefix := parseBucketValue(value)
		if bucket == "" {
			return nil
		}
		return s3Checks(bucket, prefix)
	case queueURLEnvRe.MatchString(upper):
		m := sqsURLRe.FindStringSubmatch(value)
		if m == nil {
			return nil
		}
		arn := fmt.Sprintf("arn:aws:sqs:%s:%s:%s", m[1], m[2], m[3])
		return []derivedCheck{
			{service: "sqs", action: "sqs:ReceiveMessage", resource: arn},
			{service: "sqs", action: "sqs:SendMessage", resource: arn},
		}
	case tableEnvRe.MatchString(upper):
		if region == "" || accountID == "" {
			return nil
		}
		arn := fmt.Sprintf("arn:aws:dynamodb:%s:%s:table/%s", region, accountID, value)
		return []derivedCheck{
			{service: "dynamodb", action: "dynamodb:GetItem", resource: arn},
			{service: "dynamodb", action: "dynamodb:Query", resource: arn},
		}
	case secretEnvRe.MatchString(upper):
		if region == "" || accountID == "" {
			return nil
		}
		return []derivedCheck{{service: "secretsmanager", action: "secretsmanager:GetSecretValue", resource: secretArn(region, accountID, value)}}
	case topicEnvRe.MatchString(upper):
		return nil
	case parameterEnvRe.MatchString(upper):
		if region == "" || accountID == "" {
			return nil
		}
		arn := fmt.Sprintf("arn:aws:ssm:%s:%s:parameter/%s", region, accountID, strings.TrimPrefix(value, "/"))
		return []derivedCheck{{service: "ssm", action: "ssm:GetParameter", resource: arn}}
	}
	return nil
}

func parseBucketValue(value string) (bucket, prefix string) {
	if strings.HasPrefix(value, "s3://") {
		rest := strings.TrimPrefix(value, "s3://")
		if idx := strings.Index(rest, "/"); idx >= 0 {
			return rest[:idx], strings.Trim(rest[idx+1:], "/")
		}
		return rest, ""
	}
	if !bucketNameRe.MatchString(value) {
		return "", ""
	}
	return value, ""
}

func s3Checks(bucket, prefix string) []derivedCheck {
	objects := fmt.Sprintf("arn:aws:s3:::%s/*", bucket)
	if prefix != "" {
		objects = fmt.Sprintf("arn:aws:s3:::%s/%s/*", bucket, prefix)
	}
	return []derivedCheck{
		{service: "s3", action: "s3:ListBucket", resource: "arn:aws:s3:::" + bucket},
		{service: "s3", action: "s3:GetObject", resource: objects},
	}
}

// Secrets Manager ARNs end in a random 6-character suffix; policies usually match it with a wildcard.
func secretArn(region, accountID, value string) string {
	return fmt.Sprintf("arn:aws:secretsmanager:%s:%s:secret:%s-AbCdEf", region, accountID, value)
}

func checksForArn(arn string) []derivedCheck {
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) < 6 {
		return nil
	}
	service := parts[2]
	switch service {
	case "s3":
		rest := parts[5]
		if strings.Contains(rest, "/") {
			return []derivedCheck{{service: "s3", action: "s3:GetObject", resource: arn}}
		}
		return s3Checks(rest, "")
	case "sqs":
		return []derivedCheck{{service: "sqs", action: "sqs:ReceiveMessage", resource: arn}}
	case "dynamodb":
		return []derivedCheck{{service: "dynamodb", action: "dynamodb:GetItem", resource: arn}}
	case "secretsmanager":
		return []derivedCheck{{service: "secretsmanager", action: "secretsmanager:GetSecretValue", resource: arn}}
	case "kms":
		return []derivedCheck{{service: "kms", action: "kms:Decrypt", resource: arn}}
	case "sns":
		return []derivedCheck{{service: "sns", action: "sns:Publish", resource: arn}}
	case "ssm":
		return []derivedCheck{{service: "ssm", action: "ssm:GetParameter", resource: arn}}
	}
	return nil
}

func serviceFromAction(action string) string {
	if idx := strings.Index(action, ":"); idx > 0 {
		return strings.ToLower(action[:idx])
	}
	return ""
}

func manualCheck(action, resource string) Check {
	action = strings.TrimSpace(action)
	resource = strings.TrimSpace(resource)
	if resource == "" {
		resource = "*"
	}
	return Check{
		ID:       "manual:" + action + ":" + url.PathEscape(resource),
		Source:   CheckSource{Kind: "manual"},
		Service:  serviceFromAction(action),
		Action:   action,
		Resource: resource,
	}
}
