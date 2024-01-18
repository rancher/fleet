package cmd

import (
	"testing"
)

func TestArgsAreSet(t *testing.T) {
	mock := &clonerMock{}
	cmd := New(mock)
	cmd.SetArgs([]string{"test-repo", "test-path", "--branch", "master", "--revision", "v0.1.0", "--ca-bundle-file", "caFile", "--username", "user",
		"--password-file", "passwordFile", "--ssh-private-key-file", "sshFile", "--insecure-skip-tls", "--known-hosts-file", "knownFile"})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.opts.Repo != "test-repo" {
		t.Fatalf("expected repo test-repo, got %v", mock.opts.Repo)
	}
	if mock.opts.Path != "test-path" {
		t.Fatalf("expected path test-path, got %v", mock.opts.Path)
	}
	if mock.opts.Branch != "master" {
		t.Fatalf("expected branch master, got %v", mock.opts.Branch)
	}
	if mock.opts.Revision != "v0.1.0" {
		t.Fatalf("expected revision v0.1.0, got %v", mock.opts.Revision)
	}
	if mock.opts.CABundleFile != "caFile" {
		t.Fatalf("expected CABundleFile caFile, got %v", mock.opts.CABundleFile)
	}
	if mock.opts.Username != "user" {
		t.Fatalf("expected Username user, got %v", mock.opts.Username)
	}
	if mock.opts.PasswordFile != "passwordFile" {
		t.Fatalf("expected PasswordFile passwordFile, got %v", mock.opts.PasswordFile)
	}
	if !mock.opts.InsecureSkipTLS {
		t.Fatalf("expected InsecureSkipTLS to be true")
	}
	if mock.opts.KnownHostsFile != "knownFile" {
		t.Fatalf("expected KnownHostsFile knownFile, got %v", mock.opts.KnownHostsFile)
	}
}

type clonerMock struct {
	opts *Options
}

func (m *clonerMock) CloneRepo(opts *Options) error {
	m.opts = opts

	return nil
}
