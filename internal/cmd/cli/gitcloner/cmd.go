package gitcloner

import (
	"github.com/spf13/cobra"
)

type CloneGit interface {
	CloneRepo(opts *GitCloner) error
}

type GitCloner struct {
	Repo              string
	Path              string
	Branch            string
	Revision          string
	CABundleFile      string
	Username          string
	PasswordFile      string
	SSHPrivateKeyFile string
	InsecureSkipTLS   bool
	KnownHostsFile    string
	OAuthToken        string
}

var opts *GitCloner

func NewCmd(gitCloner CloneGit) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gitcloner [REPO] [PATH]",
		Short: "Clones a git repository",
		Long: `The gitcloner command clones a git repository to a specified path.
It supports various authentication methods, including basic auth, SSH private keys, and OAuth tokens.
You can also specify a default branch other than "master" if none is provided.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Repo = args[0]
			opts.Path = args[1]

			return gitCloner.CloneRepo(opts)
		},
	}
	opts = &GitCloner{}
	cmd.Flags().StringVarP(&opts.Branch, "branch", "b", "", "git branch (default is 'master')")
	cmd.Flags().StringVar(&opts.Revision, "revision", "", "git revision")
	cmd.Flags().StringVar(&opts.CABundleFile, "ca-bundle-file", "", "CA bundle file")
	cmd.Flags().StringVarP(&opts.Username, "username", "u", "", "user name for basic auth")
	cmd.Flags().StringVar(&opts.PasswordFile, "password-file", "", "password file for basic auth")
	cmd.Flags().StringVar(&opts.SSHPrivateKeyFile, "ssh-private-key-file", "", "ssh private key file path")
	cmd.Flags().BoolVar(&opts.InsecureSkipTLS, "insecure-skip-tls", false, "do not verify tls certificates")
	cmd.Flags().StringVar(&opts.KnownHostsFile, "known-hosts-file", "", "known hosts file")
	cmd.Flags().StringVar(&opts.OAuthToken, "oauth-token", "", "OAuth token for authentication")

	return cmd
}
