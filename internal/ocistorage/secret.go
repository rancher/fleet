package ocistorage

import (
	"context"
	"fmt"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

const (
	OCISecretUsername      = "username"
	OCISecretPassword      = "password"
	OCISecretAgentUsername = "agentUsername"
	OCISecretAgentPassword = "agentPassword"
	OCISecretReference     = "reference"
	OCISecretBasicHTTP     = "basicHTTP"
	OCISecretInsecure      = "insecure"

	OCIStorageDefaultSecretName = "ocistorage"
)

// ReadOptsFromSecret reads the secret identified by the given NamespacedName and
// returns an OCIOpts structure filled with the information obtained from that secret.
func ReadOptsFromSecret(ctx context.Context, c client.Reader, ns types.NamespacedName) (OCIOpts, error) {
	// if no secret was specified, fallback to the default one
	if ns.Name == "" {
		ns.Name = OCIStorageDefaultSecretName
	}

	opts := OCIOpts{}
	var secret corev1.Secret
	err := c.Get(ctx, ns, &secret)
	if err != nil {
		return OCIOpts{}, err
	}

	if secret.Type != fleet.SecretTypeOCIStorage {
		return OCIOpts{}, fmt.Errorf("unexpected secret type: got %q, want %q", secret.Type, fleet.SecretTypeOCIStorage)
	}

	// Fill the values from the secret.
	// Only Reference is strictly required.
	opts.Reference, err = getStringValueFromSecret(secret.Data, OCISecretReference, true)
	if err != nil {
		return OCIOpts{}, err
	}

	opts.Username, err = getStringValueFromSecret(secret.Data, OCISecretUsername, false)
	if err != nil {
		return OCIOpts{}, err
	}

	opts.Password, err = getStringValueFromSecret(secret.Data, OCISecretPassword, false)
	if err != nil {
		return OCIOpts{}, err
	}

	opts.AgentUsername, err = getStringValueFromSecret(secret.Data, OCISecretAgentUsername, false)
	if err != nil {
		return OCIOpts{}, err
	}

	opts.AgentPassword, err = getStringValueFromSecret(secret.Data, OCISecretAgentPassword, false)
	if err != nil {
		return OCIOpts{}, err
	}

	opts.BasicHTTP, err = getBoolValueFromSecret(secret.Data, OCISecretBasicHTTP, false)
	if err != nil {
		return OCIOpts{}, err
	}

	opts.InsecureSkipTLS, err = getBoolValueFromSecret(secret.Data, OCISecretInsecure, false)
	if err != nil {
		return OCIOpts{}, err
	}

	return opts, nil
}

func getStringValueFromSecret(data map[string][]byte, key string, required bool) (string, error) {
	value, ok := data[key]
	if !ok {
		if !required {
			return "", nil
		}
		return "", fmt.Errorf("key %q not found in secret", key)
	}
	return string(value), nil
}

func getBoolValueFromSecret(data map[string][]byte, key string, required bool) (bool, error) {
	value, ok := data[key]
	if !ok {
		if !required {
			return false, nil
		}
		return false, fmt.Errorf("key %q not found in secret", key)
	}
	valueStr := string(value)
	boolValue, err := strconv.ParseBool(valueStr)
	if err != nil {
		return false, fmt.Errorf("failed to parse %q as bool: %w", valueStr, err)
	}

	return boolValue, nil
}
