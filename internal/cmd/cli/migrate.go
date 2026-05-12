package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	command "github.com/rancher/fleet/internal/cmd"
	"github.com/rancher/fleet/internal/cmd/cli/migrate"
)

// newMigrateCmd returns the hidden migrate command invoked by Helm post-upgrade
// Jobs. It is intentionally not shown in fleet --help because it is a one-time
// internal operation, not a user-facing workflow.
func newMigrateCmd() *cobra.Command {
	m := command.Command(&Migrate{}, cobra.Command{
		Short:         "Run one-time upgrade migrations",
		Hidden:        true,
		SilenceUsage:  true,
		SilenceErrors: true,
	})
	m.AddCommand(NewMigrateGitRepoHelmURLRegex())
	return m
}

func NewMigrateGitRepoHelmURLRegex() *cobra.Command {
	return command.Command(&MigrateGitRepoHelmURLRegex{}, cobra.Command{
		Use:           "gitrepo-helm-url-regex [flags]",
		Short:         "Set helmRepoURLRegex on GitRepos that have a Helm secret but no regex",
		SilenceUsage:  true,
		SilenceErrors: true,
	})
}

type Migrate struct{}

func (m *Migrate) Run(cmd *cobra.Command, _ []string) error {
	return cmd.Help()
}

type MigrateGitRepoHelmURLRegex struct {
	command.DebugConfig
	SystemNamespace string `usage:"system namespace" env:"NAMESPACE" default:"cattle-fleet-system"`
}

func (m *MigrateGitRepoHelmURLRegex) PersistentPre(_ *cobra.Command, _ []string) error {
	if err := m.SetupDebug(); err != nil {
		return fmt.Errorf("failed to set up debug logging: %w", err)
	}
	return nil
}

func (m *MigrateGitRepoHelmURLRegex) Run(cmd *cobra.Command, _ []string) error {
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zopts)))
	ctx := log.IntoContext(cmd.Context(), ctrl.Log)

	cfg := ctrl.GetConfigOrDie()
	cl, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return err
	}

	return migrate.GitRepoHelmURLRegex(ctx, cl, m.SystemNamespace)
}
