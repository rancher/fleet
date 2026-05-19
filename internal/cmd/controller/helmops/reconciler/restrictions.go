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

// AuthorizeAndAssignDefaults validates a HelmOp against all Policy objects in the
// same namespace and mutates the HelmOp with resolved defaults.
// It is a no-op when no Policy objects exist in the namespace.
func AuthorizeAndAssignDefaults(ctx context.Context, c client.Client, helmop *fleet.HelmOp) error {
	policies := &fleet.PolicyList{}
	if err := c.List(ctx, policies, client.InNamespace(helmop.Namespace)); err != nil {
		if apimeta.IsNoMatchError(err) {
			return nil
		}
		return err
	}

	if len(policies.Items) == 0 {
		return nil
	}

	pol := policyrestrictions.Aggregate(policies.Items)

	// Apply HelmOp-specific defaults before running the top-level checks.
	if helmop.Spec.ServiceAccount == "" {
		helmop.Spec.ServiceAccount = pol.HelmDefaultServiceAccount
	}
	if helmop.Spec.HelmSecretName == "" {
		helmop.Spec.HelmSecretName = pol.HelmDefaultHelmSecretName
	}

	// Top-level: RequireServiceAccount.
	if pol.RequireServiceAccount && helmop.Spec.ServiceAccount == "" {
		return errors.New("serviceAccount is required by Policy but is not set")
	}

	// Top-level: AllowedServiceAccounts.
	if _, err := policyrestrictions.IsAllowed(helmop.Spec.ServiceAccount, "", pol.AllowedServiceAccounts); err != nil {
		return fmt.Errorf("disallowed serviceAccount %s: %w", helmop.Spec.ServiceAccount, err)
	}

	// HelmOp-specific: AllowedHelmSecretNames.
	if _, err := policyrestrictions.IsAllowed(helmop.Spec.HelmSecretName, "", pol.HelmAllowedHelmSecretNames); err != nil {
		return fmt.Errorf("disallowed helmSecretName %s: %w", helmop.Spec.HelmSecretName, err)
	}

	// HelmOp-specific: AllowedHelmRepoPatterns and AllowedChartPatterns.
	if helmop.Spec.Helm != nil {
		if _, err := policyrestrictions.IsAllowedByRegex(helmop.Spec.Helm.Repo, "", pol.HelmAllowedRepoPatterns); err != nil {
			return fmt.Errorf("disallowed helm repo %s: %w", helmop.Spec.Helm.Repo, err)
		}
		if _, err := policyrestrictions.IsAllowedByRegex(helmop.Spec.Helm.Chart, "", pol.HelmAllowedChartPatterns); err != nil {
			return fmt.Errorf("disallowed helm chart %s: %w", helmop.Spec.Helm.Chart, err)
		}
	}

	return nil
}
