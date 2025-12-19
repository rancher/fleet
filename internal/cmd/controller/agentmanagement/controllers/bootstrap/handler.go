package bootstrap

import (
	"context"
	"fmt"
	"regexp"

	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	fleetconfig "github.com/rancher/fleet/internal/config"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

var (
	pathSplitter = regexp.MustCompile(`\s*,\s*`)
)

// BootstrapHandler handles bootstrap resource creation based on config changes
// This is not a traditional reconciler, but a config change handler
type BootstrapHandler struct {
	client.Client
	Scheme          *runtime.Scheme
	SystemNamespace string
	ClientConfig    clientcmd.ClientConfig
}

// OnConfig handles config changes and creates/updates bootstrap resources
func (h *BootstrapHandler) OnConfig(ctx context.Context, config *fleetconfig.Config) error {
	logger := log.FromContext(ctx)

	if config.Bootstrap.Namespace == "" || config.Bootstrap.Namespace == "-" {
		logger.Info("Bootstrap disabled, skipping")
		return nil
	}

	logger.Info("Reconciling bootstrap resources", "namespace", config.Bootstrap.Namespace)

	// Create namespace
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: config.Bootstrap.Namespace,
		},
	}
	if err := h.createOrUpdate(ctx, ns); err != nil {
		return fmt.Errorf("failed to create bootstrap namespace: %w", err)
	}

	// Build and create secret
	secret, err := h.buildSecret(ctx, config.Bootstrap.Namespace)
	if err != nil {
		return fmt.Errorf("failed to build secret: %w", err)
	}
	if err := h.createOrUpdate(ctx, secret); err != nil {
		return fmt.Errorf("failed to create secret: %w", err)
	}

	// Get fleet-controller deployment for tolerations
	var deployment appsv1.Deployment
	if err := h.Get(ctx, types.NamespacedName{
		Namespace: h.SystemNamespace,
		Name:      fleetconfig.ManagerConfigName,
	}, &deployment); err != nil {
		return fmt.Errorf("failed to get fleet-controller deployment: %w", err)
	}

	// Create local cluster
	cluster := &fleet.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "local",
			Namespace: config.Bootstrap.Namespace,
			Labels: map[string]string{
				"name": "local",
			},
		},
		Spec: fleet.ClusterSpec{
			KubeConfigSecret: secret.Name,
			AgentNamespace:   config.Bootstrap.AgentNamespace,
			AgentTolerations: deployment.Spec.Template.Spec.Tolerations,
		},
	}
	if err := h.createOrUpdate(ctx, cluster); err != nil {
		return fmt.Errorf("failed to create local cluster: %w", err)
	}

	// Create default cluster group
	clusterGroup := &fleet.ClusterGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default",
			Namespace: config.Bootstrap.Namespace,
		},
		Spec: fleet.ClusterGroupSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"name": "local",
				},
			},
		},
	}
	if err := h.createOrUpdate(ctx, clusterGroup); err != nil {
		return fmt.Errorf("failed to create default cluster group: %w", err)
	}

	// Create bootstrap GitRepo if configured
	if config.Bootstrap.Repo != "" {
		var paths []string
		if len(config.Bootstrap.Paths) > 0 {
			paths = pathSplitter.Split(config.Bootstrap.Paths, -1)
		}

		gitRepo := &fleet.GitRepo{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "bootstrap",
				Namespace: config.Bootstrap.Namespace,
			},
			Spec: fleet.GitRepoSpec{
				Repo:             config.Bootstrap.Repo,
				Branch:           config.Bootstrap.Branch,
				ClientSecretName: config.Bootstrap.Secret,
				Paths:            paths,
			},
		}
		if err := h.createOrUpdate(ctx, gitRepo); err != nil {
			return fmt.Errorf("failed to create bootstrap GitRepo: %w", err)
		}
	}

	logger.Info("Successfully reconciled bootstrap resources", "namespace", config.Bootstrap.Namespace)
	return nil
}

// createOrUpdate creates or updates a resource
// For namespaces and some resources, we avoid deletion by using NoDeleteGVK logic
func (h *BootstrapHandler) createOrUpdate(ctx context.Context, obj client.Object) error {
	// Check if object exists
	existing := obj.DeepCopyObject().(client.Object)
	err := h.Get(ctx, client.ObjectKeyFromObject(obj), existing)

	if apierrors.IsNotFound(err) {
		// Create the object
		return h.Create(ctx, obj)
	} else if err != nil {
		return err
	}

	// For NoDeleteGVK types (namespaces), skip if it already exists
	// This mimics the wrangler apply.WithNoDeleteGVK behavior
	gvk := obj.GetObjectKind().GroupVersionKind()
	if gvk.Group == "" && gvk.Kind == "Namespace" {
		// Don't update/delete existing namespaces
		return nil
	}

	// Update existing object - merge the spec
	obj.SetResourceVersion(existing.GetResourceVersion())
	return h.Update(ctx, obj)
}

// buildSecret creates the kubeconfig secret for the local cluster
func (h *BootstrapHandler) buildSecret(ctx context.Context, bootstrapNamespace string) (*corev1.Secret, error) {
	rawConfig, err := h.ClientConfig.RawConfig()
	if err != nil {
		return nil, err
	}

	host, err := getHost(rawConfig)
	if err != nil {
		return nil, err
	}

	ca, err := getCA(rawConfig)
	if err != nil {
		return nil, err
	}

	token, err := h.getToken(ctx)
	if err != nil {
		return nil, err
	}

	value, err := buildKubeConfig(host, ca, token, rawConfig)
	if err != nil {
		return nil, err
	}

	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "local-cluster",
			Namespace: bootstrapNamespace,
			Labels: map[string]string{
				fleet.ManagedLabel: "true",
			},
		},
		Data: map[string][]byte{
			fleetconfig.KubeConfigSecretValueKey: value,
			fleetconfig.APIServerURLKey:          []byte(host),
			fleetconfig.APIServerCAKey:           ca,
		},
	}, nil
}

// getToken retrieves the service account token for bootstrap
func (h *BootstrapHandler) getToken(ctx context.Context) (string, error) {
	var sa corev1.ServiceAccount
	err := h.Get(ctx, types.NamespacedName{
		Namespace: h.SystemNamespace,
		Name:      FleetBootstrap,
	}, &sa)

	if apierrors.IsNotFound(err) {
		// Try in-cluster config
		icc, err := rest.InClusterConfig()
		if err == nil {
			return icc.BearerToken, nil
		}
		return "", nil
	} else if err != nil {
		return "", err
	}

	// kubernetes 1.24+ doesn't populate sa.Secrets
	if len(sa.Secrets) == 0 {
		// Need to create/get the token secret
		secret, err := h.getOrCreateServiceAccountTokenSecret(ctx, &sa)
		if err != nil {
			return "", err
		}
		return string(secret.Data[corev1.ServiceAccountTokenKey]), nil
	}

	// Get the existing secret
	var secret corev1.Secret
	err = h.Get(ctx, types.NamespacedName{
		Namespace: h.SystemNamespace,
		Name:      sa.Secrets[0].Name,
	}, &secret)
	if err != nil {
		return "", err
	}

	return string(secret.Data[corev1.ServiceAccountTokenKey]), nil
}

// getOrCreateServiceAccountTokenSecret handles K8s 1.24+ service account tokens
func (h *BootstrapHandler) getOrCreateServiceAccountTokenSecret(ctx context.Context, sa *corev1.ServiceAccount) (*corev1.Secret, error) {
	// Try to find existing token secret
	var secretList corev1.SecretList
	err := h.List(ctx, &secretList, client.InNamespace(sa.Namespace))
	if err != nil {
		return nil, err
	}

	// Look for a secret that references this service account
	for _, secret := range secretList.Items {
		if secret.Type == corev1.SecretTypeServiceAccountToken &&
			secret.Annotations[corev1.ServiceAccountNameKey] == sa.Name {
			if len(secret.Data[corev1.ServiceAccountTokenKey]) > 0 {
				return &secret, nil
			}
		}
	}

	// Create a new token secret
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-token", sa.Name),
			Namespace: sa.Namespace,
			Annotations: map[string]string{
				corev1.ServiceAccountNameKey: sa.Name,
			},
		},
		Type: corev1.SecretTypeServiceAccountToken,
	}

	if err := h.Create(ctx, secret); err != nil && !apierrors.IsAlreadyExists(err) {
		return nil, err
	}

	// Get the created secret to check if token is populated
	if err := h.Get(ctx, client.ObjectKeyFromObject(secret), secret); err != nil {
		return nil, err
	}

	if len(secret.Data[corev1.ServiceAccountTokenKey]) == 0 {
		return nil, errors.New("service account token not yet populated")
	}

	return secret, nil
}
