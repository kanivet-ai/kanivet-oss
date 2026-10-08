package watcher

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer/protobuf"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
)

// protoList encodes n items of the kind as the apiserver sends a protobuf list.
func protoList(t *testing.T, kind typedKind, n int, rv string) ([]byte, []runtime.Object) {
	t.Helper()
	gvks, _, err := scheme.Scheme.ObjectKinds(kind.item())
	if err != nil {
		t.Fatal(err)
	}
	list, err := scheme.Scheme.New(gvks[0].GroupVersion().WithKind(gvks[0].Kind + "List"))
	if err != nil {
		t.Fatal(err)
	}
	items := make([]runtime.Object, n)
	for i := range items {
		item := kind.item()
		acc, err := meta.Accessor(item)
		if err != nil {
			t.Fatal(err)
		}
		acc.SetName(fmt.Sprintf("item-%d", i))
		acc.SetNamespace("ns")
		acc.SetResourceVersion(fmt.Sprint(100 + i))
		acc.SetLabels(map[string]string{"n": fmt.Sprint(i)})
		items[i] = item
	}
	if err := meta.SetList(list, items); err != nil {
		t.Fatal(err)
	}
	lm, err := meta.ListAccessor(list)
	if err != nil {
		t.Fatal(err)
	}
	lm.SetResourceVersion(rv)
	var buf bytes.Buffer
	if err := protobuf.NewSerializer(scheme.Scheme, scheme.Scheme).Encode(list, &buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes(), items
}

func collect(into *[]runtime.Object) func(runtime.Object) error {
	return func(obj runtime.Object) error {
		*into = append(*into, obj)
		return nil
	}
}

// Every kind in the table decodes from a stream to the objects that went in:
// this is what pins the list layout (metadata 1, items 2) for each of them.
func TestProtoListStreamDecodesEveryTypedKind(t *testing.T) {
	for group, kinds := range typedKinds {
		for resource, kind := range kinds {
			t.Run(group+"/"+resource, func(t *testing.T) {
				data, want := protoList(t, kind, 3, "42")
				var got []runtime.Object
				// One byte at a time: an item must survive any split of the stream.
				rv, err := decodeProtoList(iotestOneByte(data), kind.item, collect(&got))
				if err != nil {
					t.Fatal(err)
				}
				if rv != "42" {
					t.Errorf("resourceVersion = %q, want 42", rv)
				}
				if len(got) != len(want) {
					t.Fatalf("got %d items, want %d", len(got), len(want))
				}
				for i := range want {
					if !apiequality.Semantic.DeepEqual(got[i], want[i]) {
						t.Errorf("item %d differs:\n got %#v\nwant %#v", i, got[i], want[i])
					}
				}
			})
		}
	}
}

type oneByteReader struct{ r io.Reader }

func (o oneByteReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return o.r.Read(p[:1])
}

func iotestOneByte(data []byte) io.Reader { return oneByteReader{bytes.NewReader(data)} }

func TestProtoListStreamEmptyList(t *testing.T) {
	kind := typedKinds[""]["pods"]
	data, _ := protoList(t, kind, 0, "7")
	var got []runtime.Object
	rv, err := decodeProtoList(bytes.NewReader(data), kind.item, collect(&got))
	if err != nil || rv != "7" || len(got) != 0 {
		t.Fatalf("rv=%q items=%d err=%v", rv, len(got), err)
	}
}

// A stream cut short is the transport's failure; only a body that is not the
// expected protobuf asks for the whole-body decode.
func TestProtoListStreamErrors(t *testing.T) {
	kind := typedKinds[""]["pods"]
	data, _ := protoList(t, kind, 3, "42")

	// The envelope ends with two empty fields (4 bytes) after the list: a
	// stream cut there holds the whole list.
	for cut := 1; cut < len(data)-4; cut += 7 {
		_, err := decodeProtoList(bytes.NewReader(data[:cut]), kind.item, func(runtime.Object) error { return nil })
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("cut at %d: err = %v, want unexpected EOF", cut, err)
		}
	}

	for name, body := range map[string][]byte{
		"json":      []byte(`{"kind":"PodList","items":[]}`),
		"bad magic": append([]byte("k9s\x00"), data[4:]...),
		"wire type": append([]byte(protoMagic), 0x08, 0x01),
		"corrupt item": func() []byte {
			d := bytes.Clone(data)
			// Break the first item's own first tag (after envelope and metadata).
			i := bytes.Index(d, []byte("item-0"))
			d[i-2] = 0xff
			return d
		}(),
	} {
		n := 0
		_, err := decodeProtoList(bytes.NewReader(body), kind.item, func(runtime.Object) error { n++; return nil })
		if !errors.Is(err, errListFormat) {
			t.Errorf("%s: err = %v, want a format error", name, err)
		}
		if name != "corrupt item" && n != 0 {
			t.Errorf("%s: %d items handed over before the format error", name, n)
		}
	}

	stop := errors.New("stop")
	n := 0
	_, err := decodeProtoList(bytes.NewReader(data), kind.item, func(runtime.Object) error { n++; return stop })
	if err != stop || n != 1 {
		t.Fatalf("consumer error: err=%v after %d items, want it back after 1", err, n)
	}
}

// wholeJSONList is the decode the dynamic client does, the reference the
// stream has to match.
func wholeJSONList(t *testing.T, body string) *unstructured.UnstructuredList {
	t.Helper()
	list := &unstructured.UnstructuredList{}
	if _, _, err := unstructured.UnstructuredJSONScheme.Decode([]byte(body), nil, list); err != nil {
		t.Fatal(err)
	}
	return list
}

func TestJSONListStreamMatchesWholeDecode(t *testing.T) {
	for name, body := range map[string]string{
		// A built-in kind: the list's kind first, items without one.
		"built-in": `{"kind":"LeaseList","apiVersion":"coordination.k8s.io/v1","metadata":{"resourceVersion":"900","remainingItemCount":3},
			"items":[{"metadata":{"name":"a","namespace":"ns","generation":4},"spec":{"leaseDurationSeconds":15,"ratio":0.5,"big":9007199254740993}},
			         {"metadata":{"name":"b","namespace":"ns"},"spec":{"holders":["x","y"],"on":true,"none":null}}]}`,
		// A custom resource list as older apiservers send it: keys sorted, so
		// items come before the list's kind and metadata.
		"custom sorted": `{"apiVersion":"example.com/v1","items":[
			{"apiVersion":"example.com/v1","kind":"Widget","metadata":{"name":"w1","resourceVersion":"5"},"spec":{"size":3,"nested":{"deep":[1,2,{"x":"y"}]}}},
			{"apiVersion":"example.com/v1","kind":"Widget","metadata":{"name":"w2"},"status":{"ready":false}}],
			"kind":"WidgetList","metadata":{"continue":"","resourceVersion":"77"}}`,
		"empty":      `{"kind":"WidgetList","apiVersion":"example.com/v1","metadata":{"resourceVersion":"1"},"items":[]}`,
		"null items": `{"kind":"WidgetList","apiVersion":"example.com/v1","metadata":{"resourceVersion":"2"},"items":null}`,
		"no items":   `{"kind":"WidgetList","apiVersion":"example.com/v1","metadata":{"resourceVersion":"3"}}`,
		"extra keys": `{"kind":"WidgetList","extra":{"a":[1,{"b":2}]},"apiVersion":"example.com/v1","items":[{"metadata":{"name":"z"}}],"metadata":{"resourceVersion":"4"},"tail":"x"}`,
	} {
		t.Run(name, func(t *testing.T) {
			want := wholeJSONList(t, body)
			var got []runtime.Object
			rv, err := decodeJSONList(oneByteReader{strings.NewReader(body)}, collect(&got))
			if err != nil {
				t.Fatal(err)
			}
			if rv != want.GetResourceVersion() {
				t.Errorf("resourceVersion = %q, want %q", rv, want.GetResourceVersion())
			}
			if len(got) != len(want.Items) {
				t.Fatalf("got %d items, want %d", len(got), len(want.Items))
			}
			for i := range want.Items {
				if g := got[i].(*unstructured.Unstructured).Object; !reflect.DeepEqual(g, want.Items[i].Object) {
					t.Errorf("item %d differs:\n got %#v\nwant %#v", i, g, want.Items[i].Object)
				}
			}
		})
	}
}

func TestJSONListStreamErrors(t *testing.T) {
	full := `{"kind":"WidgetList","apiVersion":"example.com/v1","metadata":{"resourceVersion":"1"},"items":[{"metadata":{"name":"a"}},{"metadata":{"name":"b"}}]}`
	for cut := 1; cut < len(full); cut += 5 {
		_, err := decodeJSONList(strings.NewReader(full[:cut]), func(runtime.Object) error { return nil })
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("cut at %d (%q): err = %v, want unexpected EOF", cut, full[:cut], err)
		}
	}

	for name, body := range map[string]string{
		"not json":   "k8s\x00\x0a\x02",
		"array":      `[{"metadata":{"name":"a"}}]`,
		"items type": `{"kind":"WidgetList","items":{"a":1}}`,
		"item type":  `{"kind":"WidgetList","items":[{"metadata":{"name":"a"}},7]}`,
		// Nothing says what kind these items are until after they are read.
		"kind after kindless items": `{"apiVersion":"v1","items":[{"metadata":{"name":"a"}}],"kind":"PodList"}`,
	} {
		n := 0
		_, err := decodeJSONList(strings.NewReader(body), func(runtime.Object) error { n++; return nil })
		if !errors.Is(err, errListFormat) {
			t.Errorf("%s: err = %v, want a format error", name, err)
		}
		if name == "kind after kindless items" && n != 0 {
			t.Errorf("%s: %d items handed over before the format error", name, n)
		}
	}

	stop := errors.New("stop")
	n := 0
	_, err := decodeJSONList(strings.NewReader(full), func(runtime.Object) error { n++; return stop })
	if err != stop || n != 1 {
		t.Fatalf("consumer error: err=%v after %d items, want it back after 1", err, n)
	}
}

// listServer answers list requests the way an apiserver does, gzip included,
// and records what was asked.
type listServer struct {
	*httptest.Server
	requests []string
	serve    func(w http.ResponseWriter, r *http.Request)
}

func newListServer(t *testing.T, serve func(w http.ResponseWriter, r *http.Request)) *listServer {
	t.Helper()
	ls := &listServer{serve: serve}
	ls.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ls.requests = append(ls.requests, r.URL.RequestURI()+" accept="+r.Header.Get("Accept"))
		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			w.Header().Set("Content-Encoding", "gzip")
			gz := gzip.NewWriter(w)
			defer gz.Close()
			w = gzipResponse{w, gz}
		}
		ls.serve(w, r)
	}))
	t.Cleanup(ls.Close)
	return ls
}

type gzipResponse struct {
	http.ResponseWriter
	gz *gzip.Writer
}

func (g gzipResponse) Write(p []byte) (int, error) { return g.gz.Write(p) }

func (ls *listServer) clientset(t *testing.T) kubernetes.Interface {
	t.Helper()
	cs, err := kubernetes.NewForConfig(&rest.Config{Host: ls.URL, ContentConfig: rest.ContentConfig{
		ContentType:        "application/vnd.kubernetes.protobuf",
		AcceptContentTypes: "application/vnd.kubernetes.protobuf,application/json",
	}})
	if err != nil {
		t.Fatal(err)
	}
	return cs
}

var podsGVR = schema.GroupVersionResource{Version: "v1", Resource: "pods"}

func podNames(t *testing.T, objs []runtime.Object) []string {
	t.Helper()
	names := make([]string, len(objs))
	for i, obj := range objs {
		pod, ok := obj.(*corev1.Pod)
		if !ok {
			t.Fatalf("item %d is %T, want a typed pod", i, obj)
		}
		names[i] = pod.Name
	}
	return names
}

func TestTypedListerStreamsProtobuf(t *testing.T) {
	data, _ := protoList(t, typedKinds[""]["pods"], 3, "42")
	srv := newListServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.kubernetes.protobuf")
		w.Write(data)
	})
	lister := newTypedLister(srv.clientset(t), podsGVR)

	var got []runtime.Object
	rv, err := lister.(itemLister).ListEach(context.Background(), metav1.ListOptions{ResourceVersion: "0"}, collect(&got))
	if err != nil {
		t.Fatal(err)
	}
	if names := podNames(t, got); rv != "42" || !reflect.DeepEqual(names, []string{"item-0", "item-1", "item-2"}) {
		t.Fatalf("rv=%q names=%v", rv, names)
	}

	got = nil
	if _, err := lister.Namespace("team-a").(itemLister).ListEach(context.Background(), metav1.ListOptions{ResourceVersion: "0"}, collect(&got)); err != nil {
		t.Fatal(err)
	}
	want := []string{"/api/v1/pods?resourceVersion=0", "/api/v1/namespaces/team-a/pods?resourceVersion=0"}
	for i, w := range want {
		if !strings.HasPrefix(srv.requests[i], w+" ") {
			t.Errorf("request %d = %q, want %q", i, srv.requests[i], w)
		}
	}
	if len(srv.requests) != len(want) {
		t.Errorf("%d requests, want %d: %v", len(srv.requests), len(want), srv.requests)
	}
}

// A cluster-scoped kind is never asked for under a namespace.
func TestTypedListerStreamIgnoresNamespaceForClusterScopedKinds(t *testing.T) {
	data, _ := protoList(t, typedKinds[""]["nodes"], 1, "1")
	srv := newListServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.kubernetes.protobuf")
		w.Write(data)
	})
	lister := newTypedLister(srv.clientset(t), schema.GroupVersionResource{Version: "v1", Resource: "nodes"}).Namespace("ns")
	var got []runtime.Object
	if _, err := lister.(itemLister).ListEach(context.Background(), metav1.ListOptions{}, collect(&got)); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.HasPrefix(srv.requests[0], "/api/v1/nodes ") {
		t.Fatalf("items=%d requests=%v", len(got), srv.requests)
	}
}

// A server that answers JSON all the same still lists: the stream is given up
// before anything is handed over and the list is decoded whole.
func TestTypedListerFallsBackWhenTheStreamIsNotProtobuf(t *testing.T) {
	srv := newListServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"kind":"PodList","apiVersion":"v1","metadata":{"resourceVersion":"9"},"items":[{"metadata":{"name":"a"}},{"metadata":{"name":"b"}}]}`)
	})
	var got []runtime.Object
	rv, err := newTypedLister(srv.clientset(t), podsGVR).(itemLister).ListEach(context.Background(), metav1.ListOptions{ResourceVersion: "0"}, collect(&got))
	if err != nil {
		t.Fatal(err)
	}
	if names := podNames(t, got); rv != "9" || !reflect.DeepEqual(names, []string{"a", "b"}) {
		t.Fatalf("rv=%q names=%v", rv, names)
	}
}

func TestTypedListerStreamReportsTheServerError(t *testing.T) {
	srv := newListServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		io.WriteString(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","message":"pods is forbidden: User \"u\" cannot list resource \"pods\"","reason":"Forbidden","code":403}`)
	})
	n := 0
	_, err := newTypedLister(srv.clientset(t), podsGVR).(itemLister).ListEach(context.Background(), metav1.ListOptions{}, func(runtime.Object) error { n++; return nil })
	if err == nil || !strings.Contains(err.Error(), "forbidden") || n != 0 {
		t.Fatalf("err=%v items=%d, want the forbidden error and no items", err, n)
	}
	if len(srv.requests) != 1 {
		t.Errorf("%d requests, want 1: a server error is not retried as a whole list", len(srv.requests))
	}
}

func TestDynamicListerStreamsJSON(t *testing.T) {
	gvr := schema.GroupVersionResource{Group: "example.com", Version: "v1", Resource: "widgets"}
	body := `{"apiVersion":"example.com/v1","items":[
		{"apiVersion":"example.com/v1","kind":"Widget","metadata":{"name":"w1","namespace":"ns"},"spec":{"size":3}},
		{"apiVersion":"example.com/v1","kind":"Widget","metadata":{"name":"w2","namespace":"ns"}}],
		"kind":"WidgetList","metadata":{"resourceVersion":"77"}}`
	srv := newListServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, body)
	})
	cs := srv.clientset(t)
	dyn, err := dynamic.NewForConfig(&rest.Config{Host: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	lister := newStreamingDynamicLister(dyn.Resource(gvr), cs.CoreV1().RESTClient(), gvr)

	var got []runtime.Object
	rv, err := lister.Namespace("ns").(itemLister).ListEach(context.Background(), metav1.ListOptions{ResourceVersion: "0"}, collect(&got))
	if err != nil {
		t.Fatal(err)
	}
	want := wholeJSONList(t, body)
	if rv != "77" || len(got) != 2 {
		t.Fatalf("rv=%q items=%d", rv, len(got))
	}
	for i := range got {
		if g := got[i].(*unstructured.Unstructured).Object; !reflect.DeepEqual(g, want.Items[i].Object) {
			t.Errorf("item %d differs:\n got %#v\nwant %#v", i, g, want.Items[i].Object)
		}
	}
	if w := "/apis/example.com/v1/namespaces/ns/widgets?resourceVersion=0 accept=application/json"; len(srv.requests) != 1 || srv.requests[0] != w {
		t.Errorf("requests = %v, want [%s]", srv.requests, w)
	}
}

// Without a REST client (a fake clientset) both listers still list item by
// item, from the whole list.
func TestListersListEachWithoutAStream(t *testing.T) {
	gvr := schema.GroupVersionResource{Group: "example.com", Version: "v1", Resource: "widgets"}
	body := `{"apiVersion":"example.com/v1","kind":"WidgetList","metadata":{"resourceVersion":"5"},"items":[{"apiVersion":"example.com/v1","kind":"Widget","metadata":{"name":"w1"}}]}`
	srv := newListServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, body)
	})
	dyn, err := dynamic.NewForConfig(&rest.Config{Host: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	var got []runtime.Object
	rv, err := newDynamicLister(dyn.Resource(gvr)).(itemLister).ListEach(context.Background(), metav1.ListOptions{}, collect(&got))
	if err != nil || rv != "5" || len(got) != 1 {
		t.Fatalf("rv=%q items=%d err=%v", rv, len(got), err)
	}
}
