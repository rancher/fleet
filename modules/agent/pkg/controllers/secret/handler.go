package secret

import (
	"bytes"
	"context"
	"fmt"

	"github.com/rancher/fleet/modules/agent/pkg/register"
	v1 "github.com/rancher/wrangler/pkg/generated/controllers/core/v1"
	corev1 "k8s.io/api/core/v1"
)

type handler struct {
	keyToWatch string
	lastValue  []byte
}

func Register(ctx context.Context, namespace string, secret v1.SecretController) {
	h := handler{
		keyToWatch: fmt.Sprintf("%s/%s", namespace, register.CredName),
	}
	secret.OnChange(ctx, "cred-change", h.OnChange)
}

func (h *handler) OnChange(key string, secret *corev1.Secret) (*corev1.Secret, error) {
	if key != h.keyToWatch {
		return secret, nil
	}

	if len(h.lastValue) == 0 {
		h.lastValue = secret.Data[register.Kubeconfig]
		return secret, nil
	}

	if !bytes.Equal(h.lastValue, secret.Data[register.Kubeconfig]) {
		//logrus.Fatalf("Agent credential has change, quitting controller")
	}
	return secret, nil
}
