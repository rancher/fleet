package cli

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/pprof"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/fleet/internal/bundlereader"
	command "github.com/rancher/fleet/internal/cmd"
	"github.com/rancher/fleet/internal/cmd/cli/apply"
	"github.com/rancher/fleet/internal/cmd/cli/writer"
	ssh "github.com/rancher/fleet/internal/ssh"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes"
	typedv1core "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
)

type readFile func(name string) ([]byte, error)

// NewApply returns a subcommand to create bundles from directories
func NewApply() *cobra.Command {
	return command.Command(&Apply{}, cobra.Command{
		Use:   "apply [flags] BUNDLE_NAME PATH...",
		Short: "Create bundles from directories, and output them or apply them on a cluster",
	})
}

type Apply struct {
	FleetClient
	BundleInputArgs
	OutputArgsNoDefault
	Label                        map[string]string `usage:"Labels to apply to created bundles" short:"l"`
	TargetsFile                  string            `usage:"Addition source of targets and restrictions to be append"`
	Compress                     bool              `usage:"Force all resources to be compressed" short:"c"`
	ServiceAccount               string            `usage:"Service account to assign to bundle created" short:"a"`
	SyncGeneration               int               `usage:"Generation number used to force sync the deployment"`
	TargetNamespace              string            `usage:"Ensure this bundle goes to this target namespace"`
	Paused                       bool              `usage:"Create bundles in a paused state"`
	Commit                       string            `usage:"Commit to assign to the bundle" env:"COMMIT"`
	Username                     string            `usage:"Basic auth username for helm repo" env:"HELM_USERNAME"`
	PasswordFile                 string            `usage:"Path of file containing basic auth password for helm repo"`
	CACertsFile                  string            `usage:"Path of custom cacerts for helm repo" name:"cacerts-file"`
	SSHPrivateKeyFile            string            `usage:"Path of ssh-private-key for helm repo" name:"ssh-privatekey-file"`
	HelmRepoURLRegex             string            `usage:"Helm credentials will be used if the helm repo matches this regex. Credentials will always be used if this is empty or not provided" name:"helm-repo-url-regex"`
	KeepResources                bool              `usage:"Keep resources created after the GitRepo or Bundle is deleted" name:"keep-resources"`
	DeleteNamespace              bool              `usage:"Delete GitRepo target namespace after the GitRepo or Bundle is deleted" name:"delete-namespace"`
	HelmCredentialsByPathFile    string            `usage:"Path of file containing helm credentials for paths" name:"helm-credentials-by-path-file"`
	HelmBasicHTTP                bool              `usage:"Uses plain HTTP connections when downloading from helm repositories" name:"helm-basic-http"`
	HelmInsecureSkipTLS          bool              `usage:"Skip TLS verification when downloading from helm repositories" name:"helm-insecure-skip-tls"`
	CorrectDrift                 bool              `usage:"Rollback any change made from outside of Fleet" name:"correct-drift"`
	CorrectDriftForce            bool              `usage:"Use --force when correcting drift. Resources can be deleted and recreated" name:"correct-drift-force"`
	CorrectDriftKeepFailHistory  bool              `usage:"Keep helm history for failed rollbacks" name:"correct-drift-keep-fail-history"`
	OCIRegistrySecret            string            `usage:"OCI storage registry secret name" name:"oci-registry-secret"`
	DrivenScan                   bool              `usage:"Use driven scan. Bundles are defined by the user" name:"driven-scan"`
	DrivenScanSeparator          string            `usage:"Separator to use for bundle folder and options file" name:"driven-scan-sep" default:":"`
	BundleCreationMaxConcurrency int               `usage:"Maximum number of concurrent bundle creation routines" name:"bundle-creation-max-concurrency" default:"4" env:"FLEET_BUNDLE_CREATION_MAX_CONCURRENCY"`

	ProfillesDir   string `usage:"Directory to use for profiling" name:"profiles-dir"`
	postRunCleanup []func()
}

func (r *Apply) PersistentPre(cmd *cobra.Command, _ []string) error {
	if err := r.SetupDebug(); err != nil {
		return fmt.Errorf("failed to set up debug logging: %w", err)
	}

	profilesDir := r.ProfillesDir
	if profilesDir == "" {
		return nil
	}
	if err := os.MkdirAll(profilesDir, 0755); err != nil {
		return err
	}

	// CPU profiling
	f, err := os.Create(filepath.Join(profilesDir, "cpu-profile.pprof"))
	if err != nil {
		return err
	}
	if err := pprof.StartCPUProfile(f); err != nil {
		f.Close()
		return err
	}
	r.postRunCleanup = append(r.postRunCleanup, func() {
		pprof.StopCPUProfile()
		f.Close()
	})

	ctx, cancel := context.WithCancel(cmd.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)

		var count int

		for {
			select {
			case <-ctx.Done():
				// not return, let it run one last time
			case <-time.After(10 * time.Second):
			}

			memProfile := fmt.Sprintf("heap-profile-%d.pprof", count)
			if err := func() error {
				f, err := os.Create(filepath.Join(profilesDir, memProfile))
				if err != nil {
					return err
				}
				defer f.Close()
				return pprof.WriteHeapProfile(f)
			}(); err != nil {
				log.Printf("error creating memory profile: %v", err)
			}

			goroutineProfile := fmt.Sprintf("goroutine-profile-%d.pprof", count)
			if err := func() error {
				f, err := os.Create(filepath.Join(profilesDir, goroutineProfile))
				if err != nil {
					return err
				}
				defer f.Close()
				return pprof.Lookup("goroutine").WriteTo(f, 0)
			}(); err != nil {
				log.Printf("error creating goroutine profile: %v", err)
			}

			if ctx.Err() != nil {
				return
			}
			count++
		}
	}()
	r.postRunCleanup = append(r.postRunCleanup, func() {
		cancel()
		<-done
	})
	return nil
}

func (a *Apply) Run(cmd *cobra.Command, args []string) error {
	defer func() {
		for _, f := range a.postRunCleanup {
			f()
		}
	}()

	// Apply retries on conflict errors.
	// We could have race conditions updating the Bundle in high load situations
	var err error
	retries, err := apply.GetOnConflictRetries()
	if err != nil {
		logrus.Errorf("failed parsing env variable %s, using defaults, err: %v", apply.FleetApplyConflictRetriesEnv, err)
	}
	for range retries {
		err = a.run(cmd, args)
		if !errors.IsConflict(err) {
			break
		}
	}

	return err
}

func (a *Apply) run(cmd *cobra.Command, args []string) error {
	labels := a.Label
	if a.Commit == "" {
		a.Commit = currentCommit()
	}
	if a.Commit != "" {
		if labels == nil {
			labels = map[string]string{}
		}
		labels[fleet.CommitLabel] = a.Commit
	}

	name := ""
	opts := apply.Options{
		Namespace:                    a.Namespace,
		BundleFile:                   a.BundleFile,
		Output:                       writer.NewDefaultNone(a.Output),
		Compress:                     a.Compress,
		ServiceAccount:               a.ServiceAccount,
		Labels:                       a.Label,
		TargetsFile:                  a.TargetsFile,
		TargetNamespace:              a.TargetNamespace,
		Paused:                       a.Paused,
		SyncGeneration:               int64(a.SyncGeneration),
		HelmRepoURLRegex:             a.HelmRepoURLRegex,
		KeepResources:                a.KeepResources,
		DeleteNamespace:              a.DeleteNamespace,
		CorrectDrift:                 a.CorrectDrift,
		CorrectDriftForce:            a.CorrectDriftForce,
		CorrectDriftKeepFailHistory:  a.CorrectDriftKeepFailHistory,
		DrivenScan:                   a.DrivenScan,
		DrivenScanSeparator:          a.DrivenScanSeparator,
		OCIRegistrySecret:            a.OCIRegistrySecret,
		BundleCreationMaxConcurrency: a.BundleCreationMaxConcurrency,
	}

	knownHostsPath, err := writeTmpKnownHosts()
	if err != nil {
		return err
	}

	defer os.RemoveAll(knownHostsPath)

	if err := a.addAuthToOpts(&opts, os.ReadFile, a.HelmBasicHTTP, a.HelmInsecureSkipTLS); err != nil {
		return fmt.Errorf("adding auth to opts: %w", err)
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

	restoreEnv, err := setEnv(knownHostsPath)
	if err != nil {
		return fmt.Errorf("setting git SSH command env var for known hosts: %w", err)
	}

	defer restoreEnv() // nolint: errcheck // best-effort

	ctx := cmd.Context()
	cfg := ctrl.GetConfigOrDie()
	client, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return err
	}
	recorder, err := getEventRecorder(cfg, "fleet-apply")
	if err != nil {
		return err
	}

	if opts.DrivenScan {
		return apply.CreateBundlesDriven(ctx, client, recorder, name, args, opts)
	}
	return apply.CreateBundles(ctx, client, recorder, name, args, opts)
}

// addAuthToOpts adds auth if provided as arguments. It will look first for HelmCredentialsByPathFile. If HelmCredentialsByPathFile
// is not provided it means that the same helm secret should be used for all helm repositories, then it will look for
// Username, PasswordFile, CACertsFile and SSHPrivateKeyFile.
// It will also set the values for using basic HTTP connections and skipping TLS.
func (a *Apply) addAuthToOpts(opts *apply.Options, readFile readFile, helmBasicHTTP, helmInsecureSkipTLS bool) error {
	if a.HelmCredentialsByPathFile != "" {
		file, err := readFile(a.HelmCredentialsByPathFile)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		var authByPath map[string]bundlereader.Auth
		err = yaml.NewYAMLToJSONDecoder(bytes.NewBuffer(file)).Decode(&authByPath)
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

	opts.Auth.BasicHTTP = helmBasicHTTP
	opts.Auth.InsecureSkipVerify = helmInsecureSkipTLS

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

// writeTmpKnownHosts creates a temporary file and writes known_hosts data to it, if such data is available from
// environment variable `FLEET_KNOWN_HOSTS`.
// It returns the name of the file and any error which may have happened while creating the file or writing to it.
func writeTmpKnownHosts() (string, error) {
	knownHosts, isSet := os.LookupEnv(ssh.KnownHostsEnvVar)
	if !isSet || knownHosts == "" {
		return "", nil
	}

	f, err := os.CreateTemp("", "known_hosts")
	if err != nil {
		return "", err
	}

	knownHostsPath := f.Name()

	if err := os.WriteFile(knownHostsPath, []byte(knownHosts), 0600); err != nil {
		return "", fmt.Errorf(
			"failed to write value of %q env var to known_hosts file %s: %w",
			ssh.KnownHostsEnvVar,
			knownHostsPath,
			err,
		)
	}

	return knownHostsPath, nil
}

// setEnv sets the `GIT_SSH_COMMAND` environment variable with a known_hosts flag pointing to the provided
// knownHostsPath. It takes care of preserving existing flags in the existing value of the environment variable, if any,
// except for other user known_hosts file flags.
// It returns a function to restore the environment variable to its initial value, and any error that might have
// occurred in the process.
func setEnv(knownHostsPath string) (func() error, error) {
	commandEnvVar := "GIT_SSH_COMMAND"
	flagName := "UserKnownHostsFile"

	initialCommand, isSet := os.LookupEnv(commandEnvVar)

	fail := func(err error) (func() error, error) {
		return func() error { return nil }, err
	}

	if !isSet {
		if err := os.Setenv(commandEnvVar, fmt.Sprintf("ssh -o %s=%s", flagName, knownHostsPath)); err != nil {
			return fail(err)
		}

		return func() error { return os.Unsetenv(commandEnvVar) }, nil
	}

	// Check if `UserKnownHostsFile` is already present (case-insensitive), even multiple times, and skip it if so.
	var newSSHCommand strings.Builder
	options := strings.Split(initialCommand, " -o ")
	for _, opt := range options {
		kv := strings.Split(opt, "=")
		if len(kv) != 2 { // first element, pre `-o`, or other flag
			if _, err := newSSHCommand.WriteString(opt); err != nil {
				return fail(err)
			}

			continue
		}

		if strings.EqualFold(kv[0], flagName) { // case-insensitive comparison
			continue
		}

		if _, err := newSSHCommand.WriteString(fmt.Sprintf(" -o %s", opt)); err != nil {
			return fail(err)
		}
	}

	if _, err := newSSHCommand.WriteString(fmt.Sprintf(" -o %s=%s", flagName, knownHostsPath)); err != nil {
		return fail(err)
	}

	if err := os.Setenv(commandEnvVar, newSSHCommand.String()); err != nil {
		return fail(err)
	}

	restore := func() error {
		return os.Setenv(commandEnvVar, initialCommand)
	}

	return restore, nil
}

func getEventRecorder(config *rest.Config, componentName string) (record.EventRecorder, error) {
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	broadcaster := record.NewBroadcaster()
	broadcaster.StartStructuredLogging(0)
	broadcaster.StartRecordingToSink(&typedv1core.EventSinkImpl{
		Interface: clientset.CoreV1().Events(""),
	})

	return broadcaster.NewRecorder(scheme, corev1.EventSource{Component: componentName}), nil
}
