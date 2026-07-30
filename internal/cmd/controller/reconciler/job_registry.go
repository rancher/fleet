package reconciler

import (
	"slices"
	"sync"

	"github.com/reugn/go-quartz/quartz"
)

// jobRegistry tracks the CronDurationJobs owned by a ScheduleReconciler, keyed by quartz job key.
//
// It exists because the quartz scheduler cannot answer the question "does this Schedule have a
// job?": quartz pops a job off its queue before running it, and a CronDurationJob only puts itself
// back once its start or stop action is done. A running job is therefore reported as missing by
// quartz.GetScheduledJob, and re-creating a job from that answer would cancel the active window the
// running job has just opened.
//
// The zero value is ready to use.
type jobRegistry struct {
	mu   sync.RWMutex
	jobs map[string]*CronDurationJob
}

func (r *jobRegistry) get(key *quartz.JobKey) (*CronDurationJob, bool) {
	if key == nil {
		return nil, false
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	job, found := r.jobs[key.String()]

	return job, found
}

func (r *jobRegistry) store(key *quartz.JobKey, job *CronDurationJob) {
	if key == nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.jobs == nil {
		r.jobs = map[string]*CronDurationJob{}
	}
	r.jobs[key.String()] = job
}

func (r *jobRegistry) delete(key *quartz.JobKey) {
	if key == nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.jobs, key.String())
}

func (r *jobRegistry) list() []*CronDurationJob {
	r.mu.RLock()
	defer r.mu.RUnlock()

	jobs := make([]*CronDurationJob, 0, len(r.jobs))
	for _, job := range r.jobs {
		jobs = append(jobs, job)
	}

	return jobs
}

// jobsForCluster returns the registered jobs which currently target the given cluster.
func (r *jobRegistry) jobsForCluster(cluster, namespace string) []*CronDurationJob {
	jobs := []*CronDurationJob{}
	for _, job := range r.list() {
		if job.targets(cluster, namespace) {
			jobs = append(jobs, job)
		}
	}

	return jobs
}

// isClusterScheduled returns true if the given cluster is targeted by any registered job.
func (r *jobRegistry) isClusterScheduled(cluster, namespace string) bool {
	return clusterHasJob(r.list(), cluster, namespace)
}

// clustersNotScheduled returns those of the given clusters which are targeted by no registered job.
func (r *jobRegistry) clustersNotScheduled(clusters []string, namespace string) []string {
	// A single snapshot serves every cluster, instead of re-listing the registry once per cluster.
	jobs := r.list()

	notScheduled := []string{}
	for _, cluster := range clusters {
		if !clusterHasJob(jobs, cluster, namespace) {
			notScheduled = append(notScheduled, cluster)
		}
	}

	return notScheduled
}

// clusterHasJob returns true as soon as one of jobs targets the given cluster. Unlike
// jobsForCluster, it answers that question without allocating a slice, and without locking the
// jobs past the first match.
func clusterHasJob(jobs []*CronDurationJob, cluster, namespace string) bool {
	return slices.ContainsFunc(jobs, func(job *CronDurationJob) bool {
		return job.targets(cluster, namespace)
	})
}
