// Package policyrestrictions provides shared aggregation helpers for Fleet Policy enforcement.
// It is used by the GitRepo, HelmOp, and Bundle reconcilers.
package policyrestrictions

import (
	"fmt"
	"regexp"
	"slices"
	"sort"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

// Merged is the aggregated result of one or more Policy objects in a namespace.
type Merged struct {
	RequireServiceAccount  bool
	AllowedServiceAccounts []string

	// GitRepo-specific
	GitDefaultServiceAccount    string
	GitDefaultClientSecretName  string
	GitAllowedClientSecretNames []string
	GitAllowedRepoPatterns      []string

	// HelmOp-specific
	HelmDefaultServiceAccount  string
	HelmDefaultHelmSecretName  string
	HelmAllowedHelmSecretNames []string
	HelmAllowedRepoPatterns    []string
	HelmAllowedChartPatterns   []string
}

// Aggregate merges a slice of Policy objects into a single Merged value.
// Policies are processed in name order for determinism.
// Boolean fields use OR semantics; list fields are unioned; string defaults use first-non-empty.
func Aggregate(policies []fleet.Policy) Merged {
	sort.Slice(policies, func(i, j int) bool {
		return policies[i].Name < policies[j].Name
	})

	var m Merged
	for _, p := range policies {
		if p.RequireServiceAccount {
			m.RequireServiceAccount = true
		}
		m.AllowedServiceAccounts = append(m.AllowedServiceAccounts, p.AllowedServiceAccounts...)

		if p.GitRepo != nil {
			if m.GitDefaultServiceAccount == "" {
				m.GitDefaultServiceAccount = p.GitRepo.DefaultServiceAccount
			}
			if m.GitDefaultClientSecretName == "" {
				m.GitDefaultClientSecretName = p.GitRepo.DefaultClientSecretName
			}
			m.GitAllowedClientSecretNames = append(m.GitAllowedClientSecretNames, p.GitRepo.AllowedClientSecretNames...)
			m.GitAllowedRepoPatterns = append(m.GitAllowedRepoPatterns, p.GitRepo.AllowedRepoPatterns...)
		}

		if p.HelmOp != nil {
			if m.HelmDefaultServiceAccount == "" {
				m.HelmDefaultServiceAccount = p.HelmOp.DefaultServiceAccount
			}
			if m.HelmDefaultHelmSecretName == "" {
				m.HelmDefaultHelmSecretName = p.HelmOp.DefaultHelmSecretName
			}
			m.HelmAllowedHelmSecretNames = append(m.HelmAllowedHelmSecretNames, p.HelmOp.AllowedHelmSecretNames...)
			m.HelmAllowedRepoPatterns = append(m.HelmAllowedRepoPatterns, p.HelmOp.AllowedHelmRepoPatterns...)
			m.HelmAllowedChartPatterns = append(m.HelmAllowedChartPatterns, p.HelmOp.AllowedChartPatterns...)
		}
	}
	return m
}

// IsAllowed validates currentValue against an optional allowedValues list, applying defaultValue
// when currentValue is empty.
// Returns (resolved value, nil) on success, or (currentValue, error) when the value is disallowed.
func IsAllowed(currentValue, defaultValue string, allowedValues []string) (string, error) {
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

// IsAllowedByRegex validates currentValue against a list of regex patterns, applying defaultValue
// when currentValue is empty.
// Patterns may also match verbatim (for compatibility with plain-string allow-lists).
// Returns (resolved value, nil) on success, or (currentValue, error) when no pattern matches.
func IsAllowedByRegex(currentValue, defaultValue string, patterns []string) (string, error) {
	if currentValue == "" {
		return defaultValue, nil
	}
	if len(patterns) == 0 {
		return currentValue, nil
	}
	for _, pattern := range patterns {
		// Allow verbatim match for compatibility with plain-string allow-lists.
		if pattern == currentValue {
			return currentValue, nil
		}
		p, err := regexp.Compile("^(?:" + pattern + ")$")
		if err != nil {
			return currentValue, fmt.Errorf("policy failed to compile regex %q: %w", pattern, err)
		}
		if p.MatchString(currentValue) {
			return currentValue, nil
		}
	}
	return currentValue, fmt.Errorf("%s not in allowed set %v", currentValue, patterns)
}
