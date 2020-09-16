package git

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"time"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/config"
	"github.com/rancher/fleet/pkg/controllers/clusterregistration"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/summary"
	gitjob "github.com/rancher/gitjob/pkg/apis/gitjob.cattle.io/v1"
	v1 "github.com/rancher/gitjob/pkg/generated/controllers/gitjob.cattle.io/v1"
	"github.com/rancher/wrangler/pkg/apply"
	"github.com/rancher/wrangler/pkg/name"
	"github.com/rancher/wrangler/pkg/relatedresource"
	"github.com/rancher/wrangler/pkg/yaml"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
)

var (
	RepoLabel = "fleet.cattle.io/repo-name"
)

func Register(ctx context.Context,
	apply apply.Apply,
	gitJobs v1.GitJobController,
	bundleDeployments fleetcontrollers.BundleDeploymentController,
	gitRepoRestrictions fleetcontrollers.GitRepoRestrictionCache,
	gitRepos fleetcontrollers.GitRepoController) {
	h := &handler{
		gitjobCache:         gitJobs.Cache(),
		bundleDeployments:   bundleDeployments.Cache(),
		gitRepoRestrictions: gitRepoRestrictions,
	}

	fleetcontrollers.RegisterGitRepoGeneratingHandler(ctx, gitRepos, apply, "Accepted", "gitjobs", h.OnChange, nil)
	relatedresource.Watch(ctx, "gitjobs",
		relatedresource.OwnerResolver(true, fleet.SchemeGroupVersion.String(), "GitRepo"), gitRepos, gitJobs)
	relatedresource.Watch(ctx, "gitjobs", resolveGitRepo, gitRepos, bundleDeployments)
}

func resolveGitRepo(namespace, name string, obj runtime.Object) ([]relatedresource.Key, error) {
	if bundleDeployment, ok := obj.(*fleet.BundleDeployment); ok {
		repo := bundleDeployment.Labels[RepoLabel]
		ns := bundleDeployment.Labels["fleet.cattle.io/bundle-namespace"]
		if repo != "" && ns != "" {
			return []relatedresource.Key{{
				Namespace: ns,
				Name:      repo,
			}}, nil
		}
	}
	return nil, nil
}

type handler struct {
	gitjobCache         v1.GitJobCache
	gitRepoRestrictions fleetcontrollers.GitRepoRestrictionCache
	bundleDeployments   fleetcontrollers.BundleDeploymentCache
}

func targetsOrDefault(targets []fleet.GitTarget) []fleet.GitTarget {
	if len(targets) == 0 {
		return []fleet.GitTarget{
			{
				Name:         "default",
				ClusterGroup: "default",
			},
		}
	}
	return targets
}

func (h *handler) getConfig(repo *fleet.GitRepo) (*corev1.ConfigMap, error) {
	spec := &fleet.BundleSpec{}
	for _, target := range targetsOrDefault(repo.Spec.Targets) {
		spec.Targets = append(spec.Targets, fleet.BundleTarget{
			Name:                 target.Name,
			ClusterSelector:      target.ClusterSelector,
			ClusterGroup:         target.ClusterGroup,
			ClusterGroupSelector: target.ClusterGroupSelector,
		})
		spec.TargetRestrictions = append(spec.TargetRestrictions, fleet.BundleTargetRestriction{
			Name:                 target.Name,
			ClusterSelector:      target.ClusterSelector,
			ClusterGroup:         target.ClusterGroup,
			ClusterGroupSelector: target.ClusterGroupSelector,
		})
	}
	data, err := json.Marshal(spec)
	if err != nil {
		return nil, err
	}

	hash := clusterregistration.KeyHash(string(data))
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name.SafeConcatName(repo.Name, "config", hash),
			Namespace: repo.Namespace,
		},
		BinaryData: map[string][]byte{
			"targets.yaml": data,
		},
	}, nil
}

func (h *handler) authorizeAndAssignDefaults(gitrepo *fleet.GitRepo) (*fleet.GitRepo, error) {
	restrictions, err := h.gitRepoRestrictions.List(gitrepo.Namespace, labels.Everything())
	if err != nil {
		return nil, err
	}

	if len(restrictions) == 0 {
		return gitrepo, nil
	}

	restriction := aggregate(restrictions)
	gitrepo = gitrepo.DeepCopy()

	gitrepo.Spec.ServiceAccount, err = isAllowed(gitrepo.Spec.ServiceAccount,
		restriction.DefaultServiceAccount,
		restriction.AllowedServiceAccounts,
		false)
	if err != nil {
		return nil, fmt.Errorf("disallowed serviceAcount %s: %w", gitrepo.Spec.ServiceAccount, err)
	}

	gitrepo.Spec.Repo, err = isAllowed(gitrepo.Spec.Repo,
		"",
		restriction.AllowedRepoPatterns,
		true)
	if err != nil {
		return nil, fmt.Errorf("disallowed repo %s: %w", gitrepo.Spec.ServiceAccount, err)
	}

	gitrepo.Spec.ClientSecretName, err = isAllowed(gitrepo.Spec.ClientSecretName,
		restriction.DefaultClientSecretName,
		restriction.AllowedClientSecretNames, false)
	if err != nil {
		return nil, fmt.Errorf("disallowed clientSecretName %s: %w", gitrepo.Spec.ServiceAccount, err)
	}

	return gitrepo, nil
}

func isAllowed(currentValue, defaultValue string, allowedValues []string, pattern bool) (string, error) {
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
		if !pattern {
			continue
		}
		p, err := regexp.Compile(allowedValue)
		if err != nil {
			return currentValue, err
		}
		if p.MatchString(allowedValue) {
			return currentValue, nil
		}
	}

	return currentValue, fmt.Errorf("%s not in allowed set %v", currentValue, allowedValues)
}

func aggregate(restrictions []*fleet.GitRepoRestriction) (result fleet.GitRepoRestriction) {
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
	}
	return
}

func (h *handler) OnChange(gitrepo *fleet.GitRepo, status fleet.GitRepoStatus) ([]runtime.Object, fleet.GitRepoStatus, error) {
	gitrepo, err := h.authorizeAndAssignDefaults(gitrepo)
	if err != nil {
		return nil, status, err
	}

	status.Conditions = nil
	status.ObservedGeneration = gitrepo.Generation

	status, err = h.setBundleDeploymentStatus(gitrepo, status)
	if err != nil {
		return nil, status, err
	}

	paths := gitrepo.Spec.Paths
	if len(paths) == 0 {
		paths = []string{"."}
	}

	gitJob, err := h.gitjobCache.Get(gitrepo.Namespace, gitrepo.Name)
	if err == nil {
		status.Commit = gitJob.Status.Commit
		status.Conditions = append(status.Conditions, gitJob.Status.Conditions...)
	} else {
		status.Commit = ""
		status.Conditions = nil
	}

	branch, rev := gitrepo.Spec.Branch, gitrepo.Spec.Revision
	if branch == "" && rev == "" {
		branch = "master"
	}

	configMap, err := h.getConfig(gitrepo)
	if err != nil {
		return nil, status, err
	}

	syncSeconds := 0
	if gitrepo.Spec.PollingInterval != nil {
		syncSeconds = int(gitrepo.Spec.PollingInterval.Duration / time.Second)
	}

	syncBefore := ""
	if gitrepo.Spec.ForceSyncBefore != nil {
		syncBefore = gitrepo.Spec.ForceSyncBefore.UTC().Format(time.RFC3339)
	}

	saName := name.SafeConcatName("git", gitrepo.Name)
	return []runtime.Object{
		configMap,
		&corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      saName,
				Namespace: gitrepo.Namespace,
			},
		},
		&rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{
				Name:      saName,
				Namespace: gitrepo.Namespace,
			},
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:     []string{"get", "create", "update"},
					APIGroups: []string{"fleet.cattle.io"},
					Resources: []string{"bundles"},
				},
				{
					Verbs:     []string{"get"},
					APIGroups: []string{"fleet.cattle.io"},
					Resources: []string{"gitrepos"},
				},
			},
		},
		&rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      saName,
				Namespace: gitrepo.Namespace,
			},
			Subjects: []rbacv1.Subject{
				{
					Kind:      "ServiceAccount",
					Name:      saName,
					Namespace: gitrepo.Namespace,
				},
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: "rbac.authorization.k8s.io",
				Kind:     "Role",
				Name:     saName,
			},
		},
		&gitjob.GitJob{
			ObjectMeta: metav1.ObjectMeta{
				Labels:      yaml.CleanAnnotationsForExport(gitrepo.Labels),
				Annotations: yaml.CleanAnnotationsForExport(gitrepo.Annotations),
				Name:        gitrepo.Name,
				Namespace:   gitrepo.Namespace,
			},
			Spec: gitjob.GitJobSpec{
				SyncInterval: syncSeconds,
				ForceUpdate:  gitrepo.Spec.ForceUpdate,
				Git: gitjob.GitInfo{
					Credential: gitjob.Credential{
						ClientSecretName: gitrepo.Spec.ClientSecretName,
					},
					Provider: "polling",
					Repo:     gitrepo.Spec.Repo,
					Revision: rev,
					Branch:   branch,
				},
				JobSpec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							CreationTimestamp: metav1.Time{Time: time.Unix(0, 0)},
						},
						Spec: corev1.PodSpec{
							Volumes: []corev1.Volume{
								{
									Name: "config",
									VolumeSource: corev1.VolumeSource{
										ConfigMap: &corev1.ConfigMapVolumeSource{
											LocalObjectReference: corev1.LocalObjectReference{
												Name: configMap.Name,
											},
										},
									},
								},
							},
							ServiceAccountName: saName,
							RestartPolicy:      corev1.RestartPolicyNever,
							Containers: []corev1.Container{
								{
									Name:            "fleet",
									Image:           config.Get().AgentImage,
									ImagePullPolicy: corev1.PullPolicy(config.Get().AgentImagePullPolicy),
									Command: append([]string{
										"fleet",
										"apply",
										"--targets-file=/run/config/targets.yaml",
										"--label=" + RepoLabel + "=" + gitrepo.Name,
										"--namespace", gitrepo.Namespace,
										"--service-account", gitrepo.Spec.ServiceAccount,
										"--sync-before", syncBefore,
										gitrepo.Name,
									}, paths...),
									WorkingDir: "/workspace/source",
									VolumeMounts: []corev1.VolumeMount{
										{
											Name:      "config",
											MountPath: "/run/config",
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}, status, nil
}

func (h *handler) setBundleDeploymentStatus(gitrepo *fleet.GitRepo, status fleet.GitRepoStatus) (fleet.GitRepoStatus, error) {
	if gitrepo.DeletionTimestamp != nil {
		return status, nil
	}

	bundleDeployments, err := h.bundleDeployments.List("", labels.SelectorFromSet(labels.Set{
		RepoLabel:                          gitrepo.Name,
		"fleet.cattle.io/bundle-namespace": gitrepo.Namespace,
	}))
	if err != nil {
		return status, err
	}

	status.Summary = fleet.BundleSummary{}

	sort.Slice(bundleDeployments, func(i, j int) bool {
		return bundleDeployments[i].Name < bundleDeployments[j].Name
	})

	var maxState fleet.BundleState
	for _, app := range bundleDeployments {
		state := summary.GetDeploymentState(app)
		summary.IncrementState(&status.Summary, app.Name, state, summary.MessageFromDeployment(app), app.Status.ModifiedStatus, app.Status.NonReadyStatus)
		status.Summary.DesiredReady++
		if fleet.StateRank[state] > fleet.StateRank[maxState] {
			maxState = state
		}
	}

	if maxState == fleet.Ready {
		maxState = ""
	}

	status.Display.State = string(maxState)
	summary.SetReadyConditions(&status, "Bundle", status.Summary)
	return status, nil
}
