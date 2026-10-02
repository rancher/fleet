package httputils

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUserAgentTransportStampsHeader(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("User-Agent")
	}))
	defer srv.Close()

	client := &http.Client{Transport: UserAgentTransport{Next: http.DefaultTransport}}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()

	if !strings.HasPrefix(got, "fleet/") {
		t.Errorf("User-Agent = %q, want it to start with %q", got, "fleet/")
	}
	if got != UserAgent() {
		t.Errorf("User-Agent = %q, want %q", got, UserAgent())
	}
}

func TestUserAgentTransportKeepsCallersHeader(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("User-Agent")
	}))
	defer srv.Close()

	client := &http.Client{Transport: UserAgentTransport{Next: http.DefaultTransport}}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("User-Agent", "helm/3.19.0")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()

	if got != "helm/3.19.0" {
		t.Errorf("User-Agent = %q, want the caller's %q to be kept", got, "helm/3.19.0")
	}
}

func TestUserAgentTransportDoesNotModifyCallersRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	client := &http.Client{Transport: UserAgentTransport{Next: http.DefaultTransport}}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()

	if ua := req.Header.Get("User-Agent"); ua != "" {
		t.Errorf("caller's request was modified: User-Agent = %q, want it untouched", ua)
	}
}

func TestUserAgentTransportNilNextUsesDefault(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("User-Agent")
	}))
	defer srv.Close()

	client := &http.Client{Transport: UserAgentTransport{}}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()

	if got != UserAgent() {
		t.Errorf("User-Agent = %q, want %q", got, UserAgent())
	}
}
