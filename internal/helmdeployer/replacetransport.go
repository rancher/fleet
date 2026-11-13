package helmdeployer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-logr/logr"

	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/rest"
)

// preservingGetter is a RESTClientGetter whose REST config sends requests
// through a replaceTransport. It is only given to the kube client Helm applies
// objects with, so that a forced replace, which Helm sends as a PUT of the
// rendered object, keeps the fields listed in helm.preserveOnReplace.
type preservingGetter struct {
	genericclioptions.RESTClientGetter
	rules  []preserveRule
	logger logr.Logger
}

func newPreservingGetter(getter genericclioptions.RESTClientGetter, rules []preserveRule, logger logr.Logger) *preservingGetter {
	return &preservingGetter{RESTClientGetter: getter, rules: rules, logger: logger}
}

// ToRESTConfig returns a copy of the wrapped getter's config, with a
// replaceTransport. The wrapper sits below client-go's authentication and
// impersonation wrappers, so it sees requests with their final headers.
func (g *preservingGetter) ToRESTConfig() (*rest.Config, error) {
	cfg, err := g.RESTClientGetter.ToRESTConfig()
	if err != nil {
		return nil, err
	}
	cfg = rest.CopyConfig(cfg)
	cfg.Wrap(func(rt http.RoundTripper) http.RoundTripper {
		return &replaceTransport{next: rt, rules: g.rules, logger: g.logger}
	})
	return cfg, nil
}

// replaceTransport rewrites the body of PUT requests for objects matched by a
// helm.preserveOnReplace rule: it reads the live object and copies the rule's
// paths into the body (see preserveLiveFields). Every other request, and any
// request it fails to rewrite, is sent unchanged.
type replaceTransport struct {
	next   http.RoundTripper
	rules  []preserveRule
	logger logr.Logger
}

func (t *replaceTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodPut || req.Body == nil || req.Body == http.NoBody {
		return t.next.RoundTrip(req)
	}
	segments, ok := objectPath(req.URL.Path)
	if !ok {
		return t.next.RoundTrip(req)
	}
	// Helm's kube client sends JSON. Should that change, e.g. to CBOR, the
	// rules would stop working, with only these logs to tell. The agent
	// integration tests of helm.preserveOnReplace, which replace objects
	// through Helm against an API server, would then fail.
	if contentType := req.Header.Get("Content-Type"); !isJSON(contentType) {
		t.logger.V(1).Info("Not preserving fields on replace, the body is not JSON", "url", req.URL.Path, "contentType", contentType)
		return t.next.RoundTrip(req)
	}
	if encoding := req.Header.Get("Content-Encoding"); encoding != "" {
		t.logger.V(1).Info("Not preserving fields on replace, the body is encoded", "url", req.URL.Path, "contentEncoding", encoding)
		return t.next.RoundTrip(req)
	}

	body, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	if err != nil {
		return nil, err
	}

	if newBody := t.preserve(req, segments, body); newBody != nil {
		return t.next.RoundTrip(withBody(req, newBody))
	}

	return t.next.RoundTrip(withBody(req, body))
}

// preserve returns the body of a PUT of the object at segments with the fields
// of the matching rules copied from the live object, or nil if the body is to
// be sent unchanged. It logs the reason when a matching rule cannot be
// applied: the API server's response to the unchanged PUT, often an error
// about an immutable field, would not tell.
//
// The body is only rewritten if its apiVersion, name and namespace are those
// of the URL. This cross-check is what keeps a URL objectPath misparses, such
// as a subresource in a namespace named "api", from being acted on.
func (t *replaceTransport) preserve(req *http.Request, segments objectSegments, body []byte) []byte {
	obj, ok := decodeObject(body)
	if !ok {
		return nil // not an object, not ours to look at
	}
	apiVersion, _ := obj["apiVersion"].(string)
	kind, _ := obj["kind"].(string)
	metadata, _ := obj["metadata"].(map[string]any)
	name, _ := metadata["name"].(string)
	namespace, _ := metadata["namespace"].(string)
	if apiVersion != segments.apiVersion || name != segments.name ||
		(namespace != "" && namespace != segments.namespace) {
		return nil // the URL does not address this object
	}
	namespace = segments.namespace

	paths := preservedPaths(t.rules, apiVersion, kind, namespace, name)
	if len(paths) == 0 {
		return nil
	}
	logger := t.logger.WithValues("apiVersion", apiVersion, "kind", kind, "namespace", namespace, "name", name)

	live, err := t.getLive(req)
	if err != nil {
		logger.Error(err, "Cannot read the live object to preserve fields on replace, sending the object as rendered")
		return nil
	}
	if live == nil {
		return nil // Helm will create it
	}

	changed, err := preserveLiveFields(logger, obj, live, paths)
	if err != nil {
		logger.Error(err, "Cannot preserve some fields on replace, the other fields are preserved")
	}
	if !changed {
		return nil
	}

	newBody, err := json.Marshal(obj)
	if err != nil {
		logger.Error(err, "Cannot encode the object with the preserved fields, sending the object as rendered")
		return nil
	}
	logger.V(1).Info("Preserving fields from the live object on replace")

	return newBody
}

// getLive reads the object a PUT request addresses, with the request's
// headers, which carry its credentials and impersonation, and context. It
// returns nil if the object does not exist.
//
// The GET is sent straight to the next transport, so it deliberately bypasses
// client-go's rate limiter and warning handler: it is one extra request per
// replaced object, made while client-go is already sending the PUT, which has
// gone through the rate limiter.
//
// The PUT carries the resourceVersion of Helm's own earlier GET, not of this
// one. If the object changes in between, the PUT fails with a conflict, as it
// would have without this GET, and the deployment is retried.
func (t *replaceTransport) getLive(put *http.Request) (map[string]any, error) {
	// The URL is the one client-go built for the PUT.
	get, err := http.NewRequestWithContext(put.Context(), http.MethodGet, put.URL.String(), nil) //nolint:gosec // see above
	if err != nil {
		return nil, err
	}
	get.Header = put.Header.Clone()
	for _, h := range []string{"Content-Type", "Content-Length", "Content-Encoding"} {
		get.Header.Del(h)
	}
	get.Header.Set("Accept", "application/json")
	// Drop the PUT's parameters, such as fieldManager, which a GET does not
	// accept.
	get.URL.RawQuery = ""

	resp, err := t.next.RoundTrip(get)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, nil
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("unexpected status %s: %s", resp.Status, truncate(data, 512))
	}

	live, ok := decodeObject(data)
	if !ok {
		return nil, fmt.Errorf("decoding the live object: not a JSON object: %s", truncate(data, 512))
	}

	return live, nil
}

// withBody returns a copy of req which sends body.
func withBody(req *http.Request, body []byte) *http.Request {
	r := req.Clone(req.Context())
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	// client-go retries a request with GetBody.
	r.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	if r.Header.Get("Content-Length") != "" {
		r.Header.Set("Content-Length", strconv.Itoa(len(body)))
	}
	return r
}

// decodeJSON decodes data into v, keeping numbers as written, so that large
// integers survive being encoded again.
func decodeJSON(data []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err := d.Decode(v); err != nil {
		return err
	}
	if d.More() {
		return errors.New("unexpected data after the JSON value")
	}
	return nil
}

// decodeObject decodes data as a JSON object, reporting whether it is one.
func decodeObject(data []byte) (map[string]any, bool) {
	var obj map[string]any
	if err := decodeJSON(data, &obj); err != nil || obj == nil {
		return nil, false
	}
	return obj, true
}

func isJSON(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	return err == nil && mediaType == "application/json"
}

func truncate(data []byte, n int) string {
	if len(data) > n {
		return string(data[:n]) + "..."
	}
	return string(data)
}

// objectSegments identifies the object a request URL addresses.
type objectSegments struct {
	apiVersion string
	namespace  string
	name       string
}

// objectPath parses the path of a request for an object, without subresource:
// /api/{version}[/namespaces/{namespace}]/{resource}/{name} or
// /apis/{group}/{version}[/namespaces/{namespace}]/{resource}/{name}. The path
// may have a prefix, e.g. when the API server is reached through a proxy.
//
// As the path is matched from its end, a subresource of an object in a
// namespace named "api" is misparsed, e.g. /api/v1/namespaces/api/pods/p/status
// as an object named "status" of apiVersion "pods". This is harmless: preserve
// checks the URL against the body, whose apiVersion cannot match.
func objectPath(path string) (objectSegments, bool) {
	s := strings.Split(strings.Trim(path, "/"), "/")
	n := len(s)
	at := func(length int) []string {
		if n < length {
			return nil
		}
		return s[n-length:]
	}

	// The longest form first: a prefix may end in "api" or "apis".
	if p := at(7); p != nil && p[0] == "apis" && p[3] == "namespaces" {
		return objectSegments{apiVersion: p[1] + "/" + p[2], namespace: p[4], name: p[6]}, true
	}
	if p := at(6); p != nil && p[0] == "api" && p[2] == "namespaces" {
		return objectSegments{apiVersion: p[1], namespace: p[3], name: p[5]}, true
	}
	if p := at(5); p != nil && p[0] == "apis" {
		return objectSegments{apiVersion: p[1] + "/" + p[2], name: p[4]}, true
	}
	if p := at(4); p != nil && p[0] == "api" {
		return objectSegments{apiVersion: p[1], name: p[3]}, true
	}

	return objectSegments{}, false
}
