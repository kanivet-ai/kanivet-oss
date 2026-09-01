package listadapters

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func init() {
	register(schema.GroupVersionResource{Group: "", Version: "v1", Resource: "services"}, service)
	register(schema.GroupVersionResource{Group: "", Version: "v1", Resource: "endpoints"}, endpoints)
	register(schema.GroupVersionResource{Group: "discovery.k8s.io", Version: "v1", Resource: "endpointslices"}, endpointslice)
	register(schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"}, ingress)
	register(schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingressclasses"}, ingressclass)
	register(schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"}, generic)
	register(schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "gateways"}, gateway)
	register(schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "gatewayclasses"}, gatewayclass)
	register(schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "httproutes"}, routeAdapter)
	register(schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "grpcroutes"}, routeAdapter)
}

func service(u *unstructured.Unstructured, gvr schema.GroupVersionResource) map[string]interface{} {
	item := baseMeta(u, gvr)
	obj := u.Object

	if spec := getMap(obj, "spec"); spec != nil {
		copyFields(item, spec, "clusterIP", "type", "ports", "selector", "sessionAffinity")
	}
	if status := getMap(obj, "status"); status != nil {
		copyFields(item, status, "loadBalancer", "conditions")
	}
	return item
}

func endpoints(u *unstructured.Unstructured, gvr schema.GroupVersionResource) map[string]interface{} {
	item := baseMeta(u, gvr)
	if subsets, ok := u.Object["subsets"]; ok {
		item["subsets"] = subsets
	}
	return item
}

func endpointslice(u *unstructured.Unstructured, gvr schema.GroupVersionResource) map[string]interface{} {
	item := baseMeta(u, gvr)
	obj := u.Object
	if at, ok := obj["addressType"]; ok {
		item["addressType"] = at
	}
	if eps, ok := obj["endpoints"]; ok {
		item["endpoints"] = eps
	}
	if ports, ok := obj["ports"]; ok {
		item["ports"] = ports
	}
	return item
}

func ingress(u *unstructured.Unstructured, gvr schema.GroupVersionResource) map[string]interface{} {
	item := baseMeta(u, gvr)
	obj := u.Object
	if spec := getMap(obj, "spec"); spec != nil {
		copyFields(item, spec, "ingressClassName", "rules")
	}
	if status := getMap(obj, "status"); status != nil {
		copyFields(item, status, "loadBalancer")
	}
	return item
}

func ingressclass(u *unstructured.Unstructured, gvr schema.GroupVersionResource) map[string]interface{} {
	item := baseMeta(u, gvr)
	if spec := getMap(u.Object, "spec"); spec != nil {
		copyFields(item, spec, "controller", "parameters")
	}
	return item
}

func gateway(u *unstructured.Unstructured, gvr schema.GroupVersionResource) map[string]interface{} {
	item := baseMeta(u, gvr)
	obj := u.Object
	if spec := getMap(obj, "spec"); spec != nil {
		copyFields(item, spec, "gatewayClassName", "addresses")
	}
	if status := getMap(obj, "status"); status != nil {
		copyFields(item, status, "conditions", "addresses")
	}
	return item
}

func gatewayclass(u *unstructured.Unstructured, gvr schema.GroupVersionResource) map[string]interface{} {
	item := baseMeta(u, gvr)
	if spec := getMap(u.Object, "spec"); spec != nil {
		copyFields(item, spec, "controllerName")
	}
	if status := getMap(u.Object, "status"); status != nil {
		copyFields(item, status, "conditions")
	}
	return item
}

func routeAdapter(u *unstructured.Unstructured, gvr schema.GroupVersionResource) map[string]interface{} {
	item := baseMeta(u, gvr)
	if spec := getMap(u.Object, "spec"); spec != nil {
		copyFields(item, spec, "hostnames")
	}
	return item
}
