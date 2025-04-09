package ssh

import (
	"context"
	"errors"
	"fmt"
	"os"

	gossh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"golang.org/x/crypto/ssh"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/fleet/internal/config"
)

const (
	KnownHostsConfigMap = "known-hosts" // XXX: is this the name we want?
	KnownHostsEnvVar    = "FLEET_KNOWN_HOSTS"
)

type KnownHosts struct {
	EnforceHostKeyChecks bool
}

// Get looks for SSH known hosts information in the following locations, in decreasing order of precedence:
// * secret referenced by secretName, in namespace ns
// * `gitcredential` secret, in namespace ns, if secretName is empty
// * config map in Fleet controller namespace
// It returns found known_hosts data, if any, and any error that may have happened in the process (eg. missing fallback,
// Fleet-wide known hosts config map)
// Possible returned errors include a failure to enforce strict host key checks, if those are enabled but no known_hosts
// data is found.
func (s KnownHosts) Get(ctx context.Context, c client.Client, ns string, secretName string) (string, error) {
	if ns == "" {
		return "", errors.New("empty namespace provided for secret search")
	}

	if secretName == "" {
		secretName = config.DefaultGitCredentialsSecretName
	}

	var secret corev1.Secret
	err := c.Get(ctx, types.NamespacedName{
		Namespace: ns,
		Name:      secretName,
	}, &secret)

	if client.IgnoreNotFound(err) != nil {
		return "", err
	}

	return s.GetWithSecret(ctx, c, &secret)
}

// GetWithSecret looks for SSH known hosts information in the injected secret, then in a config map in the Fleet
// controller namespace, returning data from the first source it finds.
// It returns found known_hosts data, if any, and any error that may have happened in the process (eg. missing fallback,
// Fleet-wide known hosts config map)
// Possible returned errors include a failure to enforce strict host key checks, if those are enabled but no known_hosts
// data is found.
func (s KnownHosts) GetWithSecret(ctx context.Context, c client.Client, secret *corev1.Secret) (string, error) {
	if secret != nil {
		kh, ok := secret.Data["known_hosts"]
		if ok && len(kh) > 0 {
			return string(kh), nil
		}
	}

	var cm corev1.ConfigMap
	err := c.Get(ctx, types.NamespacedName{
		Namespace: config.DefaultNamespace,
		Name:      KnownHostsConfigMap,
	}, &cm)

	if client.IgnoreNotFound(err) != nil {
		return "", err
	}

	if err != nil { // The config map should exist as part of any Fleet deployment
		return "", fmt.Errorf(
			"config map %q should exist in namespace %q; this Fleet deployment is incomplete",
			KnownHostsConfigMap,
			config.DefaultNamespace,
		)
	}

	kh, ok := cm.Data["known_hosts"]
	if ok && len(kh) > 0 {
		return string(kh), nil
	}

	if s.EnforceHostKeyChecks {
		return "", errors.New("strict host key checks are enforced, but no known_hosts data was found")
	}

	return "", nil
}

func (s KnownHosts) IsStrict() bool {
	return s.EnforceHostKeyChecks
}

// CreateKnownHostsCallBack creates a callback function for host key checks based on the provided knownHosts.
func CreateKnownHostsCallBack(knownHosts []byte) (ssh.HostKeyCallback, error) {
	f, err := os.CreateTemp("", "known_hosts")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(f.Name())
	defer f.Close()

	if _, err := f.Write(knownHosts); err != nil {
		return nil, err
	}

	return gossh.NewKnownHostsCallback(f.Name())
}
