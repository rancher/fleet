package job

import (
	"context"
	"sort"
	"strconv"
	"strings"

	gitjobv1 "github.com/rancher/gitjob/pkg/apis/gitjob.cattle.io/v1"
	gitjobv1controller "github.com/rancher/gitjob/pkg/generated/controllers/gitjob.cattle.io/v1"
	"github.com/rancher/gitjob/pkg/types"
	"github.com/rancher/wrangler/pkg/condition"
	corev1 "github.com/rancher/wrangler/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/pkg/kstatus"
	v1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	labels2 "k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/cli-utils/pkg/kstatus/status"
)

func Register(ctx context.Context, cont *types.Context) {
	h := jobHandler{
		gitjobs: cont.Gitjob.Gitjob().V1().GitJob(),
		pods:    cont.Core.Core().V1().Pod().Cache(),
	}

	cont.Batch.Batch().V1().Job().OnChange(ctx, "sync-job-status", h.sync)
}

type jobHandler struct {
	gitjobs gitjobv1controller.GitJobController
	pods    corev1.PodCache
}

func (j jobHandler) sync(_ string, obj *v1.Job) (*v1.Job, error) {
	if obj == nil {
		return nil, nil
	}
	data, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return nil, err
	}
	us := &unstructured.Unstructured{
		Object: data,
	}
	result, err := status.Compute(us)
	if err != nil {
		return nil, err
	}

	for _, or := range obj.OwnerReferences {
		if or.APIVersion == gitjobv1.SchemeGroupVersion.String() && or.Kind == "GitJob" {
			gitjob, err := j.gitjobs.Cache().Get(obj.Namespace, or.Name)
			if err != nil {
				return nil, err
			}
			if strconv.Itoa(int(gitjob.Generation)) != obj.Annotations["generation"] {
				continue
			}
			gitjob.Status.JobStatus = result.Status.String()
			for _, con := range result.Conditions {
				condition.Cond(con.Type.String()).SetStatus(gitjob, string(con.Status))
				condition.Cond(con.Type.String()).SetMessageIfBlank(gitjob, con.Message)
				condition.Cond(con.Type.String()).Reason(gitjob, con.Reason)
			}

			if result.Status == status.FailedStatus {
				selector := labels2.SelectorFromSet(labels2.Set{
					"job-name": obj.Name,
				})
				pods, err := j.pods.List(obj.Namespace, selector)
				if err != nil {
					return nil, err
				}
				sort.Slice(pods, func(i, j int) bool {
					return pods[i].CreationTimestamp.Before(&pods[j].CreationTimestamp)
				})
				terminationMessage := ""
				for _, podStatus := range pods[len(pods)-1].Status.ContainerStatuses {
					if podStatus.Name != "step-git-source" && podStatus.State.Terminated != nil {
						terminationMessage += podStatus.State.Terminated.Message
					}
				}
				kstatus.SetError(gitjob, terminationMessage)
			}

			if result.Status == status.CurrentStatus {
				if strings.Contains(result.Message, "Job Completed") {
					gitjob.Status.LastExecutedCommit = obj.Annotations["commit"]
				}
				kstatus.SetActive(gitjob)
			}

			if _, err := j.gitjobs.UpdateStatus(gitjob); err != nil {
				return nil, err
			}
		}
	}

	return nil, nil
}
