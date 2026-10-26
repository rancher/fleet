package git

import (
	"context"
	"strings"
	"testing"

	"github.com/go-logr/logr"
)

type capturingErrorSink struct {
	errors []string
}

func (*capturingErrorSink) Init(logr.RuntimeInfo)    {}
func (*capturingErrorSink) Enabled(int) bool         { return true }
func (*capturingErrorSink) Info(int, string, ...any) {}
func (s *capturingErrorSink) Error(err error, msg string, _ ...any) {
	s.errors = append(s.errors, msg+": "+err.Error())
}
func (s *capturingErrorSink) WithValues(...any) logr.LogSink { return s }
func (s *capturingErrorSink) WithName(string) logr.LogSink   { return s }

type fallbackTestLister struct {
	refs   []*RemoteRef
	called bool
}

func (l *fallbackTestLister) List(bool) ([]*RemoteRef, error) {
	l.called = true
	return l.refs, nil
}

func TestLatestBranchCommitLogsFastLookupFailure(t *testing.T) {
	sink := &capturingErrorSink{}
	lister := &fallbackTestLister{refs: []*RemoteRef{{
		Name: "refs/heads/main",
		Hash: "bdb35e1950b5829c88df134810a0aa9a7da9bc22",
	}}}
	remote := &Remote{
		URL: "https://github.com/rancher/fleet.git",
		Options: &options{
			CABundle: []byte("not a PEM certificate"),
			log:      logr.New(sink),
		},
		Lister: lister,
	}

	commit, err := remote.LatestBranchCommit(context.Background(), "main")
	if err != nil {
		t.Fatalf("LatestBranchCommit() error = %v", err)
	}
	if want := "bdb35e1950b5829c88df134810a0aa9a7da9bc22"; commit != want {
		t.Fatalf("LatestBranchCommit() = %q, want %q", commit, want)
	}
	if !lister.called {
		t.Fatal("fallback lister was not called")
	}
	if len(sink.errors) != 1 || !strings.Contains(sink.errors[0], "Fast commit lookup failed") {
		t.Fatalf("logged errors = %v, want one fast-lookup failure", sink.errors)
	}
	if !strings.Contains(sink.errors[0], "malformed certificate") {
		t.Fatalf("logged error = %q, want the CA parsing reason", sink.errors[0])
	}
}
