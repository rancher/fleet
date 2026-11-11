// Copyright (c) 2021-2025 SUSE LLC

package reconciler

import (
	"context"
	"errors"
	"fmt"
	"strings"

	apimeta "k8s.io/apimachinery/pkg/api/meta"

	fleetutil "github.com/rancher/fleet/internal/cmd/controller/errorutil"
	"github.com/rancher/fleet/internal/cmd/controller/policyrestrictions"
	"github.com/rancher/fleet/internal/experimental"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// AuthorizeBundle validates a Bundle against all Policy objects in the same
// namespace. It never mutates the Bundle. Returns a non-nil error when the
// Bundle violates any policy, which will be surfaced as a status condition.
// It is a no-op when no Policy objects exist in the namespace.
//
// A failure to list the Policy objects is not a violation: the check could not
// be completed, and the returned error wraps ErrRetryable.
func AuthorizeBundle(ctx context.Context, c client.Client, bundle *fleet.Bundle) error {
	pol, err := namespacePolicy(ctx, c, bundle.Namespace)
	if err != nil {
		return err
	}
	if pol == nil {
		return nil
	}

	// No defaulting at the Bundle level — there is no backstop.

	// RequireServiceAccount: the top-level ServiceAccount is the fallback for
	// every target that does not set its own, so it must always be present when
	// the policy requires one.
	if pol.RequireServiceAccount && bundle.Spec.ServiceAccount == "" {
		return errors.New("serviceAccount is required by Policy but is not set on the Bundle")
	}

	// AllowedServiceAccounts — check the top-level default.
	if _, err := policyrestrictions.IsAllowed(bundle.Spec.ServiceAccount, "", pol.AllowedServiceAccounts); err != nil {
		return fmt.Errorf("disallowed serviceAccount %s: %w", bundle.Spec.ServiceAccount, err)
	}

	// AllowedServiceAccounts — check per-target overrides.
	// A target that sets its own ServiceAccount bypasses the top-level default,
	// so each non-empty target SA must also be validated independently.
	for _, target := range bundle.Spec.Targets {
		sa := target.ServiceAccount
		if sa == "" {
			// Inherits the top-level SA which was already validated above.
			continue
		}
		if _, err := policyrestrictions.IsAllowed(sa, "", pol.AllowedServiceAccounts); err != nil {
			return fmt.Errorf("disallowed serviceAccount %s in target %s: %w", sa, target.Name, err)
		}
	}

	// DownstreamResources: the controller reads the source objects with its own
	// credentials, so a tenant could otherwise copy downstream any Secret or
	// ConfigMap in the namespace, including ones it cannot read. Only the
	// objects the Policy allows may be copied. Nothing is copied unless the
	// experimental flag is set, so the check only applies then.
	if experimental.CopyResourcesDownstreamEnabled() {
		if err := checkDownstreamResourcesAllowed(*pol, bundle.Spec.DownstreamResources); err != nil {
			return err
		}
		// Only the Bundle's own downstreamResources are copied on this
		// version, but checking the targets' too keeps the check complete
		// should they be copied later.
		for _, target := range bundle.Spec.Targets {
			if err := checkDownstreamResourcesAllowed(*pol, target.DownstreamResources); err != nil {
				return fmt.Errorf("%w in target %s", err, target.Name)
			}
		}
	}

	return nil
}

// namespacePolicy returns the aggregate of the Policy objects in namespace, or
// nil if there are none or the Policy CRD is not installed. A failure to list
// them is returned as a retryable error, as it says nothing about the Bundle.
func namespacePolicy(ctx context.Context, c client.Client, namespace string) (*policyrestrictions.Merged, error) {
	policies := &fleet.PolicyList{}
	if err := c.List(ctx, policies, client.InNamespace(namespace)); err != nil {
		if apimeta.IsNoMatchError(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("%w: failed to list policies in namespace %s: %w", fleetutil.ErrRetryable, namespace, err)
	}

	if len(policies.Items) == 0 {
		return nil, nil
	}

	pol := policyrestrictions.Aggregate(policies.Items)
	return &pol, nil
}

// checkDownstreamResourcesAllowed returns an error unless pol allows every
// Secret and ConfigMap in drs to be copied downstream. The check uses only the
// kind and name, so it does not reveal whether an object exists. Other kinds
// are left to the copy step, which rejects them.
func checkDownstreamResourcesAllowed(pol policyrestrictions.Merged, drs []fleet.DownstreamResource) error {
	if !pol.DownstreamResourceRestricted() {
		return nil
	}

	for _, dr := range drs {
		switch strings.ToLower(dr.Kind) {
		case "secret", "configmap":
		default:
			continue
		}

		ok, err := policyrestrictions.IsDownstreamResourceAllowed(dr.Kind, dr.Name, pol.AllowedDownstreamResources)
		if err != nil {
			return fmt.Errorf("downstream resource %s %s: %w", dr.Kind, dr.Name, err)
		}
		if !ok {
			return fmt.Errorf(
				"downstream resource %s %s is not allowed by Policy: it is not listed in allowedDownstreamResources",
				dr.Kind, dr.Name,
			)
		}
	}

	return nil
}
