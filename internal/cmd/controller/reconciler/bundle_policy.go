// Copyright (c) 2021-2025 SUSE LLC

package reconciler

import (
	"context"
	"errors"
	"fmt"

	apimeta "k8s.io/apimachinery/pkg/api/meta"

	"github.com/rancher/fleet/internal/cmd/controller/policyrestrictions"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// AuthorizeBundle validates a Bundle against all Policy objects in the same
// namespace. It never mutates the Bundle. Returns a non-nil error when the
// Bundle violates any policy, which will be surfaced as a status condition.
// It is a no-op when no Policy objects exist in the namespace.
func AuthorizeBundle(ctx context.Context, c client.Client, bundle *fleet.Bundle) error {
	policies := &fleet.PolicyList{}
	if err := c.List(ctx, policies, client.InNamespace(bundle.Namespace)); err != nil {
		if apimeta.IsNoMatchError(err) {
			return nil
		}
		return err
	}

	if len(policies.Items) == 0 {
		return nil
	}

	pol := policyrestrictions.Aggregate(policies.Items)

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

	return nil
}
