// Copyright (c) 2021-2025 SUSE LLC

package reconciler

import (
	"context"
	"fmt"

	apimeta "k8s.io/apimachinery/pkg/api/meta"

	"github.com/rancher/fleet/internal/cmd/controller/policyrestrictions"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// AuthorizeAndAssignDefaults validates a HelmApp against all Policy objects in the
// same namespace and mutates the HelmApp with resolved defaults.
// It is a no-op when no Policy objects exist in the namespace.
func AuthorizeAndAssignDefaults(ctx context.Context, c client.Client, helmapp *fleet.HelmApp) error {
	policies := &fleet.PolicyList{}
	if err := c.List(ctx, policies, client.InNamespace(helmapp.Namespace)); err != nil {
		if apimeta.IsNoMatchError(err) {
			return nil
		}
		return err
	}

	if len(policies.Items) == 0 {
		return nil
	}

	pol := policyrestrictions.Aggregate(policies.Items)

	// Apply HelmApp-specific defaults before running the top-level checks.
	if helmapp.Spec.HelmSecretName == "" {
		helmapp.Spec.HelmSecretName = pol.HelmDefaultHelmSecretName
	}

	// HelmApp-specific: AllowedHelmSecretNames.
	if _, err := policyrestrictions.IsAllowed(helmapp.Spec.HelmSecretName, "", pol.HelmAllowedHelmSecretNames); err != nil {
		return fmt.Errorf("disallowed helmSecretName %s: %w", helmapp.Spec.HelmSecretName, err)
	}

	// HelmApp-specific: AllowedHelmRepoPatterns and AllowedChartPatterns.
	if helmapp.Spec.Helm != nil {
		if _, err := policyrestrictions.IsAllowedByRegex(helmapp.Spec.Helm.Repo, "", pol.HelmAllowedRepoPatterns); err != nil {
			return fmt.Errorf("disallowed helm repo %s: %w", helmapp.Spec.Helm.Repo, err)
		}
		if _, err := policyrestrictions.IsAllowedByRegex(helmapp.Spec.Helm.Chart, "", pol.HelmAllowedChartPatterns); err != nil {
			return fmt.Errorf("disallowed helm chart %s: %w", helmapp.Spec.Helm.Chart, err)
		}
	}

	return nil
}
