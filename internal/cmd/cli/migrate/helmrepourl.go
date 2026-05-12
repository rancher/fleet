// Package migrate contains one-time migration functions that run as Helm hooks
// during fleet upgrades. Each function checks a marker ConfigMap to avoid
// re-running when the upgrade job is re-triggered.
package migrate

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

const (
	helmURLRegexMigrationConfigMap = "fleet-helm-url-regex-migrated"

	// HelmRegexAutoMigratedAnnotation marks a GitRepo whose helmRepoURLRegex
	// was derived automatically from its existing Bundles during upgrade.
	// Admins should review and replace the generated regex with a tighter
	// pattern specific to their Helm repository.
	HelmRegexAutoMigratedAnnotation = "fleet.cattle.io/helm-regex-auto-migrated"
)

// GitRepoHelmURLRegex sets helmRepoURLRegex on every GitRepo that has a Helm
// credential secret but no regex. The regex is derived from the scheme+host of
// the Helm repository URLs already stored in the GitRepo's existing Bundles.
//
// A ConfigMap in systemNamespace is created after the migration completes;
// its presence prevents the migration from running again on a subsequent
// upgrade.
func GitRepoHelmURLRegex(ctx context.Context, cl client.Client, systemNamespace string) error {
	logger := log.FromContext(ctx).WithName("helmurlregex-migration")

	marker := &corev1.ConfigMap{}
	err := cl.Get(ctx, client.ObjectKey{
		Namespace: systemNamespace,
		Name:      helmURLRegexMigrationConfigMap,
	}, marker)
	if err == nil {
		logger.V(1).Info("Helm URL regex migration already completed, skipping")
		return nil
	}
	if !k8serrors.IsNotFound(err) {
		return fmt.Errorf("checking helm URL regex migration marker: %w", err)
	}

	logger.Info("Running Helm URL regex migration")
	if err := migrateAllGitRepos(ctx, cl); err != nil {
		return err
	}

	marker = &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      helmURLRegexMigrationConfigMap,
			Namespace: systemNamespace,
			Annotations: map[string]string{
				"fleet.cattle.io/migration": "Marks that the one-time helmRepoURLRegex migration has run. Do not delete.",
			},
		},
	}
	if err := cl.Create(ctx, marker); err != nil && !k8serrors.IsAlreadyExists(err) {
		return fmt.Errorf("creating helm URL regex migration marker: %w", err)
	}

	logger.Info("Helm URL regex migration completed")
	return nil
}

func migrateAllGitRepos(ctx context.Context, cl client.Client) error {
	logger := log.FromContext(ctx).WithName("helmurlregex-migration")

	list := &v1alpha1.GitRepoList{}
	if err := cl.List(ctx, list); err != nil {
		return fmt.Errorf("listing GitRepos for migration: %w", err)
	}

	var migrationErr error
	for i := range list.Items {
		gr := &list.Items[i]
		if !needsHelmURLRegexMigration(gr) {
			continue
		}
		if err := migrateOne(ctx, cl, gr); err != nil {
			logger.Error(err, "Failed to migrate GitRepo; continuing with remaining GitRepos",
				"gitrepo", gr.Namespace+"/"+gr.Name)
			migrationErr = errors.Join(migrationErr, err)
		}
	}
	return migrationErr
}

func needsHelmURLRegexMigration(gr *v1alpha1.GitRepo) bool {
	hasSecret := gr.Spec.HelmSecretName != "" || gr.Spec.HelmSecretNameForPaths != ""
	return hasSecret && gr.Spec.HelmRepoURLRegex == ""
}

func migrateOne(ctx context.Context, cl client.Client, gr *v1alpha1.GitRepo) error {
	logger := log.FromContext(ctx).WithName("helmurlregex-migration").
		WithValues("gitrepo", gr.Namespace+"/"+gr.Name)

	regex, err := deriveHelmRepoURLRegex(ctx, cl, gr)
	if err != nil {
		return err
	}

	if regex == "" {
		logger.Info("No Helm repository URLs found in existing Bundles; " +
			"helmRepoURLRegex left empty — credentials will not be forwarded. " +
			"Set helmRepoURLRegex on the GitRepo manually to restore credential forwarding.")
		return nil
	}

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current := &v1alpha1.GitRepo{}
		if err := cl.Get(ctx, client.ObjectKeyFromObject(gr), current); err != nil {
			return err
		}
		if !needsHelmURLRegexMigration(current) {
			return nil
		}
		if current.Annotations == nil {
			current.Annotations = map[string]string{}
		}
		current.Annotations[HelmRegexAutoMigratedAnnotation] = "true"
		current.Spec.HelmRepoURLRegex = regex
		logger.Info("Setting helmRepoURLRegex from existing Bundles",
			"helmRepoURLRegex", regex,
			"note", "review and tighten this auto-derived regex if the pattern is broader than necessary")
		return cl.Update(ctx, current)
	})
}

// deriveHelmRepoURLRegex lists existing Bundles for the GitRepo and builds a
// regex anchored at the start that matches the scheme+host of every Helm
// repository URL found. Returns "" if no Helm URLs are present in the Bundles.
func deriveHelmRepoURLRegex(ctx context.Context, cl client.Client, gr *v1alpha1.GitRepo) (string, error) {
	list := &v1alpha1.BundleList{}
	if err := cl.List(ctx, list,
		client.MatchingLabels{v1alpha1.RepoLabel: gr.Name},
		client.InNamespace(gr.Namespace),
	); err != nil {
		return "", fmt.Errorf("listing Bundles for %s/%s: %w", gr.Namespace, gr.Name, err)
	}

	seen := map[string]struct{}{}
	var prefixes []string
	for i := range list.Items {
		for _, rawURL := range collectBundleHelmURLs(&list.Items[i]) {
			p := helmURLToRegexPrefix(rawURL)
			if p == "" {
				continue
			}
			if _, ok := seen[p]; !ok {
				seen[p] = struct{}{}
				prefixes = append(prefixes, p)
			}
		}
	}

	if len(prefixes) == 0 {
		return "", nil
	}
	if len(prefixes) == 1 {
		return "^" + prefixes[0], nil
	}
	return "^(" + strings.Join(prefixes, "|") + ")", nil
}

// collectBundleHelmURLs returns all Helm repository URLs stored in a Bundle,
// covering the top-level options and any per-target customizations.
func collectBundleHelmURLs(bundle *v1alpha1.Bundle) []string {
	var urls []string
	urls = append(urls, helmURLsFromOptions(bundle.Spec.Helm)...)
	for i := range bundle.Spec.Targets {
		urls = append(urls, helmURLsFromOptions(bundle.Spec.Targets[i].Helm)...)
	}
	return urls
}

// helmURLsFromOptions extracts Helm repository URLs from a HelmOptions struct.
// For the Repo field this is always a URL; for Chart it is only a URL when it
// uses the oci:// scheme (otherwise it is just a chart name or path).
func helmURLsFromOptions(h *v1alpha1.HelmOptions) []string {
	if h == nil {
		return nil
	}
	var urls []string
	if h.Repo != "" {
		urls = append(urls, h.Repo)
	}
	if strings.HasPrefix(h.Chart, "oci://") {
		urls = append(urls, h.Chart)
	}
	return urls
}

// helmURLToRegexPrefix parses rawURL and returns a regexp.QuoteMeta-escaped
// string of the form "scheme://host/" that can be prefixed with "^" to form a
// safe, anchored regex. Returns "" if rawURL cannot be parsed or has no host.
func helmURLToRegexPrefix(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	// regexp.QuoteMeta escapes dots and other regex metacharacters in the host.
	return regexp.QuoteMeta(u.Scheme + "://" + u.Host + "/")
}
