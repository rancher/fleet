package cli_test

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"

	cli "github.com/rancher/fleet/internal/cmd/cli"
)

func TestDump_ValidateFilterOptions(t *testing.T) {
	strPtr := func(s string) *string { return &s }

	tests := []struct {
		name             string
		dump             cli.Dump
		namespaceChanged bool
		namespace        string
		wantErr          string
		wantNamespace    *string // if non-nil, d.Namespace is asserted after the call
	}{
		// --all-namespaces behaviour
		{
			name: "all-namespaces without explicit namespace is valid",
			dump: cli.Dump{
				AllNamespaces: true,
			},
			wantNamespace: strPtr(""), // default "fleet-local" is cleared to "" by ValidateFilterOptions
		},
		{
			name: "all-namespaces with explicit namespace returns error",
			dump: cli.Dump{
				AllNamespaces: true,
			},
			namespaceChanged: true,
			namespace:        "my-ns",
			wantErr:          "--namespace and --all-namespaces are mutually exclusive",
		},

		// Mutually exclusive secret/content flags
		{
			name:    "with-secrets and with-secrets-metadata are mutually exclusive",
			dump:    cli.Dump{WithSecrets: true, WithSecretsMetadata: true},
			wantErr: "--with-secrets and --with-secrets-metadata are mutually exclusive",
		},
		{
			name:    "with-content and with-content-metadata are mutually exclusive",
			dump:    cli.Dump{WithContent: true, WithContentMetadata: true},
			wantErr: "--with-content and --with-content-metadata are mutually exclusive",
		},

		// Mutually exclusive resource filters
		{
			name:    "gitrepo and bundle are mutually exclusive",
			dump:    cli.Dump{Gitrepo: "my-repo", Bundle: "my-bundle"},
			wantErr: "--gitrepo and --bundle are mutually exclusive",
		},
		{
			name:    "gitrepo and helmop are mutually exclusive",
			dump:    cli.Dump{Gitrepo: "my-repo", Helmop: "my-helmop"},
			wantErr: "--gitrepo and --helmop are mutually exclusive",
		},
		{
			name:    "bundle and helmop are mutually exclusive",
			dump:    cli.Dump{Bundle: "my-bundle", Helmop: "my-helmop"},
			wantErr: "--bundle and --helmop are mutually exclusive",
		},

		// Resource filters require explicit --namespace
		{
			name:    "gitrepo without explicit namespace returns error",
			dump:    cli.Dump{Gitrepo: "my-repo"},
			wantErr: "--gitrepo, --bundle, and --helmop filters require --namespace to be explicitly specified",
		},
		{
			name:    "bundle without explicit namespace returns error",
			dump:    cli.Dump{Bundle: "my-bundle"},
			wantErr: "--gitrepo, --bundle, and --helmop filters require --namespace to be explicitly specified",
		},
		{
			name:    "helmop without explicit namespace returns error",
			dump:    cli.Dump{Helmop: "my-helmop"},
			wantErr: "--gitrepo, --bundle, and --helmop filters require --namespace to be explicitly specified",
		},
		{
			name:             "gitrepo with explicit namespace is valid",
			dump:             cli.Dump{Gitrepo: "my-repo", FleetClient: cli.FleetClient{Namespace: "my-ns"}},
			namespaceChanged: true,
			namespace:        "my-ns",
		},

		// Valid no-op
		{
			name: "no flags is valid",
			dump: cli.Dump{FleetClient: cli.FleetClient{Namespace: "fleet-local"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &cobra.Command{}
			cmd.Flags().String("namespace", "fleet-local", "namespace")
			if tt.namespaceChanged {
				if err := cmd.Flags().Set("namespace", tt.namespace); err != nil {
					t.Fatalf("failed to set namespace flag: %v", err)
				}
			}

			err := tt.dump.ValidateFilterOptions(cmd)

			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("expected no error, got: %v", err)
				}
			} else {
				if err == nil {
					t.Errorf("expected error containing %q, got nil", tt.wantErr)
				} else if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("expected error containing %q, got %q", tt.wantErr, err.Error())
				}
			}

			if tt.wantNamespace != nil && tt.dump.Namespace != *tt.wantNamespace {
				t.Errorf("expected namespace %q after validation, got %q", *tt.wantNamespace, tt.dump.Namespace)
			}
		})
	}
}
