// Copyright (c) 2021-2025 SUSE LLC

package reconciler

import (
	"context"
	"fmt"

	"github.com/reugn/go-quartz/quartz"
	"golang.org/x/sync/semaphore"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ quartz.Job = &HelmPollingJob{}

type HelmPollingJob struct {
	sem    *semaphore.Weighted
	client client.Client

	namespace string
	name      string
}

func helmPollingDescription(namespace string, name string) string {
	return fmt.Sprintf("helmops-polling-%s-%s", namespace, name)
}

func HelmPollingKey(namespace string, name string) *quartz.JobKey {
	return quartz.NewJobKey(helmPollingDescription(namespace, name))
}

func newHelmPollingJob(c client.Client, namespace string, name string) *HelmPollingJob {
	return &HelmPollingJob{
		sem:    semaphore.NewWeighted(1),
		client: c,

		namespace: namespace,
		name:      name,
	}
}

func (j *HelmPollingJob) Execute(ctx context.Context) error {
	if !j.sem.TryAcquire(1) {
		// already running
		return nil
	}
	defer j.sem.Release(1)

	return j.pollHelm(ctx)
}

// Description returns a description for the job.
// This is needed to implement the Quartz Job interface.
func (j *HelmPollingJob) Description() string {
	return helmPollingDescription(j.namespace, j.name)
}

func (j *HelmPollingJob) pollHelm(ctx context.Context) error {
	// TODO
}
