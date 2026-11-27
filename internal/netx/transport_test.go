package netx

import (
	"errors"
	"net/http"
	"testing"
)

type fakeRT struct{}

func (fakeRT) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("not used")
}

func TestNewTransport_Default(t *testing.T) {
	orig := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = orig })
	def := &http.Transport{MaxIdleConns: 7}
	http.DefaultTransport = def

	tr, err := NewTransport(TLSConfig{InsecureSkipVerify: true})
	if err != nil {
		t.Fatal(err)
	}

	if tr == def {
		t.Fatal("expected a copy, got http.DefaultTransport itself")
	}
	if tr.MaxIdleConns != 7 {
		t.Fatalf("expected MaxIdleConns to be cloned from http.DefaultTransport, got %d", tr.MaxIdleConns)
	}
	if !tr.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("expected InsecureSkipVerify to be true")
	}
	if def.TLSClientConfig != nil && def.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("expected http.DefaultTransport to be left unchanged")
	}
}

func TestNewTransport_Another_Component_Replaced_Global_Default(t *testing.T) {
	orig := http.DefaultTransport
	http.DefaultTransport = &fakeRT{}
	t.Cleanup(func() { http.DefaultTransport = orig })

	tr, err := NewTransport(TLSConfig{InsecureSkipVerify: true})
	if err != nil {
		t.Fatal(err)
	}

	if tr.Proxy == nil {
		t.Fatal("expected Proxy to be set")
	}
	if !tr.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("expected InsecureSkipVerify to be true")
	}
}
