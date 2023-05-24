package cmds

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rancher/fleet/modules/cli/apply"
	"github.com/rancher/fleet/modules/cli/pkg/writer"
	"github.com/rancher/fleet/pkg/bundlereader"
	command "github.com/rancher/wrangler-cli"
	"github.com/rancher/wrangler/pkg/yaml"
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
	Label                     map[string]string `usage:"Labels to apply to created bundles" short:"l"`
	TargetsFile               string            `usage:"Addition source of targets and restrictions to be append"`
	Compress                  bool              `usage:"Force all resources to be compress" short:"c"`
	ServiceAccount            string            `usage:"Service account to assign to bundle created" short:"a"`
	SyncGeneration            int               `usage:"Generation number used to force sync the deployment"`
	TargetNamespace           string            `usage:"Ensure this bundle goes to this target namespace"`
	Paused                    bool              `usage:"Create bundles in a paused state"`
	Commit                    string            `usage:"Commit to assign to the bundle" env:"COMMIT"`
	Username                  string            `usage:"Basic auth username for helm repo" env:"HELM_USERNAME"`
	PasswordFile              string            `usage:"Path of file containing basic auth password for helm repo"`
	CACertsFile               string            `usage:"Path of custom cacerts for helm repo" name:"cacerts-file"`
	SSHPrivateKeyFile         string            `usage:"Path of ssh-private-key for helm repo" name:"ssh-privatekey-file"`
	HelmRepoURLRegex          string            `usage:"Helm credentials will be used if the helm repo matches this regex. Credentials will always be used if this is empty or not provided" name:"helm-repo-url-regex"`
	KeepResources             bool              `usage:"Keep resources created after the GitRepo or Bundle is deleted" name:"keep-resources"`
	HelmCredentialsByPathFile string            `usage:"Path of file containing helm credentials for paths" name:"helm-credentials-by-path-file"`
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
	opts := &apply.Options{
		BundleFile:       a.BundleFile,
		Output:           writer.NewDefaultNone(a.Output),
		Compress:         a.Compress,
		ServiceAccount:   a.ServiceAccount,
		Labels:           a.Label,
		TargetsFile:      a.TargetsFile,
		TargetNamespace:  a.TargetNamespace,
		Paused:           a.Paused,
		SyncGeneration:   int64(a.SyncGeneration),
		HelmRepoURLRegex: a.HelmRepoURLRegex,
		KeepResources:    a.KeepResources,
	}
	err := a.addAuthToOpts(opts, os.ReadFile)
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

// addAuthToOpts add auth if provided as arguments. It will look first for HelmCredentialsByPathFile. If HelmCredentialsByPathFile
// is not provided it means that the same helm secret should be used for all helm repositories, then it will look for
// Username, PasswordFile, CACertsFile and SSHPrivateKeyFile
func (a *Apply) addAuthToOpts(opts *apply.Options, readFile readFile) error {
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
	} else {
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
