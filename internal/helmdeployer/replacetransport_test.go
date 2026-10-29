package helmdeployer

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"
	"github.com/google/go-cmp/cmp"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

const (
	pvcURL = "https://example.com/api/v1/namespaces/ns/persistentvolumeclaims/data"
	// renderedPVC is a claim as Helm renders it, without the fields set on the
	// server.
	renderedPVC = `{"apiVersion":"v1","kind":"PersistentVolumeClaim","metadata":{"name":"data","namespace":"ns","labels":{"rev":"2"}},"spec":{"accessModes":["ReadWriteOnce"]}}`
	livePVC     = `{"apiVersion":"v1","kind":"PersistentVolumeClaim","metadata":{"name":"data","namespace":"ns","labels":{"rev":"1"},"resourceVersion":"7"},"spec":{"accessModes":["ReadWriteOnce"],"volumeName":"pv-1","storageClassName":"standard"},"status":{"phase":"Bound"}}`
	// preservedPVC is renderedPVC with the fields of pvcRule copied.
	preservedPVC = `{"apiVersion":"v1","kind":"PersistentVolumeClaim","metadata":{"name":"data","namespace":"ns","labels":{"rev":"2"}},"spec":{"accessModes":["ReadWriteOnce"],"volumeName":"pv-1","storageClassName":"standard"}}`
)

type ctxKey struct{}

var pvcRule = fleet.PreserveOnReplaceRule{
	APIVersion: "v1",
	Kind:       "PersistentVolumeClaim",
	Paths:      []fleet.PreservedPath{{Path: "/spec/volumeName"}, {Path: "/spec/storageClassName"}},
}

// fakeServer is an http.RoundTripper standing in for the API server. It
// answers GETs with get and records every request, with its body.
type fakeServer struct {
	mu       sync.Mutex
	get      func(*http.Request) reply
	requests []*http.Request
	bodies   []string
}

func (f *fakeServer) RoundTrip(req *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var body string
	if req.Body != nil {
		b, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		body = string(b)
	}
	f.requests = append(f.requests, req)
	f.bodies = append(f.bodies, body)
	if req.Method == http.MethodGet && f.get != nil {
		r := f.get(req)
		if r.err != nil {
			return nil, r.err
		}
		return respond(r.status, r.body), nil
	}
	return respond(http.StatusOK, body), nil
}

func (f *fakeServer) methods() []string {
	var m []string
	for _, r := range f.requests {
		m = append(m, r.Method)
	}
	return m
}

func respond(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// reply is the API server's answer to a GET.
type reply struct {
	status int
	body   string
	err    error
}

func liveObject(body string) func(*http.Request) reply {
	return func(*http.Request) reply { return reply{status: http.StatusOK, body: body} }
}

// logSink returns a logger which records Info messages, at any verbosity, and
// errors, with their key/values.
func logSink() (logr.Logger, *[]string) {
	var lines []string
	return funcr.New(func(prefix, args string) {
		lines = append(lines, prefix+" "+args)
	}, funcr.Options{Verbosity: 1}), &lines
}

func newTransport(t *testing.T, server *fakeServer, logger logr.Logger, entries ...fleet.PreserveOnReplaceRule) *replaceTransport {
	t.Helper()
	return &replaceTransport{next: server, rules: mustCompile(t, entries...), logger: logger}
}

func newRequest(t *testing.T, method, url, contentType, body string) *http.Request {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, url, r)
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return req
}

func send(t *testing.T, rt http.RoundTripper, req *http.Request) {
	t.Helper()
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
}

func TestReplaceTransportPreservesFields(t *testing.T) {
	server := &fakeServer{get: liveObject(livePVC)}
	rt := newTransport(t, server, logr.Discard(), pvcRule)

	req := newRequest(t, http.MethodPut, pvcURL+"?fieldManager=helm", "application/json", renderedPVC)
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("Impersonate-User", "system:serviceaccount:ns:sa")
	req.Header.Set("Impersonate-Group", "system:serviceaccounts")
	req.Header.Add("Impersonate-Group", "system:authenticated")
	ctx := context.WithValue(context.Background(), ctxKey{}, "marker")
	req = req.WithContext(ctx)
	send(t, rt, req)

	if diff := cmp.Diff([]string{http.MethodGet, http.MethodPut}, server.methods()); diff != "" {
		t.Fatalf("unexpected requests (-want +got):\n%s", diff)
	}

	get, put := server.requests[0], server.requests[1]
	if get.URL.String() != pvcURL {
		t.Errorf("GET %s, want %s", get.URL, pvcURL)
	}
	for _, h := range []string{"Authorization", "Impersonate-User", "Impersonate-Group"} {
		if diff := cmp.Diff(req.Header.Values(h), get.Header.Values(h)); diff != "" {
			t.Errorf("header %s not passed to the GET (-want +got):\n%s", h, diff)
		}
	}
	if get.Header.Get("Content-Type") != "" {
		t.Errorf("GET has a Content-Type header")
	}
	if get.Context().Value(ctxKey{}) != "marker" {
		t.Errorf("GET does not use the request's context")
	}

	if put.URL.String() != pvcURL+"?fieldManager=helm" {
		t.Errorf("PUT %s, want the original URL", put.URL)
	}
	if diff := cmp.Diff(obj(t, preservedPVC), obj(t, server.bodies[1])); diff != "" {
		t.Errorf("unexpected PUT body (-want +got):\n%s", diff)
	}
	if put.ContentLength != int64(len(server.bodies[1])) {
		t.Errorf("Content-Length %d, body has %d bytes", put.ContentLength, len(server.bodies[1]))
	}
	if put.GetBody == nil {
		t.Fatal("GetBody not set")
	}
	retry, err := put.GetBody()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(retry)
	if string(b) != server.bodies[1] {
		t.Errorf("GetBody returns %s, want the new body %s", b, server.bodies[1])
	}
	if put.Context().Value(ctxKey{}) != "marker" {
		t.Errorf("PUT does not use the request's context")
	}
}

func TestReplaceTransportPassesThrough(t *testing.T) {
	tests := map[string]struct {
		method      string
		url         string
		contentType string
		body        string
		entries     []fleet.PreserveOnReplaceRule
	}{
		"GET": {
			method: http.MethodGet, url: pvcURL, entries: []fleet.PreserveOnReplaceRule{pvcRule},
		},
		"PATCH": {
			method: http.MethodPatch, url: pvcURL, contentType: "application/strategic-merge-patch+json", body: `{"spec":{}}`,
			entries: []fleet.PreserveOnReplaceRule{pvcRule},
		},
		"POST": {
			method: http.MethodPost, url: "https://example.com/api/v1/namespaces/ns/persistentvolumeclaims", contentType: "application/json",
			body: renderedPVC, entries: []fleet.PreserveOnReplaceRule{pvcRule},
		},
		"protobuf": {
			method: http.MethodPut, url: pvcURL, contentType: "application/vnd.kubernetes.protobuf", body: "k8s\x00",
			entries: []fleet.PreserveOnReplaceRule{pvcRule},
		},
		"subresource": {
			method: http.MethodPut, url: pvcURL + "/status", contentType: "application/json", body: renderedPVC,
			entries: []fleet.PreserveOnReplaceRule{pvcRule},
		},
		"URL for another object": {
			method: http.MethodPut, url: "https://example.com/api/v1/namespaces/ns/persistentvolumeclaims/other", contentType: "application/json",
			body: renderedPVC, entries: []fleet.PreserveOnReplaceRule{pvcRule},
		},
		"URL in another namespace": {
			method: http.MethodPut, url: "https://example.com/api/v1/namespaces/other/persistentvolumeclaims/data", contentType: "application/json",
			body: renderedPVC, entries: []fleet.PreserveOnReplaceRule{pvcRule},
		},
		// objectPath misparses this URL as an object named "status" of
		// apiVersion "pods", which the body does not match.
		"subresource in a namespace named api": {
			method: http.MethodPut, url: "https://example.com/api/v1/namespaces/api/pods/status/status", contentType: "application/json",
			body:    `{"apiVersion":"v1","kind":"Pod","metadata":{"name":"status","namespace":"api"},"spec":{}}`,
			entries: []fleet.PreserveOnReplaceRule{{APIVersion: "v1", Kind: "Pod", Paths: []fleet.PreservedPath{{Path: "/spec/nodeName"}}}},
		},
		"body is not JSON": {
			method: http.MethodPut, url: pvcURL, contentType: "application/json", body: "{",
			entries: []fleet.PreserveOnReplaceRule{pvcRule},
		},
		"no matching rule": {
			method: http.MethodPut, url: pvcURL, contentType: "application/json", body: renderedPVC,
			entries: []fleet.PreserveOnReplaceRule{{APIVersion: "batch/v1", Kind: "Job", Paths: []fleet.PreservedPath{{Path: "/spec/selector"}}}},
		},
		"rule for another namespace": {
			method: http.MethodPut, url: pvcURL, contentType: "application/json", body: renderedPVC,
			entries: []fleet.PreserveOnReplaceRule{{APIVersion: "v1", Kind: "PersistentVolumeClaim", Namespace: "other", Paths: pvcRule.Paths}},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			server := &fakeServer{}
			if tt.method != http.MethodGet {
				server.get = func(*http.Request) reply {
					t.Error("unexpected GET of the live object")
					return reply{status: http.StatusOK, body: livePVC}
				}
			}
			rt := newTransport(t, server, logr.Discard(), tt.entries...)
			send(t, rt, newRequest(t, tt.method, tt.url, tt.contentType, tt.body))

			if diff := cmp.Diff([]string{tt.method}, server.methods()); diff != "" {
				t.Fatalf("unexpected requests (-want +got):\n%s", diff)
			}
			if server.bodies[0] != tt.body {
				t.Errorf("body changed to %s", server.bodies[0])
			}
		})
	}
}

func TestReplaceTransportSendsOriginalBodyOnErrors(t *testing.T) {
	tests := map[string]struct {
		get     func(*http.Request) reply
		wantLog string
	}{
		"object not found": {
			get: func(*http.Request) reply { return reply{status: http.StatusNotFound, body: `{"kind":"Status"}`} },
		},
		"forbidden": {
			get: func(*http.Request) reply {
				return reply{status: http.StatusForbidden, body: `{"kind":"Status","message":"forbidden"}`}
			},
			wantLog: "unexpected status",
		},
		"server error": {
			get:     func(*http.Request) reply { return reply{status: http.StatusInternalServerError, body: `oops`} },
			wantLog: "unexpected status",
		},
		"transport error": {
			get:     func(*http.Request) reply { return reply{err: errors.New("connection refused")} },
			wantLog: "connection refused",
		},
		"invalid live JSON": {
			get:     liveObject(`{"apiVersion":`),
			wantLog: "decoding the live object",
		},
		"live object is not an object": {
			get:     liveObject(`null`),
			wantLog: "decoding the live object",
		},
		"nothing to copy": {
			get: liveObject(`{"apiVersion":"v1","kind":"PersistentVolumeClaim","metadata":{"name":"data","namespace":"ns"},"spec":{}}`),
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			server := &fakeServer{get: tt.get}
			logger, lines := logSink()
			rt := newTransport(t, server, logger, pvcRule)
			send(t, rt, newRequest(t, http.MethodPut, pvcURL, "application/json", renderedPVC))

			if diff := cmp.Diff([]string{http.MethodGet, http.MethodPut}, server.methods()); diff != "" {
				t.Fatalf("unexpected requests (-want +got):\n%s", diff)
			}
			put := server.requests[1]
			if server.bodies[1] != renderedPVC {
				t.Errorf("body changed to %s", server.bodies[1])
			}
			if put.ContentLength != int64(len(renderedPVC)) {
				t.Errorf("Content-Length %d, want %d", put.ContentLength, len(renderedPVC))
			}
			if put.GetBody == nil {
				t.Fatal("GetBody not set")
			}
			retry, _ := put.GetBody()
			if b, _ := io.ReadAll(retry); string(b) != renderedPVC {
				t.Errorf("GetBody returns %s", b)
			}

			logged := strings.Join(*lines, "\n")
			if tt.wantLog == "" && logged != "" {
				t.Errorf("unexpected log: %s", logged)
			}
			if tt.wantLog != "" && !strings.Contains(logged, tt.wantLog) {
				t.Errorf("expected a log containing %q, got %q", tt.wantLog, logged)
			}
			if tt.wantLog != "" {
				for _, want := range []string{`"error"=`, `"kind"="PersistentVolumeClaim"`, `"namespace"="ns"`, `"name"="data"`} {
					if !strings.Contains(logged, want) {
						t.Errorf("expected an error log containing %s, got %q", want, logged)
					}
				}
			}
		})
	}
}

// A path which cannot be followed is logged, while the other paths are still
// copied.
func TestReplaceTransportLogsPathErrors(t *testing.T) {
	server := &fakeServer{get: liveObject(`{"apiVersion":"v1","kind":"PersistentVolumeClaim","metadata":{"name":"data","namespace":"ns"},"spec":{"volumeName":"pv-1","storageClassName":{"x":1}}}`)}
	logger, lines := logSink()
	rt := newTransport(t, server, logger, fleet.PreserveOnReplaceRule{
		APIVersion: "v1", Kind: "PersistentVolumeClaim",
		Paths: []fleet.PreservedPath{{Path: "/spec/volumeName"}, {Path: "/spec/storageClassName/x/y"}},
	})
	send(t, rt, newRequest(t, http.MethodPut, pvcURL, "application/json", renderedPVC))

	want := `{"apiVersion":"v1","kind":"PersistentVolumeClaim","metadata":{"name":"data","namespace":"ns","labels":{"rev":"2"}},"spec":{"accessModes":["ReadWriteOnce"],"volumeName":"pv-1"}}`
	if diff := cmp.Diff(obj(t, want), obj(t, server.bodies[1])); diff != "" {
		t.Errorf("unexpected PUT body (-want +got):\n%s", diff)
	}
	logged := strings.Join(*lines, "\n")
	for _, want := range []string{`/spec/storageClassName/x/y`, `"error"=`, `"name"="data"`} {
		if !strings.Contains(logged, want) {
			t.Errorf("expected an error log containing %s, got %q", want, logged)
		}
	}
}

func TestObjectPath(t *testing.T) {
	tests := map[string]struct {
		path string
		want objectSegments
		ok   bool
	}{
		"core namespaced":           {"/api/v1/namespaces/ns/persistentvolumeclaims/data", objectSegments{"v1", "ns", "data"}, true},
		"core cluster scoped":       {"/api/v1/persistentvolumes/pv", objectSegments{"v1", "", "pv"}, true},
		"namespace":                 {"/api/v1/namespaces/ns", objectSegments{"v1", "", "ns"}, true},
		"group namespaced":          {"/apis/batch/v1/namespaces/ns/jobs/job", objectSegments{"batch/v1", "ns", "job"}, true},
		"group cluster scoped":      {"/apis/rbac.authorization.k8s.io/v1/clusterroles/r", objectSegments{"rbac.authorization.k8s.io/v1", "", "r"}, true},
		"with a prefix":             {"/k8s/clusters/c-1/apis/batch/v1/namespaces/ns/jobs/job", objectSegments{"batch/v1", "ns", "job"}, true},
		"core with a prefix":        {"/k8s/clusters/c-1/api/v1/namespaces/ns/pods/p", objectSegments{"v1", "ns", "p"}, true},
		"trailing slash":            {"/api/v1/namespaces/ns/pods/p/", objectSegments{"v1", "ns", "p"}, true},
		"group subresource":         {"/apis/apps/v1/namespaces/ns/deployments/d/scale", objectSegments{}, false},
		"core collection":           {"/api/v1/namespaces/ns/pods", objectSegments{}, false},
		"group collection":          {"/apis/batch/v1/namespaces/ns/jobs", objectSegments{}, false},
		"version":                   {"/version", objectSegments{}, false},
		"empty":                     {"", objectSegments{}, false},
		"core cluster subresource?": {"/api/v1/nodes/n/status", objectSegments{}, false},
		// Misparsed, as the path is matched from its end; preserve's check
		// of the body against the URL makes this harmless.
		"core subresource in a namespace named api":  {"/api/v1/namespaces/api/pods/p/status", objectSegments{"pods", "", "status"}, true},
		"group subresource in a namespace named api": {"/apis/batch/v1/namespaces/api/jobs/j/status", objectSegments{"jobs", "", "status"}, true},
		"core subresource in a namespace named apis": {"/api/v1/namespaces/apis/pods/p/status", objectSegments{}, false},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, ok := objectPath(tt.path)
			if ok != tt.ok || got != tt.want {
				t.Errorf("objectPath(%q) = %+v, %v, want %+v, %v", tt.path, got, ok, tt.want, tt.ok)
			}
		})
	}
}

// staticGetter is a RESTClientGetter with a fixed config.
type staticGetter struct {
	genericclioptions.RESTClientGetter
	cfg *rest.Config
}

func (g staticGetter) ToRESTConfig() (*rest.Config, error) { return g.cfg, nil }

// Through client-go, the GET of the live object carries the credentials and
// impersonation headers client-go adds, the PUT carries the new body, and the
// config of the wrapped getter is left untouched.
func TestPreservingGetter(t *testing.T) {
	var (
		mu       sync.Mutex
		requests []string
		putBody  []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		requests = append(requests, r.Method+" "+r.Header.Get("Authorization")+" "+r.Header.Get("Impersonate-User"))
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(livePVC))
		case http.MethodPut:
			putBody, _ = io.ReadAll(r.Body)
			_, _ = w.Write(putBody)
		}
	}))
	defer srv.Close()

	base := &rest.Config{
		Host:        srv.URL,
		BearerToken: "token",
		Impersonate: rest.ImpersonationConfig{UserName: "system:serviceaccount:ns:sa"},
	}
	getter := newPreservingGetter(staticGetter{cfg: base}, mustCompile(t, pvcRule), logr.Discard())
	cfg, err := getter.ToRESTConfig()
	if err != nil {
		t.Fatal(err)
	}
	if base.WrapTransport != nil {
		t.Error("the wrapped getter's config was modified")
	}

	client, err := dynamic.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	u := &unstructured.Unstructured{}
	if err := u.UnmarshalJSON([]byte(renderedPVC)); err != nil {
		t.Fatal(err)
	}
	_, err = client.Resource(schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumeclaims"}).
		Namespace("ns").Update(context.Background(), u, metav1.UpdateOptions{FieldManager: "helm"})
	if err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	want := []string{
		"GET Bearer token system:serviceaccount:ns:sa",
		"PUT Bearer token system:serviceaccount:ns:sa",
	}
	if diff := cmp.Diff(want, requests); diff != "" {
		t.Errorf("unexpected requests (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(obj(t, preservedPVC), obj(t, string(bytes.TrimSpace(putBody)))); diff != "" {
		t.Errorf("unexpected PUT body (-want +got):\n%s", diff)
	}
}
