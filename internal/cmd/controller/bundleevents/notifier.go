// Package bundleevents reports the deployment state of bundles as Kubernetes
// events.
//
// Events are created directly, instead of through client-go's event recorder.
// The recorder deduplicates events by every field except their note, and keeps
// the note of the first event of a series, so notes describing different
// failures of the same bundle would be dropped. Deduplication happens here
// instead, based on a fingerprint of the failure causes and of how many
// deployments are affected, so that each event which does get created describes
// the state accurately.
package bundleevents

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rancher/fleet/internal/cmd/controller/summary"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/reference"
	"k8s.io/client-go/util/flowcontrol"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	// Reasons are scoped to the kind they are reported on, so that an event
	// can be selected by its reason alone: the bundle-level reasons describe
	// a bundle across all of its deployments, the deployment-level ones a
	// single deployment on one cluster.

	// ReasonBundleDeployFailed reports a bundle with deployments which failed to apply their resources.
	ReasonBundleDeployFailed = "BundleDeployFailed"

	// ReasonBundleNotReady reports a bundle with deployments which applied their resources, but those are not ready.
	ReasonBundleNotReady = "BundleNotReady"

	// ReasonBundleReady reports a bundle whose deployments are all ready again.
	ReasonBundleReady = "BundleReady"

	// ReasonBundleDeploymentFailed reports a single bundle deployment which failed to apply its resources.
	ReasonBundleDeploymentFailed = "BundleDeploymentFailed"

	// ReasonBundleDeploymentNotReady reports a single bundle deployment which applied its resources, but those are not ready.
	ReasonBundleDeploymentNotReady = "BundleDeploymentNotReady"

	// ReasonBundleDeploymentReady reports a single bundle deployment which is ready again.
	ReasonBundleDeploymentReady = "BundleDeploymentReady"

	// actionDeploy is the operation all events reported here relate to.
	actionDeploy = "Deploy"

	// tickInterval is how often pending events (see entry.pending) are
	// checked for being due.
	tickInterval = time.Second

	// entryTTL is how long an object without a pending event is remembered.
	entryTTL = time.Hour

	// createRetryDelay is how long to wait before retrying a failed event creation.
	createRetryDelay = 10 * time.Second

	// maxCreateAttempts is how often creating one event is tried before it
	// is dropped. Retrying forever would keep the object's entry alive,
	// which exempts it from both the size limit and the TTL, and would
	// spend the rate limit which events that can be created need.
	maxCreateAttempts = 5

	// reportingInstanceMaxLength is the maximum length the API accepts for reportingInstance.
	reportingInstanceMaxLength = 128

	// eventNameMaxLength is the maximum length the API accepts for an object name.
	eventNameMaxLength = 253
)

// Notifier observes objects after their status has been updated, and reports
// state changes worth an event.
type Notifier interface {
	// ObserveBundle reports failures and recoveries of a bundle's
	// deployments. Old is the bundle as previously persisted, which makes
	// a recovery survive a restart of the controller. Callers report the
	// bundle whether or not its status changed.
	//
	// Profile describes cur's failing deployments, counted over all of
	// them, which the summary cannot do since it lists only 10. It is nil
	// when the summary was not recomputed, in which case nothing is
	// reported.
	ObserveBundle(ctx context.Context, old, cur *fleet.Bundle, profile *Profile)

	// ObserveBundleDeployment reports a single bundle deployment, if
	// per-deployment reporting is enabled. Old is the bundle deployment as
	// previously persisted, which makes the state change survive a restart
	// of the controller.
	ObserveBundleDeployment(ctx context.Context, old, cur *fleet.BundleDeployment)

	// Forget drops everything remembered about an object. Callers report
	// objects which are going away, so that nothing is kept for them.
	Forget(uid types.UID)
}

// fingerprint identifies what an event says, so that saying it again can be skipped.
type fingerprint [sha256.Size]byte

// snapshot is an event waiting to be created.
type snapshot struct {
	regarding corev1.ObjectReference
	eventType string
	reason    string
	note      string
	fp        fingerprint

	// delay is how long the event waits before it is created: the debounce,
	// or the grace period of a deployment which is not ready yet.
	delay time.Duration
}

// failure reports whether the event reports a failure, as opposed to a recovery.
func (s *snapshot) failure() bool {
	return s.eventType == corev1.EventTypeWarning
}

// entry is the emitter's memory of one object.
type entry struct {
	// emitted is the fingerprint of the last event created for the object.
	emitted fingerprint

	// hasSent is true if emitted holds a created event. It is reset once the
	// object stops failing, so that a recurring failure is reported again.
	hasSent bool

	// lastEmit is when that event was created.
	lastEmit time.Time

	// pending is the next event to create, and dueAt is when it may be created.
	// A pending event exists only in the emitter's memory: it is not written
	// to the API until it is due, has been claimed by a flush and has passed
	// the rate limiter. Until then it can be replaced by a newer description
	// of the same object, which is how a burst collapses into a single event.
	pending *snapshot
	dueAt   time.Time

	// attempts is how often creating the pending event has failed.
	attempts int

	// awaitingRecovery records that a failure of the object was reported, so
	// that its recovery is reported once it is ready again. Between a
	// failure and a recovery an object is neither: it goes through transient
	// states, in which nothing is failing but nothing is ready either.
	// Remembering the failure is what lets the recovery be reported from
	// there, instead of on merely leaving the failing state. A failure which
	// was never reported, because it ended within the debounce or the grace
	// period, has no recovery to report either.
	//
	// It is kept until the recovery event has been created, not merely
	// queued: a queued recovery can still be replaced by a new failure, and
	// if that failure ends before it is reported, the recovery of the one
	// which was reported is still owed. It is also what keeps the entry
	// from being forgotten, see forgetStale and evict.
	awaitingRecovery bool

	// creating is an event which a flush has claimed and is writing. It is
	// not pending any more, and not recorded as created yet. No other event
	// of the object is claimed meanwhile, so that events are recorded in the
	// order they were created. For a failure event, what is observed
	// meanwhile is kept until the outcome is known:
	//
	// failureEnded records that the object stopped failing. If the event is
	// created, a recurring failure must be reported again, as if the object
	// had stopped failing after it was. If it is not, the failure is over
	// before it was reported, and it is not retried.
	//
	// recoveryAfterCreate is a recovery observed meanwhile. It is reported
	// once the failure event has been created, and dropped if it is not: a
	// failure which was never reported has no recovery to report. Without
	// it, the recovery would be missed, since it is observed only once,
	// before the failure is recorded as reported.
	creating            *snapshot
	failureEnded        bool
	recoveryAfterCreate *snapshot

	// touched is when the object was last observed, used to forget it again.
	touched time.Time
}

// dueEvent is an event which has been claimed for creation, together with the
// object it belongs to.
type dueEvent struct {
	uid  types.UID
	snap *snapshot
}

// Emitter implements Notifier. It also implements manager.Runnable: pending
// events are created by its Start method.
type Emitter struct {
	client              client.Client
	scheme              *runtime.Scheme
	reportingController string
	reportingInstance   string
	options             func() Options
	now                 func() time.Time
	limiter             flowcontrol.RateLimiter

	mu      sync.Mutex
	entries map[types.UID]*entry
}

// Option overrides an emitter's defaults.
type Option func(*Emitter)

// WithClock replaces the emitter's source of time.
func WithClock(now func() time.Time) Option {
	return func(e *Emitter) { e.now = now }
}

// WithRateLimiter replaces the emitter's rate limiter.
func WithRateLimiter(limiter flowcontrol.RateLimiter) Option {
	return func(e *Emitter) { e.limiter = limiter }
}

// New returns an emitter which creates events with the given reporting
// controller name. Options are read on every use, so that configuration changes
// take effect without a restart.
func New(c client.Client, scheme *runtime.Scheme, reportingController string, options func() Options, opts ...Option) *Emitter {
	hostname, _ := os.Hostname()
	e := &Emitter{
		client:              c,
		scheme:              scheme,
		reportingController: reportingController,
		reportingInstance:   truncate(reportingController+"-"+hostname, reportingInstanceMaxLength),
		options:             options,
		now:                 time.Now,
		limiter:             flowcontrol.NewTokenBucketRateLimiter(defaultQPS, defaultBurst),
		entries:             map[types.UID]*entry{},
	}

	for _, opt := range opts {
		opt(e)
	}

	return e
}

// Start creates pending events once they are due. It returns when the context
// is cancelled.
func (e *Emitter) Start(ctx context.Context) error {
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			e.flush(ctx, e.options())
			e.forgetStale()
		}
	}
}

// ObserveBundle reports failures and recoveries of a bundle's deployments.
func (e *Emitter) ObserveBundle(ctx context.Context, old, cur *fleet.Bundle, profile *Profile) {
	opts := e.options()
	if !opts.Enabled || old == nil || cur == nil {
		return
	}
	if !cur.DeletionTimestamp.IsZero() {
		e.Forget(cur.UID)
		return
	}

	if profile == nil {
		// The summary was not recomputed from the bundle's deployments,
		// so nothing about them has changed, and their failures cannot
		// be described: the summary only lists a sample of them.
		return
	}

	failing, reason, fp := failureFingerprint(cur.Status.Summary, profile)
	wasFailing, _ := failureCounts(old.Status.Summary)
	known := e.knows(cur.UID)

	// After a restart, the memory of what was reported is gone, but the
	// previously persisted status still shows the failure. The same holds
	// once the bundle has been forgotten or evicted.
	//
	// A failure still going on, which the persisted status already showed,
	// is assumed to have been reported, so that a restart does not report
	// every ongoing failure again. The persisted status only lists a
	// sample of the deployments, so a new cause on one it does not list,
	// with the same counts, is missed until the failure changes again. A
	// failure which is merely progressing is not assumed reported: it may
	// still have been within its grace period, and reporting it again
	// once is better than never reporting it.
	if !known && failing > 0 && wasFailing > 0 && !profile.progressing() &&
		persistedFailureFingerprint(old.Status.Summary) == persistedFailureFingerprint(cur.Status.Summary) {
		e.assumeReported(cur.UID, fp, opts)
	}

	// Whatever the bundle's state is now, a failure the persisted status
	// shows owes its recovery, since the bundle may move on to another
	// failure first, which would make it known.
	if wasFailing > 0 && opts.ReportRecovery && !known {
		e.awaitRecovery(cur.UID, opts)
	}

	if failing > 0 {
		// Whether the failures were already reported is decided by what
		// the emitter remembers creating, see queue. An event queued
		// earlier in the burst is refreshed, though: how many deployments
		// are failing is bucketed, so agents reporting one after another
		// mostly fingerprint the same, and the note would otherwise
		// describe the counts as they were when the first agent reported.
		e.schedule(ctx, cur.UID, e.snapshotFor(
			ctx,
			cur,
			corev1.EventTypeWarning,
			reason,
			failureNote(cur.Status.Summary, profile, opts.MaxCauses),
			fp,
			failureDelay(profile.progressing(), opts),
		), opts)

		return
	}

	// Nothing is failing. A failure which has not been reported yet is over
	// before it was, typically a rollout which completed within its grace
	// period, and is not reported any more. Forget which event was last
	// created for this bundle, so that a failure recurring later is
	// reported again.
	e.dropPendingFailure(cur.UID)
	e.clearEmitted(cur.UID)

	if !opts.ReportRecovery {
		e.stopAwaitingRecovery(cur.UID)
		return
	}
	if !e.expectsRecovery(cur.UID) {
		return
	}

	if !recovered(cur.Status.Summary) {
		// Not failing, but not ready either: the bundle is still being
		// deployed. Reporting here would call deployments ready which are
		// not, so keep waiting for the state the event describes.
		return
	}

	e.reportRecovery(ctx, cur.UID, e.snapshotFor(
		ctx,
		cur,
		corev1.EventTypeNormal,
		ReasonBundleReady,
		readyNote(cur.Status.Summary),
		fingerprintOf("ready", ReasonBundleReady),
		opts.Debounce,
	), opts)
}

// ObserveBundleDeployment reports a single bundle deployment, if per-deployment
// reporting is enabled.
func (e *Emitter) ObserveBundleDeployment(ctx context.Context, old, cur *fleet.BundleDeployment) {
	opts := e.options()
	if !opts.Enabled || !opts.PerDeployment || old == nil || cur == nil {
		return
	}
	if !cur.DeletionTimestamp.IsZero() {
		e.Forget(cur.UID)
		return
	}

	state := fleet.BundleState(cur.Status.Display.State)
	previous := fleet.BundleState(old.Status.Display.State)

	bundle := cur.Labels[fleet.BundleLabel]
	known := e.knows(cur.UID)

	// Whether a failure was already reported is decided by what the emitter
	// remembers creating, see queue, not by comparing with old: the caller
	// cannot tell what the agent reported before. The fingerprint includes
	// whether the deployment is progressing, so one which starts reporting
	// an error is a new event, due after the debounce instead of the grace
	// period.
	var (
		reason, message string
		progressing     bool
		fp              fingerprint
	)
	if isFailure(state) {
		reason = deploymentFailureReason(state)
		message = summary.MessageFromDeployment(cur)
		progressing = deploymentProgressing(cur)
		fp = fingerprintOf("failing", reason, string(state), []string{message}, strconv.FormatBool(progressing))

		// As for bundles, after a restart, or once the deployment has been
		// forgotten, a failure the persisted display state already showed
		// is assumed to have been reported, unless it is merely
		// progressing. Only the display state is persisted, so a deployment
		// which turned from progressing into an error while the controller
		// was down is missed until it changes again.
		if !known && state == previous && !progressing {
			e.assumeReported(cur.UID, fp, opts)
		}
	}

	// As for bundles, after a restart a failure the persisted display state
	// shows is assumed to have been reported, and owes its recovery. Only
	// the display state is persisted from before: the conditions and
	// resources are the agent's latest, in old as in cur, so whether the
	// failure was merely progressing cannot be told from old. They describe
	// the deployment as it is now, which may be ready already, and would
	// make any failure look like it was progressing, losing its recovery.
	if isFailure(previous) && opts.ReportRecovery && !known {
		e.awaitRecovery(cur.UID, opts)
	}

	if isFailure(state) {
		e.schedule(ctx, cur.UID, e.snapshotFor(
			ctx,
			cur,
			corev1.EventTypeWarning,
			reason,
			deploymentNote(bundle, state, message),
			fp,
			failureDelay(progressing, opts),
		), opts)

		return
	}

	// As for bundles, a failure which has not been reported yet is not
	// reported any more, and one recurring later is reported again.
	e.dropPendingFailure(cur.UID)
	e.clearEmitted(cur.UID)

	if state != fleet.Ready {
		return
	}
	if !opts.ReportRecovery {
		e.stopAwaitingRecovery(cur.UID)
		return
	}
	if !e.expectsRecovery(cur.UID) {
		return
	}

	e.reportRecovery(ctx, cur.UID, e.snapshotFor(
		ctx,
		cur,
		corev1.EventTypeNormal,
		ReasonBundleDeploymentReady,
		deploymentNote(bundle, fleet.Ready, ""),
		fingerprintOf("ready", ReasonBundleDeploymentReady),
		opts.Debounce,
	), opts)
}

// deploymentProgressing reports whether a bundle deployment is not ready, with
// nothing reporting an error, see progressingCause.
func deploymentProgressing(bd *fleet.BundleDeployment) bool {
	return fleet.BundleState(bd.Status.Display.State) == fleet.NotReady &&
		progressingCause(summary.MessageFromDeployment(bd), bd.Status.NonReadyStatus)
}

// failureDelay is how long a failure waits before it is reported: the debounce,
// or, for deployments which are merely not ready yet, the grace period, which
// is never shorter than the debounce.
func failureDelay(progressing bool, opts Options) time.Duration {
	if progressing {
		return max(opts.NotReadyGracePeriod, opts.Debounce)
	}

	return opts.Debounce
}

// knows reports whether the emitter remembers anything about an object. It does
// not after a restart, or once the object has been forgotten.
func (e *Emitter) knows(uid types.UID) bool {
	e.mu.Lock()
	defer e.mu.Unlock()

	_, ok := e.entries[uid]

	return ok
}

// dropPendingFailure discards a failure event which has not been created yet,
// because the object stopped failing before it was. A failure event being
// created right now is left to finish, see entry.creating.
func (e *Emitter) dropPendingFailure(uid types.UID) {
	e.mu.Lock()
	defer e.mu.Unlock()

	ent, ok := e.entries[uid]
	if !ok {
		return
	}
	if ent.pending != nil && ent.pending.failure() {
		ent.discardPending()
	}
	if ent.creatingFailure() {
		ent.failureEnded = true
	}
}

// awaitRecovery remembers that a failure of an object was reported, so that its
// recovery is reported once it is ready again. It records the wait; it does not
// block.
func (e *Emitter) awaitRecovery(uid types.UID, opts Options) {
	now := e.now()

	e.mu.Lock()
	defer e.mu.Unlock()

	ent, ok := e.entries[uid]
	if !ok {
		ent = &entry{touched: now}
		e.entries[uid] = ent
		e.evict(opts.MaxTracked, uid)
	}
	ent.touched = now
	ent.awaitingRecovery = true
}

// assumeReported remembers an object the emitter knows nothing about as if the
// failure with the given fingerprint had been reported, without creating an
// event: the persisted status shows the failure from before a restart, or from
// before the object was forgotten, when its event was created. The time it was
// created is unknown, so the minimum interval does not delay the next event.
// An object the emitter already knows is left alone, since what it remembers
// is more accurate.
func (e *Emitter) assumeReported(uid types.UID, fp fingerprint, opts Options) {
	now := e.now()

	e.mu.Lock()
	defer e.mu.Unlock()

	if _, ok := e.entries[uid]; ok {
		return
	}
	e.entries[uid] = &entry{
		emitted: fp,
		hasSent: true,
		touched: now,
	}
	e.evict(opts.MaxTracked, uid)
}

// discardPending drops the event waiting to be created, if any. The caller
// holds the lock.
func (ent *entry) discardPending() {
	ent.pending = nil
	ent.dueAt = time.Time{}
	ent.attempts = 0
}

// creatingFailure reports whether a failure event of the object is being
// created. The caller holds the lock.
func (ent *entry) creatingFailure() bool {
	return ent.creating != nil && ent.creating.failure()
}

// endCreate forgets the event being created, and what was observed while it
// was. The caller holds the lock.
func (ent *entry) endCreate() {
	ent.creating = nil
	ent.failureEnded = false
	ent.recoveryAfterCreate = nil
}

// stopAwaitingRecovery forgets a failure, either because its recovery has just
// been reported or because it is not going to be.
func (e *Emitter) stopAwaitingRecovery(uid types.UID) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if ent, ok := e.entries[uid]; ok {
		ent.awaitingRecovery = false
	}
}

// expectsRecovery reports whether a recovery of the object is worth reporting:
// its failure was reported, or is being reported right now.
func (e *Emitter) expectsRecovery(uid types.UID) bool {
	e.mu.Lock()
	defer e.mu.Unlock()

	ent, ok := e.entries[uid]

	return ok && (ent.awaitingRecovery || ent.creatingFailure())
}

// reportRecovery queues the recovery of an object whose failure was reported.
// If the failure event is still being created, the recovery is held back until
// it has been, see entry.recoveryAfterCreate.
func (e *Emitter) reportRecovery(ctx context.Context, uid types.UID, snap *snapshot, opts Options) {
	if snap == nil {
		return
	}

	e.mu.Lock()
	ent, ok := e.entries[uid]
	switch {
	case !ok:
		e.mu.Unlock()
		return
	case ent.creatingFailure():
		ent.recoveryAfterCreate = snap
		e.mu.Unlock()
		return
	case !ent.awaitingRecovery:
		e.mu.Unlock()
		return
	}
	// The recovery stays owed until its event has been created, see
	// entry.awaitingRecovery. Observing the recovery again meanwhile only
	// refreshes the queued event.
	e.mu.Unlock()

	e.schedule(ctx, uid, snap, opts)
}

// snapshotFor builds the event to create for an object. It returns nil if no
// reference to the object can be built, in which case no event can be reported.
func (e *Emitter) snapshotFor(
	ctx context.Context,
	obj client.Object,
	eventType, reason, note string,
	fp fingerprint,
	delay time.Duration,
) *snapshot {
	ref, err := reference.GetReference(e.scheme, obj)
	if err != nil {
		log.FromContext(ctx).V(1).Info(
			"Cannot report deployment state, failed to build a reference to the object",
			"name", obj.GetName(),
			"namespace", obj.GetNamespace(),
			"error", err,
		)

		return nil
	}

	return &snapshot{
		regarding: *ref,
		eventType: eventType,
		reason:    reason,
		note:      note,
		fp:        fp,
		delay:     delay,
	}
}

// schedule queues an event, unless the same event was already reported. Events
// are created by the flush loop once they are due, which lets a burst of
// changes collapse into a single event describing all of them.
func (e *Emitter) schedule(ctx context.Context, uid types.UID, snap *snapshot, opts Options) {
	if snap == nil {
		return
	}

	if e.queue(uid, snap, opts) {
		e.flush(ctx, opts)
	}
}

// queue stores the event and reports whether it is due immediately, in which
// case the caller creates it. Creating it is left to the caller because the
// lock must not be held while events are written.
func (e *Emitter) queue(uid types.UID, snap *snapshot, opts Options) bool {
	now := e.now()

	e.mu.Lock()
	defer e.mu.Unlock()

	ent, ok := e.entries[uid]
	if !ok {
		ent = &entry{touched: now}
		e.entries[uid] = ent
		e.evict(opts.MaxTracked, uid)
	}
	ent.touched = now

	if snap.failure() {
		// Failing again, so whatever ended while an earlier failure event
		// was being created is over.
		ent.failureEnded = false
		ent.recoveryAfterCreate = nil
	}

	switch {
	case ent.hasSent && ent.emitted == snap.fp:
		// Already reported. Whatever was queued since describes a state
		// the object went through and left, like an error which cleared
		// within the debounce, and is not reported any more.
		ent.discardPending()

		return false

	case ent.creating != nil && ent.creating.fp == snap.fp:
		// Being reported right now, see above.
		ent.discardPending()

		return false

	case ent.pending != nil && ent.pending.fp == snap.fp:
		// The same event, but a newer description of it. Replacing the
		// queued snapshot is what lets a burst collapse into one event
		// carrying the settled counts: the fingerprint buckets how many
		// deployments are failing, so most of a burst fingerprints the
		// same and would otherwise leave the note describing the state
		// the burst started from.
		//
		// The due time is kept, so this does not delay the event, and so
		// is the attempt budget: it is still the same event, and a state
		// which keeps being re-observed must not keep buying retries.
		ent.pending = snap

		return false
	}

	// A new description of the object replaces whatever was queued, and
	// starts over with a full budget of creation attempts. It keeps the due
	// time, so that a burst does not keep delaying its event, unless it
	// waits for another reason: a deployment which was not ready yet and
	// now reports an error is reported after the debounce, without waiting
	// for the rest of the grace period, and one whose error turned back
	// into merely not being ready starts its grace period.
	previous := ent.pending
	ent.pending = snap
	ent.attempts = 0
	if ent.dueAt.IsZero() || (previous != nil && previous.delay != snap.delay) {
		ent.dueAt = e.dueTime(now, ent.lastEmit, snap.delay, opts)

		return !ent.dueAt.After(now)
	}

	return false
}

// dueTime is when an event may be created: after its delay, and not before the
// minimum interval since the last event for the same object has passed.
func (e *Emitter) dueTime(now, lastEmit time.Time, delay time.Duration, opts Options) time.Time {
	due := now.Add(delay)
	if earliest := lastEmit.Add(opts.MinInterval); !lastEmit.IsZero() && earliest.After(due) {
		return earliest
	}

	return due
}

// flush creates all events which are due, within the rate limit. Events which
// do not fit stay queued and are retried on the next tick, where they may be
// replaced by a newer description of the same object.
func (e *Emitter) flush(ctx context.Context, opts Options) {
	if !opts.Enabled {
		// Options are read live, so reporting can be turned off while
		// events are queued. Those describe a state which is still in the
		// object's status, so they are dropped instead of being created
		// after the fact. Dropping them also lets their entries be
		// forgotten again, which a pending event exempts them from.
		e.dropPending()

		return
	}

	now := e.now()

	for _, p := range e.claimDue(now) {
		if !e.limiter.TryAccept() {
			e.requeue(p.uid, p.snap, now, opts)
			continue
		}

		if err := e.create(ctx, p.snap); err != nil {
			e.createFailed(ctx, p.uid, p.snap, now, err, opts)

			continue
		}

		e.recordEmit(p.uid, p.snap, now, opts)
	}
}

// dropPending discards every event waiting to be created.
func (e *Emitter) dropPending() {
	e.mu.Lock()
	defer e.mu.Unlock()

	for _, ent := range e.entries {
		ent.pending = nil
		ent.dueAt = time.Time{}
		ent.attempts = 0
		ent.failureEnded = false
		ent.recoveryAfterCreate = nil
	}
}

// claimDue takes the events which are due, so that a concurrent flush does not
// create them a second time.
func (e *Emitter) claimDue(now time.Time) []dueEvent {
	e.mu.Lock()
	defer e.mu.Unlock()

	var pending []dueEvent

	for uid, ent := range e.entries {
		if ent.pending == nil || ent.dueAt.IsZero() || ent.dueAt.After(now) {
			continue
		}
		if ent.creating != nil {
			// Wait for the event being created, see entry.creating.
			continue
		}
		pending = append(pending, dueEvent{uid: uid, snap: ent.pending})
		ent.creating = ent.pending
		ent.pending = nil
		ent.dueAt = time.Time{}
	}

	return pending
}

// recordEmit remembers the event which was created, and schedules whatever was
// observed while it was being created. A reported failure is what makes the
// recovery worth reporting.
func (e *Emitter) recordEmit(uid types.UID, snap *snapshot, now time.Time, opts Options) {
	e.mu.Lock()
	defer e.mu.Unlock()

	ent, ok := e.entries[uid]
	if !ok {
		return
	}

	ent.emitted = snap.fp
	ent.hasSent = true
	ent.lastEmit = now
	ent.attempts = 0
	// A reported failure owes its recovery, unless recoveries are not
	// reported, and a reported recovery settles it.
	ent.awaitingRecovery = snap.failure() && opts.ReportRecovery

	if snap.failure() && ent.creating == snap {
		// The object stopped failing, or even recovered, while this
		// event was created. Handle that now, as observing it would have,
		// had the event been recorded already.
		if ent.failureEnded {
			ent.emitted = fingerprint{}
			ent.hasSent = false
		}
		if recovery := ent.recoveryAfterCreate; recovery != nil && opts.ReportRecovery && ent.pending == nil {
			ent.pending = recovery
		}
	}
	if ent.creating == snap {
		ent.endCreate()
	}
	if ent.pending != nil {
		// Something was observed while this event was created.
		ent.dueAt = e.dueTime(now, ent.lastEmit, ent.pending.delay, opts)
	}
}

// createFailed puts the event back to be retried, unless it has been tried too
// often or the error says that trying again can only fail the same way. The
// event was taken off the queue when it was claimed, so giving up only means
// not putting it back. This lets the entry be forgotten again: what the event
// describes is still in the object's status, whereas holding on to it would
// keep the entry alive and spend the rate limit which events that can be
// created need.
//
// The dropped event is not reported later, but what it described is still in
// the object's status, and the next change to the object queues a new event
// with a full budget of attempts.
func (e *Emitter) createFailed(ctx context.Context, uid types.UID, snap *snapshot, now time.Time, err error, opts Options) {
	e.mu.Lock()

	ent, ok := e.entries[uid]
	if !ok {
		e.mu.Unlock()

		return
	}

	ent.attempts++
	attempts := ent.attempts
	retry := attempts < maxCreateAttempts && !permanentError(err)

	// A failure which ended while its event was being created is not
	// reported any more, nor is its recovery, see endFailureUnreported.
	if ent.creating == snap {
		if snap.failure() && ent.failureEnded {
			e.endFailureUnreported(ent, now, opts)
			ent.attempts = 0
			e.mu.Unlock()

			return
		}
		ent.endCreate()
	}

	// A newer event may have taken its place while this one was created.
	if retry && ent.pending == nil {
		ent.pending = snap
		ent.dueAt = now.Add(createRetryDelay)
	}

	e.mu.Unlock()

	logger := log.FromContext(ctx).WithValues(
		"name", snap.regarding.Name,
		"namespace", snap.regarding.Namespace,
		"reason", snap.reason,
		"attempts", attempts,
	)

	if retry {
		logger.V(1).Info("Failed to create event for deployment state, retrying", "error", err)

		return
	}

	// A dropped event is a gap in what the cluster reports about itself,
	// which is worth seeing without debug logging turned on.
	logger.Error(err, "Giving up on reporting deployment state as an event")
}

// permanentError reports whether creating the event again can only fail the
// same way, because the request is rejected for what it is rather than for
// when it arrived.
func permanentError(err error) bool {
	return apierrors.IsForbidden(err) ||
		apierrors.IsUnauthorized(err) ||
		apierrors.IsInvalid(err) ||
		apierrors.IsBadRequest(err) ||
		apierrors.IsNotFound(err) ||
		apierrors.IsRequestEntityTooLargeError(err) ||
		apierrors.IsMethodNotSupported(err)
}

// requeue puts an event refused by the rate limiter back, to be tried on the
// next tick, unless a newer one has taken its place.
func (e *Emitter) requeue(uid types.UID, snap *snapshot, now time.Time, opts Options) {
	e.mu.Lock()
	defer e.mu.Unlock()

	ent, ok := e.entries[uid]
	if !ok {
		return
	}

	if ent.creating == snap {
		// A failure which ended before its event could be created is
		// not reported, nor is its recovery, see endFailureUnreported.
		if snap.failure() && ent.failureEnded {
			e.endFailureUnreported(ent, now, opts)

			return
		}
		ent.endCreate()
	}

	if ent.pending != nil {
		return
	}

	ent.pending = snap
	ent.dueAt = now.Add(tickInterval)
}

// endFailureUnreported ends the creation of a failure event which is not going
// to be created, because the object stopped failing first. The caller holds the
// lock.
//
// Such a failure was never reported, so it has no recovery of its own. But it
// may have replaced a failure which was, and whose recovery is still owed, see
// entry.awaitingRecovery. The recovery observed while the event was being
// created settles that one, so it is queued instead of being dropped with the
// failure: the object is ready and stays so, and would not be observed again
// to report it.
func (e *Emitter) endFailureUnreported(ent *entry, now time.Time, opts Options) {
	recovery := ent.recoveryAfterCreate
	ent.endCreate()

	if recovery == nil || !ent.awaitingRecovery || !opts.ReportRecovery || ent.pending != nil {
		return
	}

	ent.pending = recovery
	ent.attempts = 0
	ent.dueAt = e.dueTime(now, ent.lastEmit, recovery.delay, opts)
}

// create writes the event. Its name follows the convention used by client-go,
// and its namespace has to be the one of the object it is about.
func (e *Emitter) create(ctx context.Context, snap *snapshot) error {
	now := e.now()

	namespace := snap.regarding.Namespace
	if namespace == "" {
		namespace = metav1.NamespaceDefault
	}

	return e.client.Create(ctx, &eventsv1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      eventName(snap.regarding.Name, now),
			Namespace: namespace,
		},
		EventTime:           metav1.MicroTime{Time: now},
		Type:                snap.eventType,
		Reason:              snap.reason,
		Action:              actionDeploy,
		ReportingController: e.reportingController,
		ReportingInstance:   e.reportingInstance,
		Regarding:           snap.regarding,
		Note:                snap.note,
	})
}

// eventName builds the name of the event about an object, following the
// convention used by client-go. The name of the object it is about is shortened
// if the two together would not fit what the API accepts, since a rejected name
// is a permanent error, which would drop the event.
func eventName(regarding string, now time.Time) string {
	suffix := fmt.Sprintf(".%x", now.UnixNano())
	if len(regarding)+len(suffix) > eventNameMaxLength {
		regarding = regarding[:eventNameMaxLength-len(suffix)]
	}

	// A name may not end in a separator, which cutting it can leave behind.
	return strings.TrimRight(regarding, "-._") + suffix
}

// clearEmitted forgets which event was last created for an object, without
// dropping an event which has not been created yet.
func (e *Emitter) clearEmitted(uid types.UID) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if ent, ok := e.entries[uid]; ok {
		ent.emitted = fingerprint{}
		ent.hasSent = false
		ent.touched = e.now()
	}
}

// Forget drops everything remembered about an object.
func (e *Emitter) Forget(uid types.UID) {
	e.mu.Lock()
	defer e.mu.Unlock()

	delete(e.entries, uid)
}

// forgetStale drops objects which have not been observed for a while and have
// no event waiting to be created.
func (e *Emitter) forgetStale() {
	now := e.now()

	e.mu.Lock()
	defer e.mu.Unlock()

	for uid, ent := range e.entries {
		if ent.pending == nil && ent.creating == nil && !ent.awaitingRecovery && now.Sub(ent.touched) > entryTTL {
			delete(e.entries, uid)
		}
	}
}

// evict makes room for the entry of keep by forgetting the objects observed
// longest ago. Forgetting an object may cost one duplicate event later. The
// caller holds the lock.
//
// The entry being made room for is never a candidate. Objects with an event
// waiting are exempt, and during a burst that is every object except the one
// just observed, which would otherwise leave it as the only candidate and drop
// its event. Exempting it lets the map exceed maxTracked instead, until the
// pending events have been created and the next object to be tracked evicts
// again.
func (e *Emitter) evict(maxTracked int, keep types.UID) {
	if maxTracked <= 0 || len(e.entries) <= maxTracked {
		return
	}

	candidates := make([]types.UID, 0, len(e.entries))
	for uid, ent := range e.entries {
		if ent.pending == nil && ent.creating == nil && uid != keep {
			candidates = append(candidates, uid)
		}
	}
	// Objects owing a recovery are evicted last: forgetting one loses its
	// recovery event, rather than costing a duplicate.
	sort.Slice(candidates, func(i, j int) bool {
		a, b := e.entries[candidates[i]], e.entries[candidates[j]]
		if a.awaitingRecovery != b.awaitingRecovery {
			return b.awaitingRecovery
		}

		return a.touched.Before(b.touched)
	})

	// Evict in batches, so that eviction does not run on every new object.
	target := len(e.entries) - maxTracked*9/10
	for i := 0; i < len(candidates) && i < target; i++ {
		delete(e.entries, candidates[i])
	}
}

// fingerprintOf builds the fingerprint of an event from the parts which, when they
// change, make the event worth reporting again.
func fingerprintOf(parts ...any) fingerprint {
	var b strings.Builder

	for _, part := range parts {
		switch v := part.(type) {
		case string:
			b.WriteString(v)
		case []string:
			for _, s := range v {
				b.WriteString(s)
				b.WriteByte(0)
			}
		}
		b.WriteByte(0)
	}

	return sha256.Sum256([]byte(b.String()))
}
