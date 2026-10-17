// Package clusterregistration implements manager-initiated and agent-initiated registration.
//
// Add or import downstream clusters / agents to Fleet and keep information
// from their registration (e.g. local cluster kubeconfig) up-to-date.
package clusterregistration

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/rancher/fleet/internal/cmd/controller/agentmanagement/controllers/resources"
	secretutil "github.com/rancher/fleet/internal/cmd/controller/agentmanagement/secret"
	"github.com/rancher/fleet/internal/config"
	"github.com/rancher/fleet/internal/names"
	"github.com/rancher/fleet/internal/registration"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/durations"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"

	"github.com/rancher/wrangler/v3/pkg/apply"
	corecontrollers "github.com/rancher/wrangler/v3/pkg/generated/controllers/core/v1"
	rbaccontrollers "github.com/rancher/wrangler/v3/pkg/generated/controllers/rbac/v1"
	"github.com/rancher/wrangler/v3/pkg/generic"
	"github.com/rancher/wrangler/v3/pkg/relatedresource"

	v1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
)

const (
	AgentCredentialSecretType     = "fleet.cattle.io/agent-credential" //nolint:gosec // not a credential
	clusterByClientID             = "clusterByClientID"
	clusterRegistrationByClientID = "clusterRegistrationByClientID"
	deleteSecretAfter             = durations.ClusterRegistrationDeleteDelay
)

type handler struct {
	systemNamespace             string
	systemRegistrationNamespace string
	clusterRegistration         fleetcontrollers.ClusterRegistrationController
	clusterCache                fleetcontrollers.ClusterCache
	clusters                    fleetcontrollers.ClusterClient
	serviceAccountCache         corecontrollers.ServiceAccountCache
	secretsCache                corecontrollers.SecretCache
	secrets                     corecontrollers.SecretController
	tokenCache                  fleetcontrollers.ClusterRegistrationTokenCache
	recorder                    record.EventRecorder
}

func Register(ctx context.Context,
	apply apply.Apply,
	systemNamespace string,
	systemRegistrationNamespace string,
	serviceAccount corecontrollers.ServiceAccountController,
	secret corecontrollers.SecretController,
	role rbaccontrollers.RoleController,
	roleBinding rbaccontrollers.RoleBindingController,
	clusterRegistration fleetcontrollers.ClusterRegistrationController,
	clusters fleetcontrollers.ClusterController,
	clusterRegistrationToken fleetcontrollers.ClusterRegistrationTokenController,
	recorder record.EventRecorder) {
	h := &handler{
		systemNamespace:             systemNamespace,
		systemRegistrationNamespace: systemRegistrationNamespace,
		clusterRegistration:         clusterRegistration,
		clusterCache:                clusters.Cache(),
		clusters:                    clusters,
		serviceAccountCache:         serviceAccount.Cache(),
		secrets:                     secret,
		secretsCache:                secret.Cache(),
		tokenCache:                  clusterRegistrationToken.Cache(),
		recorder:                    recorder,
	}

	fleetcontrollers.RegisterClusterRegistrationGeneratingHandler(ctx,
		clusterRegistration,
		apply.WithCacheTypes(serviceAccount,
			secret,
			role,
			roleBinding,
		),
		"",
		"cluster-registration",
		h.OnChange,
		&generic.GeneratingHandlerOptions{
			AllowClusterScoped: true,
		})

	secret.OnChange(ctx, "registration-expire", h.OnSecretChange)
	clusters.OnChange(ctx, "cluster-to-clusterregistration", h.OnCluster)
	clusters.Cache().AddIndexer(clusterByClientID, func(obj *fleet.Cluster) ([]string, error) {
		return []string{
			fmt.Sprintf("%s/%s", obj.Namespace, obj.Spec.ClientID),
		}, nil
	})
	clusterRegistration.Cache().AddIndexer(clusterRegistrationByClientID, func(obj *fleet.ClusterRegistration) ([]string, error) {
		return []string{
			fmt.Sprintf("%s/%s", obj.Namespace, obj.Spec.ClientID),
		}, nil
	})
	relatedresource.Watch(ctx, "sa-to-cluster-registration", saToClusterRegistration, clusterRegistration, serviceAccount)
	config.OnChange(ctx, h.OnConfig)
	clusterRegistrationToken.OnChange(ctx, "token-to-cluster-registration", h.OnTokenChange)
}

func (h *handler) enqueuePending(namespace string) error {
	requests, err := h.clusterRegistration.Cache().List(namespace, labels.Everything())
	if err != nil {
		return err
	}
	for _, request := range requests {
		if !request.Status.Granted && !skipClusterRegistration(request) {
			h.clusterRegistration.Enqueue(request.Namespace, request.Name)
		}
	}
	return nil
}

func (h *handler) OnConfig(cfg *config.Config) error {
	if !cfg.AllowClusterRegistrationTokenChange {
		return nil
	}
	return h.enqueuePending(metav1.NamespaceAll)
}

func (h *handler) OnTokenChange(_ string, token *fleet.ClusterRegistrationToken) (*fleet.ClusterRegistrationToken, error) {
	if token == nil {
		return nil, nil
	}
	return token, h.enqueuePending(token.Namespace)
}

func saToClusterRegistration(namespace, name string, obj runtime.Object) ([]relatedresource.Key, error) {
	if sa, ok := obj.(*v1.ServiceAccount); ok {
		ns := sa.Annotations[fleet.ClusterRegistrationNamespaceAnnotation]
		name := sa.Annotations[fleet.ClusterRegistrationAnnotation]
		if ns != "" && name != "" {
			return []relatedresource.Key{{
				Namespace: ns,
				Name:      name,
			}}, nil
		}
	}
	return nil, nil
}

func (h *handler) OnCluster(key string, cluster *fleet.Cluster) (*fleet.Cluster, error) {
	if cluster == nil || cluster.Status.Namespace == "" {
		return cluster, nil
	}

	crs, err := h.clusterRegistration.Cache().GetByIndex(clusterRegistrationByClientID,
		fmt.Sprintf("%s/%s", cluster.Namespace, cluster.Spec.ClientID))
	if err != nil {
		return nil, err
	}
	for _, cr := range crs {
		if !cr.Status.Granted {
			logrus.Infof("Namespace assigned to cluster '%s/%s' enqueues cluster registration '%s/%s'", cluster.Namespace, cluster.Name,
				cr.Namespace, cr.Name)
			h.clusterRegistration.Enqueue(cr.Namespace, cr.Name)
		}
	}

	return cluster, nil
}

func (h *handler) OnSecretChange(key string, secret *v1.Secret) (*v1.Secret, error) {
	if secret == nil || secret.Namespace != h.systemRegistrationNamespace ||
		secret.Labels[fleet.ClusterAnnotation] == "" {
		return secret, nil
	}

	if time.Since(secret.CreationTimestamp.Time) > deleteSecretAfter {
		logrus.Infof("Deleting expired registration secret %s/%s", secret.Namespace, secret.Name)
		return secret, h.secrets.Delete(secret.Namespace, secret.Name, nil)
	}

	h.secrets.EnqueueAfter(secret.Namespace, secret.Name, deleteSecretAfter/2)
	return secret, nil
}

func skipClusterRegistration(cr *fleet.ClusterRegistration) bool {
	if cr == nil {
		return true
	}
	if cr.Labels == nil {
		return false
	}
	if cr.Labels[fleet.ClusterManagementLabel] != "" {
		return true
	}
	return false
}

// OnChange creates the service account and roles for a cluster registration.
// The service account's token is deployed to the downstream cluster, via the
// fleet-secret. It allows the downstream fleet-agent to list
// bundledeployments and update their status in its own cluster namespace on upstream.
// It can also get content resources, but not list them. The name of content
// resources is random.
func (h *handler) OnChange(request *fleet.ClusterRegistration, status fleet.ClusterRegistrationStatus) ([]runtime.Object, fleet.ClusterRegistrationStatus, error) {
	if status.Granted {
		// only create the cluster for the request once
		return nil, status, generic.ErrSkip
	}
	if skipClusterRegistration(request) {
		return nil, status, generic.ErrSkip
	}

	cluster, err := h.createOrGetCluster(request)
	if h.reportTokenMismatch(err) {
		// Cluster changes or enabling token changes re-enqueue the request.
		return nil, status, generic.ErrSkip
	}
	if err != nil || cluster == nil {
		return nil, status, err
	}

	if cluster.Status.Namespace == "" {
		status.ClusterName = cluster.Name
		return nil, status, nil
	}

	// set the Cluster as owner of the cluster registration request
	// ownerFound is used to avoid calling update on request whenever OnChange is called
	ownerFound := false
	for _, owner := range request.OwnerReferences {
		if owner.Kind == "Cluster" && owner.Name == cluster.Name && owner.UID == cluster.UID {
			ownerFound = true
			break
		}
	}
	if !ownerFound {
		request.SetOwnerReferences([]metav1.OwnerReference{
			{
				APIVersion: fleet.SchemeGroupVersion.String(),
				Kind:       "Cluster",
				Name:       cluster.Name,
				UID:        cluster.UID,
			},
		})
		request, err = h.clusterRegistration.Update(request)
		if err != nil {
			return nil, status, err
		}
	}

	saName := names.SafeConcatName(request.Name, string(request.UID))
	sa, err := h.serviceAccountCache.Get(cluster.Status.Namespace, saName)
	if err != nil && apierrors.IsNotFound(err) {
		// create request service account if missing
		status.ClusterName = cluster.Name
		return []runtime.Object{requestSA(saName, cluster, request)}, status, nil
	} else if err != nil {
		return nil, status, fmt.Errorf("failed to retrieve service account from cache: %w", err)
	}

	// try to get request service account's token
	var secret *v1.Secret
	if secret, err = h.authorizeCluster(sa, cluster, request); err != nil {
		return nil, status, fmt.Errorf("failed to authorize cluster, cannot get service account token: %w", err)
	} else if secret == nil {
		status.ClusterName = cluster.Name
		logrus.Infof("Cluster registration request '%s/%s', cluster '%s/%s' not granted, waiting for service account token",
			request.Namespace, request.Name, cluster.Namespace, cluster.Name)
		return nil, status, nil
	}

	cluster, err = h.clusters.Get(cluster.Namespace, cluster.Name, metav1.GetOptions{})
	if err != nil {
		return nil, status, fmt.Errorf("retrieving cluster before granting registration: %w", err)
	}
	credToken, err := h.credentialToken(cluster, request)
	if err != nil {
		return nil, status, err
	}
	if credToken == nil {
		status.ClusterName = cluster.Name
		return []runtime.Object{requestSA(saName, cluster, request)}, status, nil
	}
	if err := h.confirmRegisteredToken(cluster, request); err != nil {
		// Retain the error even on a mismatch: the Cluster-change event that
		// would re-enqueue the request may already have been processed.
		h.reportTokenMismatch(err)
		return nil, status, err
	}

	// delete old cluster registrations
	crlist, _ := h.clusterRegistration.List(request.Namespace, metav1.ListOptions{})
	for _, creg := range crlist.Items {
		if shouldDelete(creg, *request) {
			logrus.Debugf("Deleting old clusterregistration '%s/%s', now at '%s'", creg.Namespace, creg.Name, request.Name)
			if err := h.clusterRegistration.Delete(creg.Namespace, creg.Name, nil); err != nil && !apierrors.IsNotFound(err) {
				return nil, status, err
			}
		}
	}

	// request is granted, create the registration secret and roles
	status.ClusterName = cluster.Name
	status.Granted = true

	logrus.Infof("Cluster registration request '%s/%s' granted, creating cluster, request service account, registration secret", request.Namespace, request.Name)

	objs := []runtime.Object{
		// the registration secret c-clientID-clientRandom
		secret,
		// Update the existing service account 'request-UID' in the
		// cluster namespace, e.g. 'cluster-fleet-default-NAME-ID'
		requestSA(saName, cluster, request),
		// Add role bindings to manage bundledeployments and contents,
		// the agent could previously only access clusterregistrations in
		// the cluster registration namespace (e.g. 'fleet-default'). See
		// clusterregistrationtoken controller for details.
		&rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{
				Name:      request.Name,
				Namespace: request.Namespace,
				Labels: map[string]string{
					fleet.ManagedLabel: "true",
				},
			},
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:         []string{"patch"},
					APIGroups:     []string{fleet.SchemeGroupVersion.Group},
					Resources:     []string{fleet.ClusterResourceNamePlural + "/status"},
					ResourceNames: []string{cluster.Name},
				},
			},
		},
		&rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      request.Name,
				Namespace: request.Namespace,
				Labels: map[string]string{
					fleet.ManagedLabel: "true",
				},
			},
			Subjects: []rbacv1.Subject{
				{
					Kind:      "ServiceAccount",
					Name:      saName,
					Namespace: cluster.Status.Namespace,
				},
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: rbacv1.GroupName,
				Kind:     "Role",
				Name:     request.Name,
			},
		},
		// cluster role "fleet-bundle-deployment" created when
		// fleet-controller starts
		&rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      request.Name,
				Namespace: cluster.Status.Namespace,
				Labels: map[string]string{
					fleet.ManagedLabel: "true",
				},
			},
			Subjects: []rbacv1.Subject{
				{
					Kind:      "ServiceAccount",
					Name:      saName,
					Namespace: cluster.Status.Namespace,
				},
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: rbacv1.GroupName,
				Kind:     "ClusterRole",
				Name:     resources.BundleDeploymentClusterRole,
			},
		},
		// cluster role "fleet-content" created when fleet-controller
		// starts
		&rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name: names.SafeConcatName(request.Name, "content"),
				Labels: map[string]string{
					fleet.ManagedLabel: "true",
				},
			},
			Subjects: []rbacv1.Subject{
				{
					Kind:      "ServiceAccount",
					Name:      saName,
					Namespace: cluster.Status.Namespace,
				},
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: rbacv1.GroupName,
				Kind:     "ClusterRole",
				Name:     resources.ContentClusterRole,
			},
		},
	}

	// Grant the registration token's service account access to the
	// single credential secret for this request.
	tokenSAName := names.SafeConcatName(credToken.Name, string(credToken.UID))
	credRoleName := names.SafeConcatName(request.Name, "creds")
	if cluster.Spec.KubeConfigSecret != "" {
		credRoleName = names.SafeConcatName(tokenSAName, "creds")
	}
	objs = append(objs,
		&rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{
				Name:      credRoleName,
				Namespace: h.systemRegistrationNamespace,
				Labels: map[string]string{
					fleet.ManagedLabel: "true",
				},
			},
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:         []string{"get"},
					APIGroups:     []string{""},
					Resources:     []string{"secrets"},
					ResourceNames: []string{secret.Name},
				},
			},
		},
		&rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      credRoleName,
				Namespace: h.systemRegistrationNamespace,
				Labels: map[string]string{
					fleet.ManagedLabel: "true",
				},
			},
			Subjects: []rbacv1.Subject{
				{
					Kind:      "ServiceAccount",
					Name:      tokenSAName,
					Namespace: request.Namespace,
				},
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: rbacv1.GroupName,
				Kind:     "Role",
				Name:     credRoleName,
			},
		},
	)

	return objs, status, nil
}

// shouldDelete returns true for any other cluster registration with the same clientID, but different random and older creation timestamp
func shouldDelete(creg fleet.ClusterRegistration, request fleet.ClusterRegistration) bool {
	return creg.Spec.ClientID == request.Spec.ClientID &&
		creg.Spec.ClientRandom != request.Spec.ClientRandom &&
		creg.Name != request.Name &&
		creg.CreationTimestamp.Time.Before(request.CreationTimestamp.Time)
}

func (h *handler) createOrGetCluster(request *fleet.ClusterRegistration) (*fleet.Cluster, error) {
	clusters, err := h.clusterCache.GetByIndex(clusterByClientID, fmt.Sprintf("%s/%s", request.Namespace, request.Spec.ClientID))
	if err == nil && len(clusters) > 0 {
		return h.existingCluster(clusters[0], request, true)
	} else if err != nil && !apierrors.IsNotFound(err) {
		return nil, err
	}

	clusterName := names.SafeConcatName("cluster", names.KeyHash(request.Spec.ClientID))
	if cluster, err := h.clusterCache.Get(request.Namespace, clusterName); err == nil {
		return h.existingCluster(cluster, request, true)
	} else if !apierrors.IsNotFound(err) {
		return nil, err
	}

	// need to create the cluster for agent initiated registration, local
	// and managed clusters would already exist
	labels := map[string]string{}
	if !config.Get().IgnoreClusterRegistrationLabels {
		labels = sanitizeClusterLabels(request)
	}
	labels[fleet.ClusterAnnotation] = clusterName

	// Record whatever token label the request carries, including an empty
	// one, so a later request for the same ClientID must match it exactly.
	annotations := map[string]string{fleet.RegisteredTokenAnnotation: request.Labels[fleet.RegistrationTokenLabel]}

	cluster, err := h.clusters.Create(&fleet.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:        clusterName,
			Namespace:   request.Namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: fleet.ClusterSpec{
			ClientID: request.Spec.ClientID,
		},
	})
	if apierrors.IsAlreadyExists(err) {
		cluster, err = h.clusters.Get(request.Namespace, clusterName, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		return h.existingCluster(cluster, request, false)
	}
	if err == nil {
		logrus.Infof("Created cluster %s/%s", request.Namespace, clusterName)
	}
	return cluster, err
}

// existingCluster returns cluster if it carries the request's ClientID and the
// request presents the token the cluster is registered with. cached reports
// whether cluster was read from the informer cache.
func (h *handler) existingCluster(cluster *fleet.Cluster, request *fleet.ClusterRegistration, cached bool) (*fleet.Cluster, error) {
	if cluster.Spec.ClientID != request.Spec.ClientID {
		// The cluster name is a hash of the ClientID, so this is a collision.
		return nil, fmt.Errorf("non-matching ClientID on cluster %s/%s got %s expected %s",
			cluster.Namespace, cluster.Name, cluster.Spec.ClientID, request.Spec.ClientID)
	}
	err := h.validateSameRegistrant(cluster, request)
	// A mismatch with the cached annotation may predate a change of the
	// annotation or kubeConfigSecret, so confirm it on the live Cluster.
	_, recorded := cluster.Annotations[fleet.RegisteredTokenAnnotation]
	if _, mismatch := errors.AsType[*registrantMismatchError](err); mismatch && cached && recorded {
		if cluster, err = h.clusters.Get(cluster.Namespace, cluster.Name, metav1.GetOptions{}); err != nil {
			return nil, err
		}
		err = h.validateSameRegistrant(cluster, request)
	}
	if err != nil {
		return nil, err
	}
	return cluster, nil
}

// sanitizeClusterLabels copies the agent-supplied ClusterLabels while dropping
// any key a downstream agent must not be able to assert. Keys under the
// reserved management.cattle.io/ and fleet.cattle.io/ prefixes are Rancher- and
// Fleet-managed and are trusted for cluster targeting.
func sanitizeClusterLabels(request *fleet.ClusterRegistration) map[string]string {
	labels := make(map[string]string, len(request.Spec.ClusterLabels))
	for k, v := range request.Spec.ClusterLabels {
		if isReservedLabel(k) {
			logrus.Warnf("ClusterRegistration %s/%s attempted to set reserved label %q; ignoring that label",
				request.Namespace, request.Name, k)
			continue
		}
		labels[k] = v
	}
	return labels
}

// isReservedLabel reports whether key falls under a reserved, controller-managed
// label prefix that an agent may not assert. These namespaces are managed by
// Rancher and Fleet respectively and are trusted for cluster targeting (e.g.
// management.cattle.io/cluster-display-name is consumed by clusterName
// selectors); letting an agent set them would allow a registrant to spoof
// another cluster's identity and attract its BundleDeployments. The debugging
// label fleet.cattle.io/created-by-agent-pod is explicitly allowed.
func isReservedLabel(key string) bool {
	if key == fleet.CreatedByAgentPodLabel {
		return false
	}
	return strings.HasPrefix(key, fleet.ManagementLabelPrefix) || strings.HasPrefix(key, fleet.FleetLabelPrefix)
}

// registrantMismatchError reports a ClusterRegistration whose token differs
// from the one its Cluster is registered with.
type registrantMismatchError struct {
	cluster    *fleet.Cluster
	request    *fleet.ClusterRegistration
	registered string
	requested  string
}

func (e *registrantMismatchError) Error() string {
	return fmt.Sprintf("clusterregistration %s/%s: clientID %q already registered on cluster %s/%s with registration token %q, request uses %q",
		e.request.Namespace, e.request.Name, e.request.Spec.ClientID, e.cluster.Namespace, e.cluster.Name, e.registered, e.requested)
}

// tokenMismatch returns a registrantMismatchError if request does not present
// the registered token.
func tokenMismatch(cluster *fleet.Cluster, request *fleet.ClusterRegistration, registered string) error {
	if requested := request.Labels[fleet.RegistrationTokenLabel]; requested != registered {
		return &registrantMismatchError{cluster: cluster, request: request, registered: registered, requested: requested}
	}
	return nil
}

// reportTokenMismatch logs and records a Warning event if err is a
// registrantMismatchError, and reports whether it was one.
func (h *handler) reportTokenMismatch(err error) bool {
	var e *registrantMismatchError
	if !errors.As(err, &e) {
		return false
	}
	logrus.WithError(e).Error("ClusterRegistration not granted")
	h.recorder.Eventf(e.cluster, v1.EventTypeWarning, "RegistrationTokenMismatch",
		"ClusterRegistration %s uses registration token %q, but the cluster is registered with %q; set annotation %s=%s on the cluster to allow it",
		e.request.Name, e.requested, e.registered, fleet.RegisteredTokenAnnotation, e.requested)
	return true
}

// validateSameRegistrant returns an error for a ClusterRegistration of an already
// registered ClientID if it presents a different registration token than the
// one the cluster is registered with.
func (h *handler) validateSameRegistrant(cluster *fleet.Cluster, request *fleet.ClusterRegistration) error {
	if config.Get().AllowClusterRegistrationTokenChange {
		return nil
	}
	registered, ok, err := h.registeredToken(cluster)
	if err != nil || !ok {
		return err
	}
	return tokenMismatch(cluster, request, registered)
}

// registeredToken returns the registration token a Cluster is bound to.
// Clusters with kubeConfigSecret are not bound, even if they were created by
// an agent: Fleet deploys their agent with the import token. Agent-initiated
// clusters without the annotation, e.g. registered by an older release, are
// bound to the token of their latest granted registration. If several were
// granted within that second with different tokens, no request matches until
// the annotation is set.
func (h *handler) registeredToken(cluster *fleet.Cluster) (string, bool, error) {
	if cluster.Spec.KubeConfigSecret != "" {
		return "", false, nil
	}
	if token, ok := cluster.Annotations[fleet.RegisteredTokenAnnotation]; ok {
		return token, true, nil
	}

	crs, err := h.clusterRegistration.Cache().GetByIndex(clusterRegistrationByClientID,
		fmt.Sprintf("%s/%s", cluster.Namespace, cluster.Spec.ClientID))
	if err != nil {
		return "", false, err
	}
	var (
		latest metav1.Time
		tokens []string
	)
	for _, cr := range crs {
		if !cr.Status.Granted {
			continue
		}
		token := cr.Labels[fleet.RegistrationTokenLabel]
		switch {
		case len(tokens) == 0 || latest.Before(&cr.CreationTimestamp):
			latest, tokens = cr.CreationTimestamp, []string{token}
		case latest.Equal(&cr.CreationTimestamp) && !slices.Contains(tokens, token):
			tokens = append(tokens, token)
		}
	}
	// Read after the history lookup so a binding persisted before history
	// cleanup remains visible even when the cluster cache is behind.
	current, err := h.clusters.Get(cluster.Namespace, cluster.Name, metav1.GetOptions{})
	if err != nil {
		return "", false, err
	}
	if current.Spec.KubeConfigSecret != "" {
		return "", false, nil
	}
	if token, ok := current.Annotations[fleet.RegisteredTokenAnnotation]; ok {
		return token, true, nil
	}
	switch len(tokens) {
	case 0:
		return "", false, nil
	case 1:
		if err := h.recordRegisteredToken(current, tokens[0]); err != nil {
			return "", false, err
		}
		return tokens[0], true, nil
	default:
		// Timestamps have one-second resolution, so the latest granted
		// registrations cannot be ordered. Token names cannot contain commas:
		// the joined names match no request until the annotation selects one.
		slices.Sort(tokens)
		return strings.Join(tokens, ","), true, nil
	}
}

// confirmRegisteredToken checks the same live Cluster snapshot used to
// select the credential token. A Cluster without a recorded token is bound
// to the request's token.
func (h *handler) confirmRegisteredToken(cluster *fleet.Cluster, request *fleet.ClusterRegistration) error {
	if cluster.Spec.KubeConfigSecret != "" {
		return nil
	}
	registered, ok := cluster.Annotations[fleet.RegisteredTokenAnnotation]
	if !ok {
		return h.recordRegisteredToken(cluster, request.Labels[fleet.RegistrationTokenLabel])
	}
	if config.Get().AllowClusterRegistrationTokenChange {
		return nil
	}
	return tokenMismatch(cluster, request, registered)
}

// recordRegisteredToken sets the registered token annotation with an update
// checked against the resourceVersion of cluster, so that concurrent
// reconciles cannot record different tokens.
func (h *handler) recordRegisteredToken(cluster *fleet.Cluster, token string) error {
	cluster = cluster.DeepCopy()
	if cluster.Annotations == nil {
		cluster.Annotations = map[string]string{}
	}
	cluster.Annotations[fleet.RegisteredTokenAnnotation] = token
	if _, err := h.clusters.Update(cluster); err != nil {
		return fmt.Errorf("recording registration token on cluster %s/%s: %w",
			cluster.Namespace, cluster.Name, err)
	}
	return nil
}

// credentialToken returns the registration token whose service account may
// read the registration secret, or nil if that token does not exist. For a
// cluster Fleet deploys the agent to, this is always its import token, even
// if the request names another token.
func (h *handler) credentialToken(cluster *fleet.Cluster, request *fleet.ClusterRegistration) (*fleet.ClusterRegistrationToken, error) {
	tokenName := request.Labels[fleet.RegistrationTokenLabel]
	if cluster.Spec.KubeConfigSecret != "" {
		tokenName = names.SafeConcatName(config.ImportTokenPrefix + cluster.Name)
	}
	if tokenName == "" {
		logrus.Infof("ClusterRegistration '%s/%s' not granted: no %s label",
			request.Namespace, request.Name, fleet.RegistrationTokenLabel)
		return nil, nil
	}

	token, err := h.tokenCache.Get(request.Namespace, tokenName)
	if apierrors.IsNotFound(err) {
		logrus.Infof("ClusterRegistration '%s/%s' not granted: registration token %s/%s not found",
			request.Namespace, request.Name, request.Namespace, tokenName)
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("looking up registration token %s/%s: %w", request.Namespace, tokenName, err)
	}
	return token, nil
}

func (h *handler) authorizeCluster(sa *v1.ServiceAccount, cluster *fleet.Cluster, req *fleet.ClusterRegistration) (*v1.Secret, error) {
	var secret *v1.Secret
	var err error
	if len(sa.Secrets) != 0 {
		secret, err = h.secretsCache.Get(sa.Namespace, sa.Secrets[0].Name)
		if apierrors.IsNotFound(err) {
			// secrets can be slow to propagate to the cache
			secret, err = h.secrets.Get(sa.Namespace, sa.Secrets[0].Name, metav1.GetOptions{})
		}
	} else {
		secret, err = secretutil.GetServiceAccountTokenSecret(sa, h.secrets)
	}
	if err != nil || secret == nil {
		return nil, err
	}
	return &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      registration.SecretName(req.Spec.ClientID, req.Spec.ClientRandom),
			Namespace: h.systemRegistrationNamespace,
			Labels: map[string]string{
				fleet.ClusterAnnotation: cluster.Name,
				fleet.ManagedLabel:      "true",
			},
		},
		Type: AgentCredentialSecretType,
		Data: map[string][]byte{
			"token":               secret.Data["token"],
			"deploymentNamespace": []byte(cluster.Status.Namespace),
			"clusterNamespace":    []byte(cluster.Namespace),
			"clusterName":         []byte(cluster.Name),
			"systemNamespace":     []byte(h.systemNamespace),
		},
	}, nil
}

func requestSA(saName string, cluster *fleet.Cluster, request *fleet.ClusterRegistration) *v1.ServiceAccount {
	return &v1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      saName,
			Namespace: cluster.Status.Namespace,
			Labels: map[string]string{
				fleet.ManagedLabel: "true",
			},
			Annotations: map[string]string{
				fleet.ClusterAnnotation:                      cluster.Name,
				fleet.ClusterRegistrationAnnotation:          request.Name,
				fleet.ClusterRegistrationNamespaceAnnotation: request.Namespace,
			},
		},
	}
}
