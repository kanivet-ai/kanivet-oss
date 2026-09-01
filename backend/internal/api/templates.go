package api

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kanivet/backend/internal/utils"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer/yaml"
)

func (h *Handler) UpdateResource(c *gin.Context) {
	cluster := c.Query("cluster")
	if cluster == "" {
		h.respond(c, http.StatusBadRequest, nil, fmt.Errorf("cluster parameter is required"))
		return
	}

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		h.respond(c, http.StatusBadRequest, nil, fmt.Errorf("failed to read request body: %v", err))
		return
	}

	decoder := yaml.NewDecodingSerializer(unstructured.UnstructuredJSONScheme)
	obj := &unstructured.Unstructured{}
	_, gvk, err := decoder.Decode(body, nil, obj)
	if err != nil {
		h.respond(c, http.StatusBadRequest, nil, fmt.Errorf("failed to decode YAML: %v", err))
		return
	}

	gvr := schema.GroupVersionResource{
		Group:    gvk.Group,
		Version:  gvk.Version,
		Resource: h.getResourceName(gvk.Kind),
	}

	namespace := obj.GetNamespace()
	name := obj.GetName()

	if name == "" {
		h.respond(c, http.StatusBadRequest, nil, fmt.Errorf("resource name is required"))
		return
	}

	updatedResource, err := h.k8s.UpdateResource(context.Background(), cluster, gvr, namespace, name, obj)
	if err != nil {
		h.respond(c, http.StatusInternalServerError, nil, fmt.Errorf("failed to update resource: %v", err))
		return
	}

	detailKey := h.cache.BuildKey("detail", cluster, gvk.Group, gvk.Version, h.getResourceName(gvk.Kind), namespace, name)
	h.cache.Delete(detailKey)
	topic := fmt.Sprintf("items:%s:%s:%s:%s:%s", cluster, gvk.Group, gvk.Version, h.getResourceName(gvk.Kind), namespace)
	if h.invalidationBus != nil {
		h.invalidationBus.Invalidate(topic)
	}
	dashboardKey := h.cache.BuildKey("dashboard", cluster)
	h.cache.Delete(dashboardKey)

	h.respond(c, http.StatusOK, updatedResource, nil)
}

func (h *Handler) CreateResource(c *gin.Context) {
	cluster := c.Query("cluster")
	if cluster == "" {
		h.respond(c, http.StatusBadRequest, nil, fmt.Errorf("cluster parameter is required"))
		return
	}

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		h.respond(c, http.StatusBadRequest, nil, fmt.Errorf("failed to read request body: %v", err))
		return
	}

	yamlDocs := bytes.Split(body, []byte("\n---\n"))
	createdResources := []interface{}{}
	errors := []string{}

	for i, doc := range yamlDocs {
		trimmed := bytes.TrimSpace(doc)
		if len(trimmed) == 0 || string(trimmed) == "---" {
			continue
		}

		lines := bytes.Split(trimmed, []byte("\n"))
		allComments := true
		for _, line := range lines {
			trimmedLine := bytes.TrimSpace(line)
			if len(trimmedLine) > 0 && !bytes.HasPrefix(trimmedLine, []byte("#")) {
				allComments = false
				break
			}
		}
		if allComments {
			continue
		}

		decoder := yaml.NewDecodingSerializer(unstructured.UnstructuredJSONScheme)
		obj := &unstructured.Unstructured{}
		_, gvk, err := decoder.Decode(doc, nil, obj)
		if err != nil {
			errors = append(errors, fmt.Sprintf("document %d: failed to decode YAML: %v", i+1, err))
			continue
		}

		gvr := schema.GroupVersionResource{
			Group:    gvk.Group,
			Version:  gvk.Version,
			Resource: h.getResourceName(gvk.Kind),
		}

		namespace := obj.GetNamespace()
		name := obj.GetName()

		if name == "" {
			errors = append(errors, fmt.Sprintf("document %d: resource name is required", i+1))
			continue
		}

		unstructured.RemoveNestedField(obj.Object, "metadata", "resourceVersion")
		unstructured.RemoveNestedField(obj.Object, "metadata", "uid")
		unstructured.RemoveNestedField(obj.Object, "metadata", "selfLink")
		unstructured.RemoveNestedField(obj.Object, "metadata", "generation")
		unstructured.RemoveNestedField(obj.Object, "status")

		createdResource, err := h.k8s.CreateResource(context.Background(), cluster, gvr, namespace, name, obj)
		if err != nil {
			errors = append(errors, fmt.Sprintf("document %d (%s/%s): %v", i+1, gvk.Kind, name, err))
			continue
		}

		createdResources = append(createdResources, createdResource)
		topic := fmt.Sprintf("items:%s:%s:%s:%s:%s", cluster, gvk.Group, gvk.Version, h.getResourceName(gvk.Kind), namespace)
		if h.invalidationBus != nil {
			h.invalidationBus.Invalidate(topic)
		}
	}

	dashboardKey := h.cache.BuildKey("dashboard", cluster)
	h.cache.Delete(dashboardKey)

	if len(errors) > 0 && len(createdResources) == 0 {
		h.respond(c, http.StatusBadRequest, nil, fmt.Errorf("failed to create resources: %v", strings.Join(errors, "; ")))
	} else if len(errors) > 0 && len(createdResources) > 0 {
		h.respond(c, http.StatusPartialContent, gin.H{"created": createdResources, "errors": errors}, nil)
	} else if len(createdResources) == 1 {
		h.respond(c, http.StatusCreated, createdResources[0], nil)
	} else {
		h.respond(c, http.StatusCreated, gin.H{"created": createdResources, "count": len(createdResources)}, nil)
	}
}

func (h *Handler) GetResourceSchema(c *gin.Context) {
	cluster := c.Query("cluster")
	if cluster == "" {
		h.respond(c, http.StatusBadRequest, nil, fmt.Errorf("cluster parameter is required"))
		return
	}

	group := c.Param("group")
	version := c.Param("version")
	kind := c.Param("kind")

	if group == "_" {
		group = ""
	}

	if group != "" {
		crdClient, err := h.k8s.GetDynamicClient(cluster)
		if err != nil {
			h.respond(c, http.StatusInternalServerError, nil, err)
			return
		}

		pluralName := strings.ToLower(utils.PluralizeKind(kind))
		crdName := fmt.Sprintf("%s.%s", pluralName, group)
		crdGVR := schema.GroupVersionResource{
			Group:    "apiextensions.k8s.io",
			Version:  "v1",
			Resource: "customresourcedefinitions",
		}

		crd, err := crdClient.Resource(crdGVR).Get(context.Background(), crdName, metav1.GetOptions{})
		if err != nil {
			crdGVR.Version = "v1beta1"
			crd, err = crdClient.Resource(crdGVR).Get(context.Background(), crdName, metav1.GetOptions{})
			if err != nil {
				h.respond(c, http.StatusNotFound, nil, fmt.Errorf("CRD not found: %v", err))
				return
			}
		}

		template := h.generateTemplateFromCRD(crd, group, version, kind)
		h.respond(c, http.StatusOK, gin.H{"template": template, "schema": crd.Object}, nil)
		return
	}

	template := h.getBuiltInResourceTemplate(kind)
	h.respond(c, http.StatusOK, gin.H{"template": template}, nil)
}

func (h *Handler) generateTemplateFromCRD(crd *unstructured.Unstructured, group, version, kind string) string {
	template := fmt.Sprintf(`apiVersion: %s/%s
kind: %s
metadata:
  name: <Fill here>
`, group, version, kind)

	versions, found, _ := unstructured.NestedSlice(crd.Object, "spec", "versions")
	if !found {
		if schema, found, _ := unstructured.NestedMap(crd.Object, "spec", "validation", "openAPIV3Schema", "properties"); found {
			if specSchema, hasSpec, _ := unstructured.NestedMap(schema, "spec"); hasSpec {
				template += h.generateSpecFromSchema("spec", specSchema, 0)
			} else {
				for key, val := range schema {
					if key != "apiVersion" && key != "kind" && key != "metadata" {
						if propSchema, ok := val.(map[string]interface{}); ok {
							template += h.generateSpecFromSchema(key, propSchema, 0)
						}
					}
				}
			}
		}
		return template
	}

	for _, v := range versions {
		versionMap, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		versionName, _ := versionMap["name"].(string)
		if versionName != version {
			continue
		}

		var schemaRoot map[string]interface{}
		if s, found, _ := unstructured.NestedMap(versionMap, "schema", "openAPIV3Schema"); found {
			schemaRoot = s
		}

		if schemaRoot != nil {
			if props, found, _ := unstructured.NestedMap(schemaRoot, "properties"); found {
				if specSchema, hasSpec, _ := unstructured.NestedMap(props, "spec"); hasSpec {
					template += h.generateSpecFromSchema("spec", specSchema, 0)
				} else {
					for key, val := range props {
						if key != "apiVersion" && key != "kind" && key != "metadata" && key != "status" {
							if propSchema, ok := val.(map[string]interface{}); ok {
								template += h.generateSpecFromSchema(key, propSchema, 0)
							}
						}
					}
				}
			}
		}
		break
	}
	return template
}

func (h *Handler) generateSpecFromSchema(key string, schema map[string]interface{}, indent int) string {
	result := ""
	indentStr := strings.Repeat("  ", indent)
	schemaType, _, _ := unstructured.NestedString(schema, "type")
	properties, hasProps, _ := unstructured.NestedMap(schema, "properties")

	if (schemaType == "object" || hasProps) && key != "" {
		result += fmt.Sprintf("%s%s:\n", indentStr, key)
		indent++
		indentStr = strings.Repeat("  ", indent)
	} else if key != "" && !hasProps {
		return h.generateLeafNode(key, schema, indent)
	} else if key == "" && !hasProps && schemaType != "object" {
		return ""
	}

	requiredFields := make(map[string]bool)
	if required, hasReq, _ := unstructured.NestedStringSlice(schema, "required"); hasReq {
		for _, field := range required {
			requiredFields[field] = true
		}
	}

	if hasProps {
		var keys []string
		for k := range properties {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, propKey := range keys {
			propValue := properties[propKey]
			propSchema, ok := propValue.(map[string]interface{})
			if !ok {
				continue
			}
			if defVal, hasDefault, _ := unstructured.NestedFieldNoCopy(propSchema, "default"); hasDefault {
				result += h.formatDefaultValue(propKey, defVal, indent)
				continue
			}
			propType, _, _ := unstructured.NestedString(propSchema, "type")
			_, hasNestedProps, _ := unstructured.NestedMap(propSchema, "properties")
			if hasNestedProps || propType == "object" {
				result += h.generateSpecFromSchema(propKey, propSchema, indent)
			} else if propType == "array" {
				result += h.generateArrayField(propKey, propSchema, indent, requiredFields[propKey])
			} else {
				result += h.generateSimpleField(propKey, propSchema, propType, indent, requiredFields[propKey])
			}
		}
	}

	if additionalProps, hasAdditional, _ := unstructured.NestedFieldNoCopy(schema, "additionalProperties"); hasAdditional {
		if key != "" && schemaType == "object" {
			switch ap := additionalProps.(type) {
			case bool:
				if ap {
					result += fmt.Sprintf("%s# <key>: <value>  # Additional properties allowed\n", indentStr)
				}
			case map[string]interface{}:
				propType, _, _ := unstructured.NestedString(ap, "type")
				result += fmt.Sprintf("%s# <key>: <value - %s>  # Additional properties\n", indentStr, propType)
			}
		}
	}

	if preserve, hasPreserve, _ := unstructured.NestedBool(schema, "x-kubernetes-preserve-unknown-fields"); hasPreserve && preserve {
		result += fmt.Sprintf("%s# Additional fields are preserved\n", indentStr)
	}
	return result
}

func (h *Handler) generateLeafNode(key string, schema map[string]interface{}, indent int) string {
	if defVal, hasDefault, _ := unstructured.NestedFieldNoCopy(schema, "default"); hasDefault {
		return h.formatDefaultValue(key, defVal, indent)
	}
	schemaType, _, _ := unstructured.NestedString(schema, "type")
	isRequired := false
	if req, hasReq, _ := unstructured.NestedBool(schema, "required"); hasReq {
		isRequired = req
	}
	return h.generateSimpleField(key, schema, schemaType, indent, isRequired)
}

func (h *Handler) formatDefaultValue(key string, defVal interface{}, indent int) string {
	indentStr := strings.Repeat("  ", indent)
	switch v := defVal.(type) {
	case map[string]interface{}:
		if len(v) == 0 {
			return fmt.Sprintf("%s%s: {}\n", indentStr, key)
		}
		result := fmt.Sprintf("%s%s:\n", indentStr, key)
		for k, val := range v {
			result += fmt.Sprintf("%s  %s: %v\n", indentStr, k, val)
		}
		return result
	case []interface{}:
		if len(v) == 0 {
			return fmt.Sprintf("%s%s: []\n", indentStr, key)
		}
		result := fmt.Sprintf("%s%s:\n", indentStr, key)
		for _, item := range v {
			result += fmt.Sprintf("%s- %v\n", strings.Repeat("  ", indent+1), item)
		}
		return result
	default:
		return fmt.Sprintf("%s%s: %v\n", indentStr, key, defVal)
	}
}

func (h *Handler) generateArrayField(key string, schema map[string]interface{}, indent int, isRequired bool) string {
	indentStr := strings.Repeat("  ", indent)
	result := fmt.Sprintf("%s%s:\n", indentStr, key)
	itemSchema, hasItems, _ := unstructured.NestedMap(schema, "items")

	if hasItems {
		itemType, _, _ := unstructured.NestedString(itemSchema, "type")
		if _, hasItemProps, _ := unstructured.NestedMap(itemSchema, "properties"); hasItemProps {
			if isRequired {
				result += fmt.Sprintf("%s- # Required array item\n", strings.Repeat("  ", indent+1))
				itemContent := h.generateSpecFromSchema("", itemSchema, indent+2)
				lines := strings.Split(strings.TrimRight(itemContent, "\n"), "\n")
				for _, line := range lines {
					if line != "" {
						result += fmt.Sprintf("%s  %s\n", strings.Repeat("  ", indent+1), strings.TrimLeft(line, " "))
					}
				}
			} else {
				result += fmt.Sprintf("%s# - # Array item\n", strings.Repeat("  ", indent+1))
				itemContent := h.generateSpecFromSchema("", itemSchema, indent+2)
				lines := strings.Split(strings.TrimRight(itemContent, "\n"), "\n")
				for _, line := range lines {
					if line != "" {
						result += fmt.Sprintf("%s#   %s\n", strings.Repeat("  ", indent+1), strings.TrimLeft(line, " "))
					}
				}
			}
		} else if itemType == "string" {
			if isRequired {
				result += fmt.Sprintf("%s- <Fill here - required string>\n", strings.Repeat("  ", indent+1))
			} else {
				result += fmt.Sprintf("%s# - <Fill here - string>\n", strings.Repeat("  ", indent+1))
			}
		} else {
			if isRequired {
				result += fmt.Sprintf("%s- <Fill here - required %s>\n", strings.Repeat("  ", indent+1), itemType)
			} else {
				result += fmt.Sprintf("%s# - <Fill here - %s>\n", strings.Repeat("  ", indent+1), itemType)
			}
		}
	} else {
		if isRequired {
			result += fmt.Sprintf("%s- <Fill here - required>\n", strings.Repeat("  ", indent+1))
		} else {
			result += fmt.Sprintf("%s# - <Fill here>\n", strings.Repeat("  ", indent+1))
		}
	}
	return result
}

func (h *Handler) generateSimpleField(key string, schema map[string]interface{}, fieldType string, indent int, isRequired bool) string {
	indentStr := strings.Repeat("  ", indent)

	if enumVals, hasEnum, _ := unstructured.NestedStringSlice(schema, "enum"); hasEnum && len(enumVals) > 0 {
		comment := fmt.Sprintf("Options: %s", strings.Join(enumVals, ", "))
		if isRequired {
			return fmt.Sprintf("%s%s: <Fill here - required - %s>\n", indentStr, key, comment)
		}
		return fmt.Sprintf("%s# %s: %s  # %s\n", indentStr, key, enumVals[0], comment)
	}

	switch fieldType {
	case "string":
		if isRequired {
			return fmt.Sprintf("%s%s: <Fill here - required string>\n", indentStr, key)
		}
		return fmt.Sprintf("%s# %s: <Fill here - string>\n", indentStr, key)
	case "integer", "number":
		if isRequired {
			return fmt.Sprintf("%s%s: <Fill here - required number>\n", indentStr, key)
		}
		return fmt.Sprintf("%s# %s: 0\n", indentStr, key)
	case "boolean":
		if isRequired {
			return fmt.Sprintf("%s%s: <Fill here - required boolean>\n", indentStr, key)
		}
		return fmt.Sprintf("%s# %s: false\n", indentStr, key)
	default:
		if fieldType == "object" {
			if isRequired {
				return fmt.Sprintf("%s%s: {}  # <Fill here - required map>\n", indentStr, key)
			}
			return fmt.Sprintf("%s# %s: {}  # <Fill here - map>\n", indentStr, key)
		}
		if isRequired {
			return fmt.Sprintf("%s%s: <Fill here - required>\n", indentStr, key)
		}
		return fmt.Sprintf("%s# %s: <Fill here>\n", indentStr, key)
	}
}

func (h *Handler) getBuiltInResourceTemplate(kind string) string {
	templates := map[string]string{
		"Pod": `apiVersion: v1
kind: Pod
metadata:
  name: <Fill here>
  namespace: default
spec:
  containers:
  - name: <Fill here>
    image: <Fill here - required>
    # ports:
    # - containerPort: 80`,
		"Service": `apiVersion: v1
kind: Service
metadata:
  name: <Fill here>
  namespace: default
spec:
  selector:
    app: <Fill here - required>
  ports:
  - port: 80
    targetPort: 8080
    # nodePort: 30000  # For NodePort type
  # type: ClusterIP  # Options: ClusterIP, NodePort, LoadBalancer`,
		"ConfigMap": `apiVersion: v1
kind: ConfigMap
metadata:
  name: <Fill here>
  namespace: default
data:
  # key: value`,
		"Secret": `apiVersion: v1
kind: Secret
metadata:
  name: <Fill here>
  namespace: default
type: Opaque  # Options: Opaque, kubernetes.io/tls, kubernetes.io/dockerconfigjson
data:
  # key: <base64 encoded value>
stringData:
  # key: value  # Use stringData for automatic base64 encoding`,
		"Deployment": `apiVersion: apps/v1
kind: Deployment
metadata:
  name: <Fill here>
  namespace: default
spec:
  replicas: 1
  selector:
    matchLabels:
      app: <Fill here - required>
  template:
    metadata:
      labels:
        app: <Fill here - must match selector>
    spec:
      containers:
      - name: <Fill here>
        image: <Fill here - required>
        # ports:
        # - containerPort: 8080
        # resources:
        #   requests:
        #     memory: "64Mi"
        #     cpu: "250m"
        #   limits:
        #     memory: "128Mi"
        #     cpu: "500m"`,
		"StatefulSet": `apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: <Fill here>
  namespace: default
spec:
  serviceName: <Fill here - required>
  replicas: 1
  selector:
    matchLabels:
      app: <Fill here - required>
  template:
    metadata:
      labels:
        app: <Fill here - must match selector>
    spec:
      containers:
      - name: <Fill here>
        image: <Fill here - required>
        # volumeMounts:
        # - name: data
        #   mountPath: /data
  # volumeClaimTemplates:
  # - metadata:
  #     name: data
  #   spec:
  #     accessModes: [ "ReadWriteOnce" ]
  #     resources:
  #       requests:
  #         storage: 1Gi`,
		"DaemonSet": `apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: <Fill here>
  namespace: default
spec:
  selector:
    matchLabels:
      app: <Fill here - required>
  template:
    metadata:
      labels:
        app: <Fill here - must match selector>
    spec:
      containers:
      - name: <Fill here>
        image: <Fill here - required>
      # tolerations:
      # - key: node-role.kubernetes.io/master
      #   effect: NoSchedule`,
		"Job": `apiVersion: batch/v1
kind: Job
metadata:
  name: <Fill here>
  namespace: default
spec:
  template:
    spec:
      containers:
      - name: <Fill here>
        image: <Fill here - required>
        command: <Fill here - required>
      restartPolicy: Never
  # backoffLimit: 4`,
		"CronJob": `apiVersion: batch/v1
kind: CronJob
metadata:
  name: <Fill here>
  namespace: default
spec:
  schedule: "*/5 * * * *"  # <Fill here - cron expression>
  jobTemplate:
    spec:
      template:
        spec:
          containers:
          - name: <Fill here>
            image: <Fill here - required>
            command: <Fill here - required>
          restartPolicy: OnFailure`,
		"Ingress": `apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: <Fill here>
  namespace: default
  # annotations:
  #   nginx.ingress.kubernetes.io/rewrite-target: /
spec:
  # ingressClassName: nginx
  rules:
  - host: <Fill here - required>
    http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: <Fill here - required>
            port:
              number: 80`,
		"PersistentVolumeClaim": `apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: <Fill here>
  namespace: default
spec:
  accessModes:
  - ReadWriteOnce  # Options: ReadWriteOnce, ReadOnlyMany, ReadWriteMany
  resources:
    requests:
      storage: 1Gi
  # storageClassName: standard`,
		"ServiceAccount": `apiVersion: v1
kind: ServiceAccount
metadata:
  name: <Fill here>
  namespace: default
# secrets:
# - name: <secret-name>`,
		"Role": `apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: <Fill here>
  namespace: default
rules:
- apiGroups: [""]
  resources: ["pods"]
  verbs: ["get", "list", "watch"]`,
		"RoleBinding": `apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: <Fill here>
  namespace: default
subjects:
- kind: ServiceAccount
  name: <Fill here - required>
  namespace: default
roleRef:
  kind: Role
  name: <Fill here - required>
  apiGroup: rbac.authorization.k8s.io`,
		"ClusterRole": `apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: <Fill here>
rules:
- apiGroups: [""]
  resources: ["pods"]
  verbs: ["get", "list", "watch"]`,
		"ClusterRoleBinding": `apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: <Fill here>
subjects:
- kind: ServiceAccount
  name: <Fill here - required>
  namespace: <Fill here - required>
roleRef:
  kind: ClusterRole
  name: <Fill here - required>
  apiGroup: rbac.authorization.k8s.io`,
		"NetworkPolicy": `apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: <Fill here>
  namespace: default
spec:
  podSelector:
    matchLabels:
      app: <Fill here - required>
  policyTypes:
  - Ingress
  - Egress
  # ingress:
  # - from:
  #   - podSelector:
  #       matchLabels:
  #         app: allowed-app
  #   ports:
  #   - protocol: TCP
  #     port: 80`,
		"HorizontalPodAutoscaler": `apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: <Fill here>
  namespace: default
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: <Fill here - required>
  minReplicas: 1
  maxReplicas: 10
  metrics:
  - type: Resource
    resource:
      name: cpu
      target:
        type: Utilization
        averageUtilization: 80`,
		"PodDisruptionBudget": `apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: <Fill here>
  namespace: default
spec:
  minAvailable: 1  # or use maxUnavailable
  selector:
    matchLabels:
      app: <Fill here - required>`,
	}

	if template, exists := templates[kind]; exists {
		return template
	}
	return fmt.Sprintf(`apiVersion: <Fill here - required>
kind: %s
metadata:
  name: <Fill here>
  namespace: default
spec:
  <Fill here - required>`, kind)
}

func (h *Handler) getResourceName(kind string) string {
	kindToResource := map[string]string{
		"Pod": "pods", "Service": "services", "ConfigMap": "configmaps",
		"Secret": "secrets", "Deployment": "deployments", "StatefulSet": "statefulsets",
		"DaemonSet": "daemonsets", "ReplicaSet": "replicasets", "Job": "jobs",
		"CronJob": "cronjobs", "Ingress": "ingresses", "IngressClass": "ingressclasses",
		"PersistentVolume": "persistentvolumes", "PersistentVolumeClaim": "persistentvolumeclaims",
		"ServiceAccount": "serviceaccounts", "Role": "roles", "RoleBinding": "rolebindings",
		"ClusterRole": "clusterroles", "ClusterRoleBinding": "clusterrolebindings",
		"Namespace": "namespaces", "Node": "nodes", "NetworkPolicy": "networkpolicies",
		"HorizontalPodAutoscaler": "horizontalpodautoscalers", "VerticalPodAutoscaler": "verticalpodautoscalers",
		"StorageClass": "storageclasses", "PriorityClass": "priorityclasses",
		"ResourceQuota": "resourcequotas", "LimitRange": "limitranges",
		"PodDisruptionBudget": "poddisruptionbudgets", "EndpointSlice": "endpointslices",
		"Endpoints": "endpoints", "Event": "events", "VolumeAttachment": "volumeattachments",
		"CSINode": "csinodes", "CSIDriver": "csidrivers", "CSIStorageCapacity": "csistoragecapacities",
		"ValidatingWebhookConfiguration": "validatingwebhookconfigurations",
		"MutatingWebhookConfiguration":   "mutatingwebhookconfigurations",
		"CustomResourceDefinition":       "customresourcedefinitions",
		"APIService":                     "apiservices", "TokenReview": "tokenreviews",
		"SubjectAccessReview": "subjectaccessreviews", "SelfSubjectAccessReview": "selfsubjectaccessreviews",
		"LocalSubjectAccessReview": "localsubjectaccessreviews", "Lease": "leases",
		"RuntimeClass": "runtimeclasses", "PodTemplate": "podtemplates",
		"ReplicationController": "replicationcontrollers", "Certificate": "certificates",
		"CertificateSigningRequest": "certificatesigningrequests",
		"Issuer":                    "issuers", "ClusterIssuer": "clusterissuers",
	}

	if resource, ok := kindToResource[kind]; ok {
		return resource
	}
	if strings.HasSuffix(kind, "y") && !strings.HasSuffix(kind, "ay") && !strings.HasSuffix(kind, "ey") && !strings.HasSuffix(kind, "oy") && !strings.HasSuffix(kind, "uy") {
		return strings.ToLower(kind[:len(kind)-1]) + "ies"
	}
	if strings.HasSuffix(kind, "s") || strings.HasSuffix(kind, "x") || strings.HasSuffix(kind, "ch") || strings.HasSuffix(kind, "sh") {
		if strings.HasSuffix(kind, "ions") {
			return strings.ToLower(kind)
		}
		return strings.ToLower(kind) + "es"
	}
	return strings.ToLower(kind) + "s"
}
