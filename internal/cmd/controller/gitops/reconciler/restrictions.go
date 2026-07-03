package reconciler

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"

	apimeta "k8s.io/apimachinery/pkg/api/meta"

	"github.com/rancher/fleet/internal/cmd/controller/labelselectors"
	"github.com/rancher/fleet/internal/cmd/controller/policyrestrictions"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// AuthorizeAndAssignDefaults applies restrictions from both GitRepoRestriction and
// Policy objects in the same namespace, then mutates the GitRepo with resolved defaults.
func AuthorizeAndAssignDefaults(ctx context.Context, c client.Client, gitrepo *fleet.GitRepo) error {
	restrictions := &fleet.GitRepoRestrictionList{}
	if err := c.List(ctx, restrictions, client.InNamespace(gitrepo.Namespace)); err != nil {
		return err
	}

	policies := &fleet.PolicyList{}
	if err := c.List(ctx, policies, client.InNamespace(gitrepo.Namespace)); err != nil {
		if apimeta.IsNoMatchError(err) {
			return nil
		}
		return err
	}

	if len(restrictions.Items) == 0 && len(policies.Items) == 0 {
		return nil
	}

	if len(restrictions.Items) > 0 {
		log.FromContext(ctx).Info(
			"GitRepoRestriction will be removed in the next minor release (Fleet v0.18.0 / Rancher v2.17.0); All restrictions must migrate to Policy. " +
				"See https://fleet.rancher.io/next/how-tos-for-operators/tenant-setup#_migration_from_gitreporestriction")
	}

	grr := aggregate(restrictions.Items)
	pol := policyrestrictions.Aggregate(policies.Items)

	// Merge defaults: GitRepoRestriction wins over Policy for first-non-empty.
	defaultSA := firstNonEmpty(grr.DefaultServiceAccount, pol.GitDefaultServiceAccount)
	defaultClientSecret := firstNonEmpty(grr.DefaultClientSecretName, pol.GitDefaultClientSecretName)

	// Union allow-lists from both sources.
	allowedSAs := slices.Concat(grr.AllowedServiceAccounts, pol.AllowedServiceAccounts)
	allowedClientSecrets := slices.Concat(grr.AllowedClientSecretNames, pol.GitAllowedClientSecretNames)
	// Repo patterns are kept separate: GitRepoRestriction patterns are evaluated
	// unanchored (backward-compatible), while Policy patterns are anchored (^(?:...)$).
	// A repo is allowed if it passes either list.
	allowedTargetNS := grr.AllowedTargetNamespaces
	mergedSelector := grr.AllowedTargetNamespaceSelector

	if len(allowedTargetNS) > 0 && gitrepo.Spec.TargetNamespace == "" {
		return errors.New("empty targetNamespace denied, because allowedTargetNamespaces restriction is present")
	}
	if mergedSelector != nil && gitrepo.Spec.TargetNamespace == "" {
		return errors.New("empty targetNamespace denied, because allowedTargetNamespaceSelector restriction is present")
	}

	targetNamespace, err := isAllowed(gitrepo.Spec.TargetNamespace, "", allowedTargetNS)
	if err != nil {
		return fmt.Errorf("disallowed targetNamespace %s: %w", gitrepo.Spec.TargetNamespace, err)
	}

	serviceAccount, err := isAllowed(gitrepo.Spec.ServiceAccount, defaultSA, allowedSAs)
	if err != nil {
		return fmt.Errorf("disallowed serviceAccount %s: %w", gitrepo.Spec.ServiceAccount, err)
	}

	repo, err := isAllowedByRepo(gitrepo.Spec.Repo, grr.AllowedRepoPatterns, pol.GitAllowedRepoPatterns)
	if err != nil {
		return fmt.Errorf("disallowed repo %s: %w", gitrepo.Spec.Repo, err)
	}

	clientSecretName, err := isAllowed(gitrepo.Spec.ClientSecretName, defaultClientSecret, allowedClientSecrets)
	if err != nil {
		return fmt.Errorf("disallowed clientSecretName %s: %w", gitrepo.Spec.ClientSecretName, err)
	}

	// Policy: RequireServiceAccount is checked after all defaulting.
	if pol.RequireServiceAccount && serviceAccount == "" {
		return errors.New("serviceAccount is required by Policy but is not set")
	}

	// Write resolved values back to the GitRepo.
	gitrepo.Spec.TargetNamespace = targetNamespace
	gitrepo.Spec.ServiceAccount = serviceAccount
	gitrepo.Spec.Repo = repo
	gitrepo.Spec.ClientSecretName = clientSecretName

	return nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func aggregate(restrictions []fleet.GitRepoRestriction) (result fleet.GitRepoRestriction) {
	sort.Slice(restrictions, func(i, j int) bool {
		return restrictions[i].Name < restrictions[j].Name
	})
	for _, restriction := range restrictions {
		if result.DefaultServiceAccount == "" {
			result.DefaultServiceAccount = restriction.DefaultServiceAccount
		}
		if result.DefaultClientSecretName == "" {
			result.DefaultClientSecretName = restriction.DefaultClientSecretName
		}
		result.AllowedServiceAccounts = append(result.AllowedServiceAccounts, restriction.AllowedServiceAccounts...)
		result.AllowedClientSecretNames = append(result.AllowedClientSecretNames, restriction.AllowedClientSecretNames...)
		result.AllowedRepoPatterns = append(result.AllowedRepoPatterns, restriction.AllowedRepoPatterns...)
		result.AllowedTargetNamespaces = append(result.AllowedTargetNamespaces, restriction.AllowedTargetNamespaces...)
		result.AllowedTargetNamespaceSelector = labelselectors.Merge(result.AllowedTargetNamespaceSelector, restriction.AllowedTargetNamespaceSelector)
	}
	return result
}

func isAllowed(currentValue, defaultValue string, allowedValues []string) (string, error) {
	if currentValue == "" {
		return defaultValue, nil
	}
	if len(allowedValues) == 0 {
		return currentValue, nil
	}
	if slices.Contains(allowedValues, currentValue) {
		return currentValue, nil
	}

	return currentValue, fmt.Errorf("%s not in allowed set %v", currentValue, allowedValues)
}

func isAllowedByRegex(currentValue, defaultValue string, patterns []string) (string, error) {
	if currentValue == "" {
		return defaultValue, nil
	}
	if len(patterns) == 0 {
		return currentValue, nil
	}
	for _, pattern := range patterns {
		// for compatibility with previous versions, the patterns can match verbatim
		if pattern == currentValue {
			return currentValue, nil
		}

		p, err := regexp.Compile(pattern)
		if err != nil {
			return currentValue, fmt.Errorf("GitRepoRestriction failed to compile regex '%s': %w", pattern, err)
		}
		if p.MatchString(currentValue) {
			return currentValue, nil
		}
	}

	return currentValue, fmt.Errorf("%s not in allowed set %v", currentValue, patterns)
}

// isAllowedByRepo validates a repo URL against two separate allow-lists with different
// anchoring semantics. GitRepoRestriction patterns are evaluated unanchored (backward-
// compatible). Policy patterns are evaluated anchored via policyrestrictions.IsAllowedByRegex.
// An empty combined allow-list means no restriction. The repo passes if either non-empty
// list accepts it.
func isAllowedByRepo(repo string, grrPatterns, policyPatterns []string) (string, error) {
	if len(grrPatterns) == 0 && len(policyPatterns) == 0 {
		return repo, nil
	}
	var grrErr, polErr error
	if len(grrPatterns) > 0 {
		_, grrErr = isAllowedByRegex(repo, "", grrPatterns)
		if grrErr == nil {
			return repo, nil
		}
	}
	if len(policyPatterns) > 0 {
		_, polErr = policyrestrictions.IsAllowedByRegex(repo, "", policyPatterns)
		if polErr == nil {
			return repo, nil
		}
	}
	// Both lists were non-empty and both rejected — report the Policy error if
	// only Policy patterns were present, otherwise report the GRR error.
	if grrErr != nil {
		return repo, grrErr
	}
	return repo, polErr
}
