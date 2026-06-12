package cli

import (
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"k8s.io/client-go/util/homedir"
)

func TestNewCleanupKubeConfigFlag(t *testing.T) {
	customPath := filepath.Join(homedir.HomeDir(), "custom", "config")
	testCases := []struct {
		name                       string
		args                       []string
		subcommand                 func() *cobra.Command
		expectedKubeconfig         string
		shouldUseDefaultKubeconfig bool
	}{
		{
			name:                       "custom kubeconfig for cluster registration",
			args:                       []string{"--kubeconfig", customPath, "test-bundle"},
			subcommand:                 NewClusterRegistration,
			expectedKubeconfig:         customPath,
			shouldUseDefaultKubeconfig: false,
		},
		{
			name:                       "custom kubeconfig for git job",
			args:                       []string{"--kubeconfig", customPath, "test-bundle"},
			subcommand:                 NewGitjob,
			expectedKubeconfig:         customPath,
			shouldUseDefaultKubeconfig: false,
		},
		{
			name:                       "no custom kubeconfig for cluster registration",
			args:                       []string{"test-bundle"},
			subcommand:                 NewClusterRegistration,
			expectedKubeconfig:         "",
			shouldUseDefaultKubeconfig: true,
		},
		{
			name:                       "no custom kubeconfig for git job",
			args:                       []string{"test-bundle"},
			subcommand:                 NewGitjob,
			expectedKubeconfig:         "",
			shouldUseDefaultKubeconfig: true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := tc.subcommand()

			err := cmd.ParseFlags(tc.args)
			if err != nil {
				t.Fatalf("failed to parse flags: %v", err)
			}

			kubeconfigFlag := cmd.Flag("kubeconfig")
			if kubeconfigFlag == nil {
				t.Fatal("kubeconfig flag not registered")
			}

			gotKubeconfig := kubeconfigFlag.Value.String()
			if gotKubeconfig != tc.expectedKubeconfig {
				t.Errorf("kubeconfig flag value mismatch: expected %q, got %q", tc.expectedKubeconfig, gotKubeconfig)
			}
		})
	}
}
