package bundleevents

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/validation"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/util/flowcontrol"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	renderError = "chart render failed: template: app/templates/deploy.yaml:12: nil pointer"
	nsForbidden = `namespace "prod" is forbidden by AllowedTargetNamespaceSelector`
)

type testClock struct {
	now time.Time
}

func (c *testClock) Now() time.Time {
	return c.now
}

func (c *testClock) advance(d time.Duration) {
	c.now = c.now.Add(d)
}

type fixture struct {
	emitter *Emitter
	client  client.Client
	clock   *testClock
	opts    *Options

	// createErr decides how creating an event fails. Nil lets it succeed.
	createErr func() error
}

// failingClient lets a test fail event creation the way the API server would.
type failingClient struct {
	client.Client

	fixture *fixture
}

func (c *failingClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if c.fixture.createErr != nil {
		if err := c.fixture.createErr(); err != nil {
			return err
		}
	}

	return c.Client.Create(ctx, obj, opts...)
}

func newFixture(t *testing.T, limiter flowcontrol.RateLimiter) *fixture {
	t.Helper()

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(fleet.AddToScheme(scheme))

	opts := DefaultOptions()
	clock := &testClock{now: time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)}
	c := fake.NewClientBuilder().WithScheme(scheme).Build()

	if limiter == nil {
		limiter = flowcontrol.NewFakeAlwaysRateLimiter()
	}

	f := &fixture{client: c, clock: clock, opts: &opts}
	f.emitter = New(&failingClient{Client: c, fixture: f}, scheme, "fleet-bundle-ctrl",
		func() Options { return *f.opts },
		WithClock(clock.Now), WithRateLimiter(limiter))

	return f
}

// bundleEntry returns what the emitter remembers about the test bundle, or nil.
func (f *fixture) bundleEntry() *entry {
	f.emitter.mu.Lock()
	defer f.emitter.mu.Unlock()

	return f.emitter.entries[bundleUID]
}

// retry advances past the retry delay and flushes, n times.
func (f *fixture) retry(n int) {
	for range n {
		f.clock.advance(createRetryDelay + time.Second)
		f.emitter.flush(context.TODO(), *f.opts)
	}
}

// flush advances the clock past the debounce and creates all due events.
func (f *fixture) flush(t *testing.T) {
	t.Helper()

	f.clock.advance(f.opts.Debounce + time.Second)
	f.emitter.flush(context.TODO(), *f.opts)
}

func (f *fixture) events(t *testing.T) []eventsv1.Event {
	t.Helper()

	list := &eventsv1.EventList{}
	if err := f.client.List(context.TODO(), list); err != nil {
		t.Fatalf("listing events: %v", err)
	}
	sort.Slice(list.Items, func(i, j int) bool {
		return list.Items[i].EventTime.Before(&list.Items[j].EventTime)
	})

	return list.Items
}

func bundle(summary fleet.BundleSummary) *fleet.Bundle {
	return &fleet.Bundle{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-repo-app",
			Namespace: "fleet-default",
			UID:       bundleUID,
		},
		Status: fleet.BundleStatus{Summary: summary},
	}
}

// targets is the number of clusters the test bundle is deployed to.
const targets = 500

func ready() fleet.BundleSummary {
	return fleet.BundleSummary{DesiredReady: targets, Ready: targets}
}

func failing(errApplied int, messages ...string) fleet.BundleSummary {
	s := fleet.BundleSummary{
		DesiredReady: targets,
		Ready:        targets - errApplied,
		ErrApplied:   errApplied,
	}

	for i, message := range messages {
		s.NonReadyResources = append(s.NonReadyResources, fleet.NonReadyResource{
			Name:    "fleet-default/cluster-" + string(rune('a'+i)),
			State:   fleet.ErrApplied,
			Message: message,
		})
	}

	return s
}

func TestBurstIsReportedAsOneEventWithFinalCounts(t *testing.T) {
	f := newFixture(t, nil)

	// The first failure of the burst, followed by the rest of it, before the
	// debounce has elapsed.
	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(failing(1, renderError)))
	f.emitter.ObserveBundle(context.TODO(), bundle(failing(1, renderError)), bundle(failing(500, renderError)))

	if got := f.events(t); len(got) != 0 {
		t.Fatalf("expected no event before the debounce elapsed, got %d", len(got))
	}

	f.flush(t)

	events := f.events(t)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	event := events[0]
	if event.Type != corev1.EventTypeWarning || event.Reason != ReasonDeployFailed || event.Action != actionDeploy {
		t.Errorf("unexpected type/reason/action: %s/%s/%s", event.Type, event.Reason, event.Action)
	}
	if event.Namespace != "fleet-default" || event.Regarding.Kind != "Bundle" || event.Regarding.Name != "my-repo-app" {
		t.Errorf("unexpected namespace or regarding object: %s, %+v", event.Namespace, event.Regarding)
	}
	if !strings.HasPrefix(event.Note, "500/500 bundle deployments failing.") {
		t.Errorf("expected the note to describe the whole burst, got %q", event.Note)
	}
	if !strings.Contains(event.Note, renderError) {
		t.Errorf("expected the note to name the cause, got %q", event.Note)
	}
	if event.ReportingController != "fleet-bundle-ctrl" || event.ReportingInstance == "" {
		t.Errorf("unexpected reporting controller/instance: %q/%q", event.ReportingController, event.ReportingInstance)
	}
	if event.Series != nil {
		t.Errorf("expected a standalone event, got a series: %+v", event.Series)
	}
}

func TestUnchangedFailureIsReportedOnce(t *testing.T) {
	f := newFixture(t, nil)

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(failing(500, renderError)))
	f.flush(t)

	// Every bundle deployment reporting its failure triggers a reconcile of
	// the bundle, with an unchanged summary.
	for range 100 {
		f.emitter.ObserveBundle(context.TODO(), bundle(failing(500, renderError)), bundle(failing(500, renderError)))
	}
	f.clock.advance(2 * f.opts.MinInterval)
	f.flush(t)

	if got := f.events(t); len(got) != 1 {
		t.Fatalf("expected the failure to be reported once, got %d events", len(got))
	}
}

func TestNewCauseIsReported(t *testing.T) {
	f := newFixture(t, nil)

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(failing(500, renderError)))
	f.flush(t)

	f.clock.advance(f.opts.MinInterval)
	f.emitter.ObserveBundle(context.TODO(),
		bundle(failing(500, renderError)),
		bundle(failing(500, renderError, nsForbidden)),
	)
	f.flush(t)

	events := f.events(t)
	if len(events) != 2 {
		t.Fatalf("expected the new cause to be reported, got %d events", len(events))
	}
	if !strings.Contains(events[1].Note, nsForbidden) {
		t.Errorf("expected the second event to name the new cause, got %q", events[1].Note)
	}
	if !strings.Contains(events[0].Note, renderError) || strings.Contains(events[0].Note, nsForbidden) {
		t.Errorf("expected the first event to keep its own note, got %q", events[0].Note)
	}
}

func TestMagnitudeChangeIsReportedOnceItLeavesTheBucket(t *testing.T) {
	f := newFixture(t, nil)

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(failing(500, renderError)))
	f.flush(t)

	// Same order of magnitude, nothing new to say.
	f.clock.advance(f.opts.MinInterval)
	f.emitter.ObserveBundle(context.TODO(), bundle(failing(500, renderError)), bundle(failing(300, renderError)))
	f.flush(t)

	if got := f.events(t); len(got) != 1 {
		t.Fatalf("expected no event for 500 -> 300 failures, got %d events", len(got))
	}

	// An order of magnitude fewer failures is worth reporting.
	f.clock.advance(f.opts.MinInterval)
	f.emitter.ObserveBundle(context.TODO(), bundle(failing(300, renderError)), bundle(failing(12, renderError)))
	f.flush(t)

	events := f.events(t)
	if len(events) != 2 {
		t.Fatalf("expected an event for 300 -> 12 failures, got %d events", len(events))
	}
	if !strings.HasPrefix(events[1].Note, "12/500 bundle deployments failing.") {
		t.Errorf("unexpected note: %q", events[1].Note)
	}
}

func TestRecoveryIsReported(t *testing.T) {
	f := newFixture(t, nil)

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(failing(500, renderError)))
	f.flush(t)

	f.clock.advance(f.opts.MinInterval)
	f.emitter.ObserveBundle(context.TODO(), bundle(failing(500, renderError)), bundle(ready()))
	f.flush(t)

	// Staying ready is not worth another event.
	f.clock.advance(f.opts.MinInterval)
	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(ready()))
	f.flush(t)

	events := f.events(t)
	if len(events) != 2 {
		t.Fatalf("expected the recovery to be reported once, got %d events", len(events))
	}
	if events[1].Type != corev1.EventTypeNormal || events[1].Reason != ReasonReady {
		t.Errorf("unexpected type/reason: %s/%s", events[1].Type, events[1].Reason)
	}
	if events[1].Note != "500/500 bundle deployments ready" {
		t.Errorf("unexpected note: %q", events[1].Note)
	}
}

func TestFailureRecurringAfterARecoveryIsReportedAgain(t *testing.T) {
	f := newFixture(t, nil)
	f.opts.ReportRecovery = false

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(failing(500, renderError)))
	f.flush(t)

	f.clock.advance(f.opts.MinInterval)
	f.emitter.ObserveBundle(context.TODO(), bundle(failing(500, renderError)), bundle(ready()))
	f.flush(t)

	f.clock.advance(f.opts.MinInterval)
	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(failing(500, renderError)))
	f.flush(t)

	events := f.events(t)
	if len(events) != 2 {
		t.Fatalf("expected the recurring failure to be reported, got %d events", len(events))
	}
	for _, event := range events {
		if event.Reason != ReasonDeployFailed {
			t.Errorf("expected only failures to be reported, got %q", event.Reason)
		}
	}
}

func TestOngoingFailureIsNotReportedAfterARestart(t *testing.T) {
	f := newFixture(t, nil)

	// A restart loses what was reported before it, so the state change has to
	// be detected from the persisted status alone.
	f.emitter.ObserveBundle(context.TODO(), bundle(failing(500, renderError)), bundle(failing(500, renderError)))
	f.flush(t)

	if got := f.events(t); len(got) != 0 {
		t.Fatalf("expected no event for an unchanged failure, got %d", len(got))
	}
}

func TestMinIntervalDelaysTheNextEvent(t *testing.T) {
	f := newFixture(t, nil)

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(failing(500, renderError)))
	f.flush(t)

	// A new cause right after the first event has to wait for the minimum
	// interval, and is reported with the state it has by then.
	f.emitter.ObserveBundle(context.TODO(),
		bundle(failing(500, renderError)),
		bundle(failing(500, renderError, nsForbidden)),
	)
	f.flush(t)

	if got := f.events(t); len(got) != 1 {
		t.Fatalf("expected the second event to be held back, got %d events", len(got))
	}

	f.clock.advance(f.opts.MinInterval)
	f.emitter.flush(context.TODO(), *f.opts)

	if got := f.events(t); len(got) != 2 {
		t.Fatalf("expected the second event once the interval passed, got %d events", len(got))
	}
}

func TestRateLimitedEventsStayPending(t *testing.T) {
	f := newFixture(t, flowcontrol.NewFakeNeverRateLimiter())

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(failing(500, renderError)))
	f.flush(t)

	if got := f.events(t); len(got) != 0 {
		t.Fatalf("expected the event to be rate limited, got %d", len(got))
	}

	f.emitter.mu.Lock()
	defer f.emitter.mu.Unlock()

	ent, ok := f.emitter.entries[types.UID("7b3d")]
	if !ok || ent.pending == nil {
		t.Fatal("expected the rate limited event to stay pending")
	}
}

func TestDisabledEmitsNothing(t *testing.T) {
	f := newFixture(t, nil)
	f.opts.Enabled = false

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(failing(500, renderError)))
	f.flush(t)

	if got := f.events(t); len(got) != 0 {
		t.Fatalf("expected no events, got %d", len(got))
	}
}

func TestDisablingDropsQueuedEvents(t *testing.T) {
	f := newFixture(t, nil)

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(failing(500, renderError)))

	if ent := f.bundleEntry(); ent == nil || ent.pending == nil {
		t.Fatal("expected the event to be queued while reporting is enabled")
	}

	f.opts.Enabled = false
	f.flush(t)

	if got := f.events(t); len(got) != 0 {
		t.Fatalf("expected the queued event to be dropped, got %d", len(got))
	}

	// A pending event exempts the entry from the TTL, so it has to be
	// cleared for the object to be forgotten again.
	if ent := f.bundleEntry(); ent != nil && ent.pending != nil {
		t.Fatal("expected the queued event to be cleared")
	}
}

func TestEventNameFitsTheObjectNameLimit(t *testing.T) {
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)

	for _, name := range []string{
		"my-repo-app",
		strings.Repeat("a", 236),
		strings.Repeat("a", 253),
		strings.Repeat("a", 235) + "-",
	} {
		got := eventName(name, now)

		if len(got) > eventNameMaxLength {
			t.Errorf("event name for a %d character object name is %d characters", len(name), len(got))
		}
		if errs := validation.IsDNS1123Subdomain(got); len(errs) > 0 {
			t.Errorf("event name %q for a %d character object name is invalid: %v", got, len(name), errs)
		}
		if !strings.HasSuffix(got, fmt.Sprintf(".%x", now.UnixNano())) {
			t.Errorf("event name %q does not carry the timestamp suffix", got)
		}
	}
}

func deployment(state fleet.BundleState) *fleet.BundleDeployment {
	return &fleet.BundleDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-repo-app",
			Namespace: "cluster-fleet-default-cluster-002-c3d4",
			UID:       types.UID("8f1c"),
			Labels:    map[string]string{fleet.BundleLabel: "my-repo-app"},
		},
		Status: fleet.BundleDeploymentStatus{
			Display: fleet.BundleDeploymentDisplay{State: string(state)},
		},
	}
}

func TestBundleDeploymentsAreOnlyReportedWhenEnabled(t *testing.T) {
	f := newFixture(t, nil)

	f.emitter.ObserveBundleDeployment(context.TODO(), deployment(fleet.Ready), deployment(fleet.ErrApplied))
	f.flush(t)

	if got := f.events(t); len(got) != 0 {
		t.Fatalf("expected per-deployment events to be off by default, got %d", len(got))
	}

	f.opts.PerDeployment = true
	f.emitter.ObserveBundleDeployment(context.TODO(), deployment(fleet.Ready), deployment(fleet.ErrApplied))
	f.flush(t)

	events := f.events(t)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Namespace != "cluster-fleet-default-cluster-002-c3d4" || events[0].Regarding.Kind != "BundleDeployment" {
		t.Errorf("unexpected namespace or regarding object: %s, %+v", events[0].Namespace, events[0].Regarding)
	}
	if !strings.HasPrefix(events[0].Note, "bundle my-repo-app: ErrApplied") {
		t.Errorf("unexpected note: %q", events[0].Note)
	}
}

func TestBundleDeploymentInAnUnchangedStateIsNotReported(t *testing.T) {
	f := newFixture(t, nil)
	f.opts.PerDeployment = true

	f.emitter.ObserveBundleDeployment(context.TODO(), deployment(fleet.ErrApplied), deployment(fleet.ErrApplied))
	f.flush(t)

	if got := f.events(t); len(got) != 0 {
		t.Fatalf("expected no event without a state change, got %d", len(got))
	}
}

// bundleUID is the UID of the bundle the fixture reports on.
const bundleUID = types.UID("7b3d")

func serverBusy() error {
	return apierrors.NewTimeoutError("server is busy", 1)
}

func forbidden() error {
	return apierrors.NewForbidden(
		schema.GroupResource{Group: "events.k8s.io", Resource: "events"},
		"",
		errors.New("events.k8s.io is forbidden"),
	)
}

func TestTransientCreateFailureIsRetried(t *testing.T) {
	f := newFixture(t, nil)

	failures := 2
	f.createErr = func() error {
		if failures == 0 {
			return nil
		}
		failures--

		return serverBusy()
	}

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(failing(500, renderError)))
	f.flush(t)

	if got := f.events(t); len(got) != 0 {
		t.Fatalf("expected the first attempt to fail, got %d events", len(got))
	}

	f.retry(2)

	if got := f.events(t); len(got) != 1 {
		t.Fatalf("expected the event to be created on a retry, got %d events", len(got))
	}
	if ent := f.bundleEntry(); ent == nil || ent.attempts != 0 {
		t.Error("expected the attempts to be reset once the event was created")
	}
}

func TestForbiddenCreateIsNotRetried(t *testing.T) {
	f := newFixture(t, nil)

	attempts := 0
	f.createErr = func() error {
		attempts++

		return forbidden()
	}

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(failing(500, renderError)))
	f.flush(t)
	f.retry(2 * maxCreateAttempts)

	// Retrying a request the API server rejects for what it is only fails
	// the same way again.
	if attempts != 1 {
		t.Errorf("expected a single attempt, got %d", attempts)
	}
	if ent := f.bundleEntry(); ent == nil || ent.pending != nil {
		t.Error("expected the event to be dropped, so that the entry can be reclaimed")
	}
}

func TestCreateIsGivenUpOnAfterTooManyAttempts(t *testing.T) {
	f := newFixture(t, nil)

	attempts := 0
	f.createErr = func() error {
		attempts++

		return serverBusy()
	}

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(failing(500, renderError)))
	f.flush(t)
	f.retry(2 * maxCreateAttempts)

	if attempts != maxCreateAttempts {
		t.Errorf("expected %d attempts, got %d", maxCreateAttempts, attempts)
	}

	// An event which is never given up on would keep its entry out of reach
	// of both the TTL and the size limit.
	ent := f.bundleEntry()
	if ent == nil || ent.pending != nil {
		t.Fatal("expected the event to be dropped")
	}

	f.clock.advance(entryTTL + time.Minute)
	f.emitter.forgetStale()

	if f.bundleEntry() != nil {
		t.Error("expected the entry to be forgotten once its event was dropped")
	}
}

func TestFailureIsReportedAgainAfterGivingUp(t *testing.T) {
	f := newFixture(t, nil)

	f.createErr = serverBusy

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(failing(500, renderError)))
	f.flush(t)
	f.retry(2 * maxCreateAttempts)

	if got := f.events(t); len(got) != 0 {
		t.Fatalf("expected no event while creation fails, got %d", len(got))
	}

	// Whatever made creation fail is fixed, and the bundle fails for a new
	// reason: nothing was lost permanently.
	f.createErr = nil
	f.emitter.ObserveBundle(context.TODO(),
		bundle(failing(500, renderError)), bundle(failing(500, nsForbidden)))
	f.flush(t)

	if got := f.events(t); len(got) != 1 {
		t.Fatalf("expected the new failure to be reported, got %d events", len(got))
	}
}

func TestForgetDropsTheObject(t *testing.T) {
	f := newFixture(t, nil)

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(failing(500, renderError)))
	f.flush(t)

	if f.bundleEntry() == nil {
		t.Fatal("expected the bundle to be remembered")
	}

	f.emitter.Forget(bundleUID)

	if f.bundleEntry() != nil {
		t.Error("expected the bundle to be forgotten")
	}
}

// otherBundle is a bundle which is not the one the other tests report on, so
// that a test can track more than one object.
func otherBundle(uid types.UID, summary fleet.BundleSummary) *fleet.Bundle {
	return &fleet.Bundle{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-repo-" + string(uid),
			Namespace: "fleet-default",
			UID:       uid,
		},
		Status: fleet.BundleStatus{Summary: summary},
	}
}

// fill observes n bundles as failing, one per second, so that they are tracked
// and have distinct times of last observation.
func (f *fixture) fill(n int) {
	for i := range n {
		uid := types.UID(fmt.Sprintf("old-%03d", i))
		f.emitter.ObserveBundle(
			context.TODO(),
			otherBundle(uid, ready()),
			otherBundle(uid, failing(1, renderError)),
		)
		f.clock.advance(time.Second)
	}
}

// eventsFor counts the events created about one object.
func (f *fixture) eventsFor(t *testing.T, uid types.UID) int {
	t.Helper()

	count := 0
	for _, event := range f.events(t) {
		if event.Regarding.UID == uid {
			count++
		}
	}

	return count
}

const newUID = types.UID("brand-new")

func TestEvictionKeepsTheObjectItMakesRoomFor(t *testing.T) {
	f := newFixture(t, nil)
	f.opts.MaxTracked = 10

	f.fill(f.opts.MaxTracked)
	f.flush(t)

	// The object which pushes the emitter over the limit is the one eviction
	// is making room for, so it has to survive it, while an object observed
	// longer ago is forgotten.
	f.emitter.ObserveBundle(
		context.TODO(),
		otherBundle(newUID, ready()),
		otherBundle(newUID, failing(1, nsForbidden)),
	)
	f.flush(t)

	if got := f.eventsFor(t, newUID); got != 1 {
		t.Errorf("expected the newly tracked bundle to be reported once, got %d events", got)
	}
	if len(f.emitter.entries) > f.opts.MaxTracked {
		t.Errorf("expected at most %d entries, got %d", f.opts.MaxTracked, len(f.emitter.entries))
	}
}

func TestEvictionDoesNotDropEventsDuringABurst(t *testing.T) {
	f := newFixture(t, nil)
	f.opts.MaxTracked = 10

	// Nothing is flushed, so every tracked object has an event waiting, which
	// exempts it from eviction. The object observed last must not be evicted
	// for being the only candidate left.
	f.fill(f.opts.MaxTracked)

	f.emitter.ObserveBundle(
		context.TODO(),
		otherBundle(newUID, ready()),
		otherBundle(newUID, failing(1, nsForbidden)),
	)
	f.flush(t)

	if got := f.eventsFor(t, newUID); got != 1 {
		t.Errorf("expected the newly tracked bundle to be reported once, got %d events", got)
	}
}
