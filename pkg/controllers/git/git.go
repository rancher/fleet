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
	"github.com/rancher/fleet/pkg/display"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/summary"
	gitjob "github.com/rancher/gitjob/pkg/apis/gitjob.cattle.io/v1"
	v1 "github.com/rancher/gitjob/pkg/generated/controllers/gitjob.cattle.io/v1"
	"github.com/rancher/lasso/pkg/client"
	"github.com/rancher/wrangler/pkg/apply"
	"github.com/rancher/wrangler/pkg/genericcondition"
	"github.com/rancher/wrangler/pkg/kv"
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
	two = int32(2)
)

func Register(ctx context.Context,
	apply apply.Apply,
	gitJobs v1.GitJobController,
	bundleDeployments fleetcontrollers.BundleDeploymentController,
	gitRepoRestrictions fleetcontrollers.GitRepoRestrictionCache,
	bundles fleetcontrollers.BundleController,
	gitRepos fleetcontrollers.GitRepoController) {
	h := &handler{
		gitjobCache:         gitJobs.Cache(),
		bundleCache:         bundles.Cache(),
		bundles:             bundles,
		bundleDeployments:   bundleDeployments.Cache(),
		gitRepoRestrictions: gitRepoRestrictions,
		display:             display.NewFactory(bundles.Cache()),
	}

	gitRepos.OnChange(ctx, "gitjob-purge", h.DeleteOnChange)
	fleetcontrollers.RegisterGitRepoGeneratingHandler(ctx, gitRepos, apply, "Accepted", "gitjobs", h.OnChange, nil)
	relatedresource.Watch(ctx, "gitjobs",
		relatedresource.OwnerResolver(true, fleet.SchemeGroupVersion.String(), "GitRepo"), gitRepos, gitJobs)
	relatedresource.Watch(ctx, "gitjobs", resolveGitRepo, gitRepos, bundles)
}

func resolveGitRepo(namespace, name string, obj runtime.Object) ([]relatedresource.Key, error) {
	if bundle, ok := obj.(*fleet.Bundle); ok {
		repo := bundle.Labels[fleet.RepoLabel]
		if repo != "" {
			return []relatedresource.Key{{
				Namespace: bundle.Namespace,
				Name:      repo,
			}}, nil
		}
	}
	return nil, nil
}

type handler struct {
	shareClientFactory  client.SharedClientFactory
	gitjobCache         v1.GitJobCache
	bundleCache         fleetcontrollers.BundleCache
	bundles             fleetcontrollers.BundleClient
	gitRepoRestrictions fleetcontrollers.GitRepoRestrictionCache
	bundleDeployments   fleetcontrollers.BundleDeploymentCache
	display             *display.Factory
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

func (h *handler) DeleteOnChange(key string, gitrepo *fleet.GitRepo) (*fleet.GitRepo, error) {
	if gitrepo != nil {
		return gitrepo, nil
	}

	ns, name := kv.Split(key, "/")
	bundles, err := h.bundleCache.List(ns, labels.SelectorFromSet(labels.Set{
		fleet.RepoLabel: name,
	}))
	if err != nil {
		return nil, err
	}

	for _, bundle := range bundles {
		err := h.bundles.Delete(bundle.Namespace, bundle.Name, nil)
		if err != nil {
			return nil, err
		}
	}

	return nil, nil
}

func mergeConditions(existing, next []genericcondition.GenericCondition) []genericcondition.GenericCondition {
	result := make([]genericcondition.GenericCondition, 0, len(existing)+len(next))
	names := map[string]int{}
	for i, existing := range existing {
		result = append(result, existing)
		names[existing.Type] = i
	}
	for _, next := range next {
		if i, ok := names[next.Type]; ok {
			result[i] = next
		} else {
			result = append(result, next)
		}
	}
	return result
}

func (h *handler) OnChange(gitrepo *fleet.GitRepo, status fleet.GitRepoStatus) ([]runtime.Object, fleet.GitRepoStatus, error) {
	status.ObservedGeneration = gitrepo.Generation

	if gitrepo.Spec.Repo == "" {
		return nil, status, nil
	}

	gitrepo, err := h.authorizeAndAssignDefaults(gitrepo)
	if err != nil {
		return nil, status, err
	}

	status, err = h.setBundleStatus(gitrepo, status)
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
		status.Conditions = mergeConditions(status.Conditions, gitJob.Status.Conditions)
		status.GitJobStatus = gitJob.Status.JobStatus
	} else {
		status.Commit = ""
	}

	if status.GitJobStatus != "Current" {
		status.Display.State = "GitUpdating"
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

	saName := name.SafeConcatName("git", gitrepo.Name)

	bundleErrorState := ""
	if status.Summary.WaitApplied > 0 {
		bundleErrorState = "WaitApplied"
	}
	if status.Summary.ErrApplied > 0 {
		bundleErrorState = "ErrApplied"
	}
	status.Resources, status.ResourceErrors = h.display.Render(gitrepo.Namespace, gitrepo.Name, bundleErrorState)
	status = countResources(status)
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
				SyncInterval:          syncSeconds,
				ForceUpdateGeneration: gitrepo.Spec.ForceSyncGeneration,
				Git: gitjob.GitInfo{
					Credential: gitjob.Credential{
						ClientSecretName:      gitrepo.Spec.ClientSecretName,
						CABundle:              gitrepo.Spec.CABundle,
						InsecureSkipTLSverify: gitrepo.Spec.InsecureSkipTLSverify,
					},
					Provider: "polling",
					Repo:     gitrepo.Spec.Repo,
					Revision: rev,
					Branch:   branch,
				},
				JobSpec: batchv1.JobSpec{
					BackoffLimit: &two,
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
							SecurityContext: &corev1.PodSecurityContext{
								RunAsUser: &[]int64{1000}[0],
							},
							ServiceAccountName: saName,
							RestartPolicy:      corev1.RestartPolicyNever,
							Containers: []corev1.Container{
								{
									Name:            "fleet",
									Image:           config.Get().AgentImage,
									ImagePullPolicy: corev1.PullPolicy(config.Get().AgentImagePullPolicy),
									Command: append([]string{
										"log.sh",
										"fleet",
										"apply",
										"--targets-file=/run/config/targets.yaml",
										"--label=" + fleet.RepoLabel + "=" + gitrepo.Name,
										"--namespace", gitrepo.Namespace,
										"--service-account", gitrepo.Spec.ServiceAccount,
										fmt.Sprintf("--sync-generation=%d", gitrepo.Spec.ForceSyncGeneration),
										fmt.Sprintf("--paused=%v", gitrepo.Spec.Paused),
										"--target-namespace", gitrepo.Spec.TargetNamespace,
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
							NodeSelector: map[string]string{"kubernetes.io/os": "linux"},
							Tolerations: []corev1.Toleration{{
								Key:      "cattle.io/os",
								Operator: "Equal",
								Value:    "linux",
								Effect:   "NoSchedule",
							}},
						},
					},
				},
			},
		},
	}, status, nil
}

func countResources(status fleet.GitRepoStatus) fleet.GitRepoStatus {
	status.ResourceCounts = fleet.GitRepoResourceCounts{}

	for _, resource := range status.Resources {
		status.ResourceCounts.DesiredReady++
		switch resource.State {
		case "Ready":
			status.ResourceCounts.Ready++
		case "WaitApplied":
			status.ResourceCounts.WaitApplied++
		case "Modified":
			status.ResourceCounts.Modified++
		case "Orphan":
			status.ResourceCounts.Orphaned++
		case "Missing":
			status.ResourceCounts.Missing++
		case "Unknown":
			status.ResourceCounts.Unknown++
		default:
			status.ResourceCounts.NotReady++
		}
	}

	return status
}

func (h *handler) setBundleStatus(gitrepo *fleet.GitRepo, status fleet.GitRepoStatus) (fleet.GitRepoStatus, error) {
	if gitrepo.DeletionTimestamp != nil {
		return status, nil
	}

	bundleDeployments, err := h.bundleDeployments.List("", labels.SelectorFromSet(labels.Set{
		fleet.RepoLabel:                    gitrepo.Name,
		"fleet.cattle.io/bundle-namespace": gitrepo.Namespace,
	}))
	if err != nil {
		return status, err
	}

	status.Summary = fleet.BundleSummary{}

	sort.Slice(bundleDeployments, func(i, j int) bool {
		return bundleDeployments[i].UID < bundleDeployments[j].UID
	})

	var (
		maxState fleet.BundleState
		message  string
	)

	for _, app := range bundleDeployments {
		state := summary.GetDeploymentState(app)
		summary.IncrementState(&status.Summary, app.Name, state, summary.MessageFromDeployment(app), app.Status.ModifiedStatus, app.Status.NonReadyStatus)
		status.Summary.DesiredReady++
		if fleet.StateRank[state] > fleet.StateRank[maxState] {
			maxState = state
			message = summary.MessageFromDeployment(app)
		}
	}

	if maxState == fleet.Ready {
		maxState = ""
		message = ""
	}

	bundles, err := h.bundleCache.List(gitrepo.Namespace, labels.SelectorFromSet(labels.Set{
		fleet.RepoLabel: gitrepo.Name,
	}))
	if err != nil {
		return status, err
	}

	sort.Slice(bundles, func(i, j int) bool {
		return bundles[i].Name < bundles[j].Name
	})

	var (
		clustersDesiredReady int
		clustersReady        = -1
	)

	for _, bundle := range bundles {
		if bundle.Status.Summary.DesiredReady > 0 {
			clustersDesiredReady = bundle.Status.Summary.DesiredReady
			if clustersReady < 0 || bundle.Status.Summary.Ready < clustersReady {
				clustersReady = bundle.Status.Summary.Ready
			}
		}
	}

	if clustersReady < 0 {
		clustersReady = 0
	}

	status.Display.State = string(maxState)
	status.Display.Message = message
	status.Display.Error = len(message) > 0
	status.DesiredReadyClusters = clustersDesiredReady
	status.ReadyClusters = clustersReady
	summary.SetReadyConditions(&status, "Bundle", status.Summary)
	return status, nil
}
