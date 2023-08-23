package cli

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"

	httpgit "github.com/go-git/go-git/v5/plumbing/transport/http"
	gossh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"github.com/rancher/fleet/internal/bundlereader"
	"github.com/rancher/fleet/internal/cmd/cli/apply"
	"github.com/rancher/fleet/internal/cmd/cli/writer"
	command "github.com/rancher/wrangler-cli"
	"github.com/rancher/wrangler/pkg/yaml"
	"github.com/spf13/cobra"
	giturls "github.com/whilp/git-urls"
	"golang.org/x/crypto/ssh"
)

type readFile func(name string) ([]byte, error)

func NewApply() *cobra.Command {
	cmd := command.Command(&Apply{}, cobra.Command{
		Use:   "apply [flags] BUNDLE_NAME PATH...",
		Short: "Render a bundle into a Kubernetes resource and apply it in the Fleet Manager",
	})
	command.AddDebug(cmd, &Debug)
	return cmd
}

type Apply struct {
	BundleInputArgs
	OutputArgsNoDefault
	Label                       map[string]string `usage:"Labels to apply to created bundles" short:"l"`
	TargetsFile                 string            `usage:"Addition source of targets and restrictions to be append"`
	Compress                    bool              `usage:"Force all resources to be compress" short:"c"`
	ServiceAccount              string            `usage:"Service account to assign to bundle created" short:"a"`
	SyncGeneration              int               `usage:"Generation number used to force sync the deployment"`
	TargetNamespace             string            `usage:"Ensure this bundle goes to this target namespace"`
	Paused                      bool              `usage:"Create bundles in a paused state"`
	Commit                      string            `usage:"Commit to assign to the bundle" env:"COMMIT"`
	Username                    string            `usage:"Basic auth username for helm repo" env:"HELM_USERNAME"`
	PasswordFile                string            `usage:"Path of file containing basic auth password for helm repo"`
	CACertsFile                 string            `usage:"Path of custom cacerts for helm repo" name:"cacerts-file"`
	SSHPrivateKeyFile           string            `usage:"Path of ssh-private-key for helm repo" name:"ssh-privatekey-file"`
	HelmRepoURLRegex            string            `usage:"Helm credentials will be used if the helm repo matches this regex. Credentials will always be used if this is empty or not provided" name:"helm-repo-url-regex"`
	KeepResources               bool              `usage:"Keep resources created after the GitRepo or Bundle is deleted" name:"keep-resources"`
	HelmCredentialsByPathFile   string            `usage:"Path of file containing helm credentials for paths" name:"helm-credentials-by-path-file"`
	CorrectDrift                bool              `usage:"Rollback any change made from outside of Fleet" name:"correct-drift"`
	CorrectDriftForce           bool              `usage:"Use --force when correcting drift. Resources can be deleted and recreated" name:"correct-drift-force"`
	CorrectDriftKeepFailHistory bool              `usage:"Keep helm history for failed rollbacks" name:"correct-drift-keep-fail-history"`
	GitRepo                     string            `usage:"Repository URL" name:"git-repo"`
	GitUsername                 string            `usage:"Basic auth username for git repository" name:"git-username" env:"GIT_USERNAME"`
	GitPasswordFile             string            `usage:"Basic auth password for git repository" name:"git-password-file"`
	GitSSHPrivateKey            string            `usage:"Git repository SSH private key path" name:"git-ssh-private-key"`
	GitInsecureSkipTLS          bool              `usage:"Git repository skip tls" name:"git-insecure-skip-tls"`
	GitKnownHostsFile           string            `usage:"Git repository known hosts file" name:"git-known-hosts"`
	GitCABundleFile             string            `usage:"Git repository CA Bundle" name:"git-ca-bundle-file"`
	GitBranch                   string            `usage:"Git repository branch" name:"git-branch"`
}

func (a *Apply) Run(cmd *cobra.Command, args []string) error {
	labels := a.Label
	if a.Commit == "" {
		a.Commit = currentCommit()
	}
	if a.Commit != "" {
		if labels == nil {
			labels = map[string]string{}
		}
		labels["fleet.cattle.io/commit"] = a.Commit
	}

	name := ""
	opts := apply.Options{
		BundleFile:                  a.BundleFile,
		Output:                      writer.NewDefaultNone(a.Output),
		Compress:                    a.Compress,
		ServiceAccount:              a.ServiceAccount,
		Labels:                      a.Label,
		TargetsFile:                 a.TargetsFile,
		TargetNamespace:             a.TargetNamespace,
		Paused:                      a.Paused,
		SyncGeneration:              int64(a.SyncGeneration),
		HelmRepoURLRegex:            a.HelmRepoURLRegex,
		KeepResources:               a.KeepResources,
		CorrectDrift:                a.CorrectDrift,
		CorrectDriftForce:           a.CorrectDriftForce,
		CorrectDriftKeepFailHistory: a.CorrectDriftKeepFailHistory,
		GitRepo:                     a.GitRepo,
		GitInsecureSkipTLS:          a.GitInsecureSkipTLS,
		GitKnownHostsFile:           a.GitKnownHostsFile,
		GitBranch:                   a.GitBranch,
	}

	err := a.addHelmAuthToOpts(&opts, os.ReadFile)
	if err != nil {
		return err
	}
	err = a.addGitAuthToOpts(&opts, os.ReadFile)
	if err != nil {
		return err
	}
	if a.File == "-" {
		opts.BundleReader = os.Stdin
		if len(args) != 1 {
			return fmt.Errorf("the bundle name is required as the first argument")
		}
		name = args[0]
	} else if a.File != "" {
		f, err := os.Open(a.File)
		if err != nil {
			return err
		}
		defer f.Close()
		opts.BundleReader = f
		if len(args) != 1 {
			return fmt.Errorf("the bundle name is required as the first argument")
		}
		name = args[0]
	} else if len(args) < 1 {
		return fmt.Errorf("at least one arguments is required BUNDLE_NAME")
	} else {
		name = args[0]
		args = args[1:]
	}

	return apply.Apply(cmd.Context(), Client, name, args, opts)
}

// addHelmAuthToOpts adds auth if provided as arguments. It will look first for HelmCredentialsByPathFile. If HelmCredentialsByPathFile
// is not provided it means that the same helm secret should be used for all helm repositories, then it will look for
// Username, PasswordFile, CACertsFile and SSHPrivateKeyFile
func (a *Apply) addHelmAuthToOpts(opts *apply.Options, readFile readFile) error {
	if a.HelmCredentialsByPathFile != "" {
		file, err := readFile(a.HelmCredentialsByPathFile)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		var authByPath map[string]bundlereader.Auth
		err = yaml.Unmarshal(file, &authByPath)
		if err != nil {
			return err
		}
		opts.AuthByPath = authByPath

		return nil
	}

	if a.Username != "" && a.PasswordFile != "" {
		password, err := readFile(a.PasswordFile)
		if err != nil && !os.IsNotExist(err) {
			return err
		}

		opts.Auth.Username = a.Username
		opts.Auth.Password = string(password)
	}
	if a.CACertsFile != "" {
		cabundle, err := readFile(a.CACertsFile)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		opts.Auth.CABundle = cabundle
	}
	if a.SSHPrivateKeyFile != "" {
		privateKey, err := readFile(a.SSHPrivateKeyFile)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		opts.Auth.SSHPrivateKey = privateKey
	}

	return nil
}

// addGitAuthToOpts adds auth for cloning git repos based on the parameters provided in opts.
func (a *Apply) addGitAuthToOpts(opts *apply.Options, readFile readFile) error {
	if a.GitUsername != "" && a.GitPasswordFile != "" {
		password, err := readFile(a.GitPasswordFile)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		opts.GitAuth = &httpgit.BasicAuth{
			Username: a.GitUsername,
			Password: string(password),
		}
	}
	if a.GitCABundleFile != "" {
		caBundle, err := readFile(a.GitCABundleFile)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		opts.GitCABundle = caBundle
	}
	if a.GitSSHPrivateKey != "" {
		privateKey, err := readFile(a.GitSSHPrivateKey)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		gitURL, err := giturls.Parse(a.GitRepo)
		if err != nil {
			return err
		}
		auth, err := gossh.NewPublicKeys(gitURL.User.Username(), privateKey, "")
		if err != nil {
			return err
		}
		if a.GitKnownHostsFile != "" {
			knownHosts, err := readFile(a.GitKnownHostsFile)
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			knownHostsCallBack, err := createKnownHostsCallBack(knownHosts)
			if err != nil {
				return err
			}
			auth.HostKeyCallback = knownHostsCallBack
		} else {
			//nolint G106: Use of ssh InsecureIgnoreHostKey should be audited
			auth.HostKeyCallback = ssh.InsecureIgnoreHostKey()
		}
		opts.GitAuth = auth
	}

	return nil
}

func currentCommit() string {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	buf := &bytes.Buffer{}
	cmd.Stdout = buf
	err := cmd.Run()
	if err == nil {
		return strings.TrimSpace(buf.String())
	}
	return ""
}

func createKnownHostsCallBack(knownHosts []byte) (ssh.HostKeyCallback, error) {
	f, err := os.CreateTemp("", "known_hosts")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(f.Name())
	defer f.Close()

	if _, err := f.Write(knownHosts); err != nil {
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("closing knownHosts file %s: %w", f.Name(), err)
	}

	return gossh.NewKnownHostsCallback(f.Name())
}
