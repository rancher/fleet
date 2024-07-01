package register

import (
	"context"
	"net/http"
	"regexp"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	corecontrollers "github.com/rancher/wrangler/v3/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/v3/pkg/generic/fake"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type mockCoreInterface struct {
	corecontrollers.ConfigMapController
	corecontrollers.NamespaceController
	corecontrollers.SecretController
}

func (c mockCoreInterface) ConfigMap() corecontrollers.ConfigMapController {
	return c.ConfigMapController
}
func (c mockCoreInterface) Namespace() corecontrollers.NamespaceController {
	return c.NamespaceController
}
func (c mockCoreInterface) Secret() corecontrollers.SecretController {
	return c.SecretController
}

// This is a smoke test, preventing regressions for https://github.com/rancher/rancher/issues/43012 where adding a label
// to cluster registration labels would panic, causing the Fleet agent to crash, if the map of labels was nil.
func TestRunRegistrationLabelSmokeTest(t *testing.T) {
	namespace := "my-namespace"
	secret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
		},
		Data: map[string][]byte{
			"apiServerURL":     []byte("https://42.42.42.42:4242"),
			"clusterNamespace": []byte(namespace),
		},
	}

	ctrl := gomock.NewController(t)
	mockSecretController := fake.NewMockControllerInterface[*corev1.Secret, *corev1.SecretList](ctrl)
	mockSecretController.EXPECT().Get(namespace, "fleet-agent-bootstrap", metav1.GetOptions{}).Return(&secret, nil)

	mockConfigMapController := fake.NewMockControllerInterface[*corev1.ConfigMap, *corev1.ConfigMapList](ctrl)
	mockConfigMapController.EXPECT().Get(secret.Namespace, "fleet-agent", metav1.GetOptions{}).
		Return(nil, &apierrors.StatusError{ErrStatus: metav1.Status{Reason: metav1.StatusReasonNotFound}})

	mockNamespaceController := fake.NewMockNonNamespacedControllerInterface[*corev1.Namespace, *corev1.NamespaceList](ctrl)
	mockNamespaceController.EXPECT().Get("kube-system", metav1.GetOptions{}).Return(&corev1.Namespace{}, nil)

	mockCoreController := mockCoreInterface{mockConfigMapController, mockNamespaceController, mockSecretController}

	http.DefaultClient.Timeout = 100 * time.Millisecond // no need to wait longer, the API server URL is a dummy.
	agentSecret, err := runRegistration(context.Background(), mockCoreController, namespace)

	assert.Nil(t, agentSecret)
	// expecting an error at cluster registration creation time because the API server URL is a dummy.
	// this may be a timeout or a simple 'connection refused' error.
	assert.Regexp(t, regexp.MustCompile("cannot create clusterregistration.*"), err.Error())
}
