package gitrepo

import (
	"context"
	"fmt"
	"regexp"
	"sort"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

func AuthorizeAndAssignDefaults(ctx context.Context, c client.Client, gitrepo *fleet.GitRepo) (*fleet.GitRepo, error) {
	restrictions := &fleet.GitRepoRestrictionList{}
	err := c.List(ctx, restrictions, client.InNamespace(gitrepo.Namespace))
	if err != nil {
		return nil, err
	}

	if len(restrictions.Items) == 0 {
		return gitrepo, nil
	}

	restriction := aggregate(restrictions.Items)
	gitrepo = gitrepo.DeepCopy()

	if len(restriction.AllowedTargetNamespaces) > 0 && gitrepo.Spec.TargetNamespace == "" {
		return nil, fmt.Errorf("empty targetNamespace denied, because allowedTargetNamespaces restriction is present")
	}

	gitrepo.Spec.TargetNamespace, err = isAllowed(gitrepo.Spec.TargetNamespace, "", restriction.AllowedTargetNamespaces)
	if err != nil {
		return nil, fmt.Errorf("disallowed targetNamespace %s: %w", gitrepo.Spec.TargetNamespace, err)
	}

	gitrepo.Spec.ServiceAccount, err = isAllowed(gitrepo.Spec.ServiceAccount,
		restriction.DefaultServiceAccount,
		restriction.AllowedServiceAccounts)
	if err != nil {
		return nil, fmt.Errorf("disallowed serviceAccount %s: %w", gitrepo.Spec.ServiceAccount, err)
	}

	gitrepo.Spec.Repo, err = isAllowedByRegex(gitrepo.Spec.Repo, "", restriction.AllowedRepoPatterns)
	if err != nil {
		return nil, fmt.Errorf("disallowed repo %s: %w", gitrepo.Spec.ServiceAccount, err)
	}

	gitrepo.Spec.ClientSecretName, err = isAllowed(gitrepo.Spec.ClientSecretName,
		restriction.DefaultClientSecretName,
		restriction.AllowedClientSecretNames)
	if err != nil {
		return nil, fmt.Errorf("disallowed clientSecretName %s: %w", gitrepo.Spec.ServiceAccount, err)
	}

	return gitrepo, nil
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
	}
	return
}

func isAllowed(currentValue, defaultValue string, allowedValues []string) (string, error) {
	if currentValue == "" {
		return defaultValue, nil
	}
	if len(allowedValues) == 0 {
		return currentValue, nil
	}
	for _, allowedValue := range allowedValues {
		if allowedValue == currentValue {
			return currentValue, nil
		}
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
