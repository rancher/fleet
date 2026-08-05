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
	// ReasonDeployFailed reports bundle deployments which failed to apply their resources.
	ReasonDeployFailed = "BundleDeployFailed"

	// ReasonNotReady reports bundle deployments which applied their resources, but those are not ready.
	ReasonNotReady = "BundleNotReady"

	// ReasonReady reports a bundle whose deployments are all ready again.
	ReasonReady = "BundleDeploymentsReady"

	// actionDeploy is the operation all events reported here relate to.
	actionDeploy = "Deploy"

	// tickInterval is how often pending events are checked for being due.
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
)

// Notifier observes objects after their status has been updated, and reports
// state changes worth an event.
type Notifier interface {
	// ObserveBundle reports failures and recoveries of a bundle's
	// deployments. Old is the bundle as previously persisted, which makes
	// the state change survive a restart of the controller.
	ObserveBundle(ctx context.Context, old, cur *fleet.Bundle)

	// ObserveBundleDeployment reports a single bundle deployment, if
	// per-deployment reporting is enabled.
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
}

// entry is the emitter's memory of one object.
type entry struct {
	// emitted is the fingerprint of the last event created for the object.
	emitted fingerprint
	hasSent bool

	// lastEmit is when that event was created.
	lastEmit time.Time

	// pending is the next event to create, and dueAt is when it may be created.
	pending *snapshot
	dueAt   time.Time

	// attempts is how often creating the pending event has failed.
	attempts int

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
func (e *Emitter) ObserveBundle(ctx context.Context, old, cur *fleet.Bundle) {
	opts := e.options()
	if !opts.Enabled || old == nil || cur == nil {
		return
	}
	if !cur.DeletionTimestamp.IsZero() {
		e.Forget(cur.UID)
		return
	}

	failing, reason, fp := failureFingerprint(cur.Status.Summary)
	wasFailing, _, previous := failureFingerprint(old.Status.Summary)

	switch {
	case failing > 0:
		if wasFailing > 0 && previous == fp {
			// The same failures were already part of the previously
			// persisted status, so this is not a state change. Comparing
			// persisted statuses, instead of remembering the last event,
			// keeps this true across restarts of the controller.
			return
		}

		e.schedule(ctx, cur.UID, e.snapshotFor(
			ctx,
			cur,
			corev1.EventTypeWarning,
			reason,
			failureNote(cur.Status.Summary, opts.MaxCauses),
			fp,
		), opts)

	case wasFailing > 0 && opts.ReportRecovery:
		e.schedule(ctx, cur.UID, e.snapshotFor(
			ctx,
			cur,
			corev1.EventTypeNormal,
			ReasonReady,
			readyNote(cur.Status.Summary),
			fingerprintOf("ready", ReasonReady),
		), opts)

	default:
		// Nothing to report. Forget which event was last created for this
		// bundle, so that a failure recurring later is reported again.
		e.clearEmitted(cur.UID)
	}
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
	if state == previous {
		return
	}

	bundle := cur.Labels[fleet.BundleLabel]

	switch state {
	case fleet.ErrApplied, fleet.NotReady:
		reason := failureReason(state)
		message := summary.MessageFromDeployment(cur)

		e.schedule(ctx, cur.UID, e.snapshotFor(
			ctx,
			cur,
			corev1.EventTypeWarning,
			reason,
			deploymentNote(bundle, state, message),
			fingerprintOf("failing", reason, string(state), []string{message}),
		), opts)

	case fleet.Ready:
		if !opts.ReportRecovery || (previous != fleet.ErrApplied && previous != fleet.NotReady) {
			return
		}

		e.schedule(ctx, cur.UID, e.snapshotFor(
			ctx,
			cur,
			corev1.EventTypeNormal,
			ReasonReady,
			deploymentNote(bundle, fleet.Ready, ""),
			fingerprintOf("ready", ReasonReady),
		), opts)
	}
}

// snapshotFor builds the event to create for an object. It returns nil if no
// reference to the object can be built, in which case no event can be reported.
func (e *Emitter) snapshotFor(
	ctx context.Context,
	obj client.Object,
	eventType, reason, note string,
	fp fingerprint,
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

	switch {
	case ent.hasSent && ent.emitted == snap.fp:
		// Already reported and nothing has changed since.
		return false

	case ent.pending != nil && ent.pending.fp == snap.fp:
		// Already queued, keep the earlier due time.
		return false
	}

	// A new description of the object replaces whatever was queued, and
	// starts over with a full budget of creation attempts.
	ent.pending = snap
	ent.attempts = 0
	if ent.dueAt.IsZero() {
		ent.dueAt = e.dueTime(now, ent.lastEmit, opts)

		return !ent.dueAt.After(now)
	}

	return false
}

// dueTime is when an event may be created: after the debounce, and not before
// the minimum interval since the last event for the same object has passed.
func (e *Emitter) dueTime(now, lastEmit time.Time, opts Options) time.Time {
	due := now.Add(opts.Debounce)
	if earliest := lastEmit.Add(opts.MinInterval); !lastEmit.IsZero() && earliest.After(due) {
		return earliest
	}

	return due
}

// flush creates all events which are due, within the rate limit. Events which
// do not fit stay queued and are retried on the next tick, where they may be
// replaced by a newer description of the same object.
func (e *Emitter) flush(ctx context.Context, opts Options) {
	now := e.now()

	for _, p := range e.claimDue(now) {
		if !e.limiter.TryAccept() {
			e.requeue(p.uid, p.snap, now.Add(tickInterval))
			continue
		}

		if err := e.create(ctx, p.snap); err != nil {
			e.createFailed(ctx, p.uid, p.snap, now, err)

			continue
		}

		e.recordEmit(p.uid, p.snap.fp, now, opts)
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
		pending = append(pending, dueEvent{uid: uid, snap: ent.pending})
		ent.pending = nil
		ent.dueAt = time.Time{}
	}

	return pending
}

// recordEmit remembers the event which was created, and schedules whatever was
// observed while it was being created.
func (e *Emitter) recordEmit(uid types.UID, fp fingerprint, now time.Time, opts Options) {
	e.mu.Lock()
	defer e.mu.Unlock()

	ent, ok := e.entries[uid]
	if !ok {
		return
	}

	ent.emitted = fp
	ent.hasSent = true
	ent.lastEmit = now
	ent.attempts = 0
	if ent.pending != nil {
		// Something was observed while this event was created.
		ent.dueAt = e.dueTime(now, ent.lastEmit, opts)
	}
}

// createFailed retries the event, unless it has been tried too often or the
// error says that trying again can only fail the same way. Giving up drops the
// event, which lets the entry be forgotten again: what the event describes is
// still in the object's status, whereas holding on to it would keep the entry
// alive and spend the rate limit which events that can be created need.
//
// Nothing is lost permanently: the next change to the object queues a new
// event, with a full budget of attempts, so a cause which is fixed in the
// meantime reports again.
func (e *Emitter) createFailed(ctx context.Context, uid types.UID, snap *snapshot, now time.Time, err error) {
	e.mu.Lock()

	ent, ok := e.entries[uid]
	if !ok {
		e.mu.Unlock()

		return
	}

	ent.attempts++
	attempts := ent.attempts
	retry := attempts < maxCreateAttempts && !permanentError(err)

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

// requeue puts an event back, unless a newer one has taken its place.
func (e *Emitter) requeue(uid types.UID, snap *snapshot, dueAt time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()

	ent, ok := e.entries[uid]
	if !ok || ent.pending != nil {
		return
	}

	ent.pending = snap
	ent.dueAt = dueAt
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
			Name:      fmt.Sprintf("%s.%x", snap.regarding.Name, now.UnixNano()),
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
		if ent.pending == nil && now.Sub(ent.touched) > entryTTL {
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
// its event. Exempting it lets the map exceed maxTracked instead, which the
// next flush undoes as it drains the pending events.
func (e *Emitter) evict(maxTracked int, keep types.UID) {
	if maxTracked <= 0 || len(e.entries) <= maxTracked {
		return
	}

	candidates := make([]types.UID, 0, len(e.entries))
	for uid, ent := range e.entries {
		if ent.pending == nil && uid != keep {
			candidates = append(candidates, uid)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		return e.entries[candidates[i]].touched.Before(e.entries[candidates[j]].touched)
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
