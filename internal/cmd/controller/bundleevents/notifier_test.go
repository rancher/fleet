package bundleevents

import (
	"context"
	"errors"
	"fmt"
	"slices"
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

	// targets is the number of clusters the test bundle is deployed to.
	targets = 500

	// bundleUID is the UID of the bundle the fixture reports on.
	bundleUID = types.UID("7b3d")

	// newUID is the UID of a bundle observed for the first time, in the
	// eviction tests.
	newUID = types.UID("brand-new")
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

	// whileCreating runs once, the next time an event is being created,
	// before it is. It observes what happens while a flush writes an event.
	whileCreating func()
}

// failingClient lets a test fail event creation the way the API server would.
type failingClient struct {
	client.Client

	fixture *fixture
}

func (c *failingClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if hook := c.fixture.whileCreating; hook != nil {
		c.fixture.whileCreating = nil
		hook()
	}
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

// flushGrace advances the clock past the grace period of deployments which are
// not ready yet, and creates all due events.
func (f *fixture) flushGrace(t *testing.T) {
	t.Helper()

	f.clock.advance(f.opts.NotReadyGracePeriod + time.Second)
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

// profileOf is the profile of a test bundle's failing deployments. The test
// summaries list every kind of failing deployment they describe, so that a
// profile can be made from them; the emitter itself never reads the list.
func profileOf(s fleet.BundleSummary) *Profile {
	p := &Profile{}
	for _, r := range s.NonReadyResources {
		p.Add(r.Name, r.State, r.Message, r.NonReadyStatus)
	}

	return p
}

func ready() fleet.BundleSummary {
	return fleet.BundleSummary{DesiredReady: targets, Ready: targets}
}

// deploying is the state in between a failure and a recovery: the agent has
// been handed new content, so nothing is failing any more, but nothing is ready
// yet either.
func deploying() fleet.BundleSummary {
	return fleet.BundleSummary{DesiredReady: targets, Pending: targets}
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
	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(failing(1, renderError)), profileOf(failing(1, renderError)))
	f.emitter.ObserveBundle(context.TODO(), bundle(failing(1, renderError)), bundle(failing(500, renderError)), profileOf(failing(500, renderError)))

	if got := f.events(t); len(got) != 0 {
		t.Fatalf("expected no event before the debounce elapsed, got %d", len(got))
	}

	f.flush(t)

	events := f.events(t)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	event := events[0]
	if event.Type != corev1.EventTypeWarning || event.Reason != ReasonBundleDeployFailed || event.Action != actionDeploy {
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

func TestBurstWithinOneMagnitudeBucketCarriesTheSettledCounts(t *testing.T) {
	f := newFixture(t, nil)

	few := failing(2, renderError)
	many := failing(5, renderError)

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(few), profileOf(few))
	f.emitter.ObserveBundle(context.TODO(), bundle(few), bundle(many), profileOf(many))
	f.flush(t)

	events := f.events(t)
	if len(events) != 1 {
		t.Fatalf("expected the burst to collapse into 1 event, got %d", len(events))
	}
	if !strings.HasPrefix(events[0].Note, "5/500 bundle deployments failing.") {
		t.Errorf("expected the note to carry the settled counts, got %q", events[0].Note)
	}
}

func TestUnchangedFailureIsReportedOnce(t *testing.T) {
	f := newFixture(t, nil)

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(failing(500, renderError)), profileOf(failing(500, renderError)))
	f.flush(t)

	// Every bundle deployment reporting its failure triggers a reconcile of
	// the bundle, with an unchanged summary.
	for range 100 {
		f.emitter.ObserveBundle(context.TODO(), bundle(failing(500, renderError)), bundle(failing(500, renderError)), profileOf(failing(500, renderError)))
	}
	f.clock.advance(2 * f.opts.MinInterval)
	f.flush(t)

	if got := f.events(t); len(got) != 1 {
		t.Fatalf("expected the failure to be reported once, got %d events", len(got))
	}
}

func TestNewCauseIsReported(t *testing.T) {
	f := newFixture(t, nil)

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(failing(500, renderError)), profileOf(failing(500, renderError)))
	f.flush(t)

	f.clock.advance(f.opts.MinInterval)
	f.emitter.ObserveBundle(context.TODO(), bundle(failing(500, renderError)), bundle(failing(500, renderError, nsForbidden)), profileOf(failing(500, renderError, nsForbidden)))

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

func TestNotReadyProgressIsNotReportedAgain(t *testing.T) {
	f := newFixture(t, nil)

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(notReadySummary(1, notReadyDeployment("foo", "0/3"))), profileOf(notReadySummary(1, notReadyDeployment("foo", "0/3"))))

	f.flushGrace(t)

	// The rollout advances, then another resource stops being ready.
	f.clock.advance(f.opts.MinInterval)
	f.emitter.ObserveBundle(context.TODO(), bundle(notReadySummary(1, notReadyDeployment("foo", "0/3"))), bundle(notReadySummary(1, notReadyDeployment("foo", "1/3"))), profileOf(notReadySummary(1, notReadyDeployment("foo", "1/3"))))

	f.flushGrace(t)
	f.emitter.ObserveBundle(context.TODO(), bundle(notReadySummary(1, notReadyDeployment("foo", "1/3"))), bundle(notReadySummary(1, notReadyDeployment("foo", "1/3"), notReadyDeployment("bar", "0/1"))), profileOf(notReadySummary(1, notReadyDeployment("foo", "1/3"), notReadyDeployment("bar", "0/1"))))

	f.flushGrace(t)

	// Another resource which is merely not ready yet is the same failure:
	// which resources are not ready changes as the rollout advances.
	events := f.events(t)
	if len(events) != 1 {
		t.Fatalf("expected only the first failure to be reported, got %v", reasons(events))
	}
	if !strings.Contains(events[0].Note, "Available: 0/3;") {
		t.Errorf("expected the event to describe the resource, got %q", events[0].Note)
	}
}

func TestMagnitudeChangeIsReportedOnceItLeavesTheBucket(t *testing.T) {
	f := newFixture(t, nil)

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(failing(500, renderError)), profileOf(failing(500, renderError)))
	f.flush(t)

	// Same order of magnitude, nothing new to say.
	f.clock.advance(f.opts.MinInterval)
	f.emitter.ObserveBundle(context.TODO(), bundle(failing(500, renderError)), bundle(failing(300, renderError)), profileOf(failing(300, renderError)))
	f.flush(t)

	if got := f.events(t); len(got) != 1 {
		t.Fatalf("expected no event for 500 -> 300 failures, got %d events", len(got))
	}

	// An order of magnitude fewer failures is worth reporting.
	f.clock.advance(f.opts.MinInterval)
	f.emitter.ObserveBundle(context.TODO(), bundle(failing(300, renderError)), bundle(failing(12, renderError)), profileOf(failing(12, renderError)))
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

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(failing(500, renderError)), profileOf(failing(500, renderError)))
	f.flush(t)

	f.clock.advance(f.opts.MinInterval)
	f.emitter.ObserveBundle(context.TODO(), bundle(failing(500, renderError)), bundle(ready()), profileOf(ready()))
	f.flush(t)

	// Staying ready is not worth another event.
	f.clock.advance(f.opts.MinInterval)
	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(ready()), profileOf(ready()))
	f.flush(t)

	events := f.events(t)
	if len(events) != 2 {
		t.Fatalf("expected the recovery to be reported once, got %d events", len(events))
	}
	if events[1].Type != corev1.EventTypeNormal || events[1].Reason != ReasonBundleReady {
		t.Errorf("unexpected type/reason: %s/%s", events[1].Type, events[1].Reason)
	}
	if events[1].Note != "500/500 bundle deployments ready" {
		t.Errorf("unexpected note: %q", events[1].Note)
	}
}

func TestRecoveryIsOnlyReportedOnceTheBundleIsReady(t *testing.T) {
	f := newFixture(t, nil)

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(failing(500, renderError)), profileOf(failing(500, renderError)))
	f.flush(t)

	// Fixing the bundle redeploys it, so it stops failing before it is ready.
	f.clock.advance(f.opts.MinInterval)
	f.emitter.ObserveBundle(context.TODO(), bundle(failing(500, renderError)), bundle(deploying()), profileOf(deploying()))
	f.flush(t)

	if got := f.events(t); len(got) != 1 {
		t.Fatalf("expected no event while the bundle is being deployed, got %d", len(got))
	}

	f.clock.advance(f.opts.MinInterval)
	f.emitter.ObserveBundle(context.TODO(), bundle(deploying()), bundle(ready()), profileOf(ready()))
	f.flush(t)

	events := f.events(t)
	if len(events) != 2 {
		t.Fatalf("expected the recovery to be reported once ready, got %d events", len(events))
	}
	if events[1].Reason != ReasonBundleReady {
		t.Fatalf("unexpected reason: %s", events[1].Reason)
	}
	if events[1].Note != "500/500 bundle deployments ready" {
		t.Errorf("expected the note to describe the ready bundle, got %q", events[1].Note)
	}
}

func TestBundleFailingAgainWhileDeployingIsReportedAgain(t *testing.T) {
	f := newFixture(t, nil)

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(failing(500, renderError)), profileOf(failing(500, renderError)))
	f.flush(t)

	// The redeploy hits the same failure again, without ever being ready in
	// between, so no recovery is reported and the failure is.
	f.clock.advance(f.opts.MinInterval)
	f.emitter.ObserveBundle(context.TODO(), bundle(failing(500, renderError)), bundle(deploying()), profileOf(deploying()))
	f.flush(t)

	f.clock.advance(f.opts.MinInterval)
	f.emitter.ObserveBundle(context.TODO(), bundle(deploying()), bundle(failing(500, renderError)), profileOf(failing(500, renderError)))
	f.flush(t)

	events := f.events(t)
	if len(events) != 2 {
		t.Fatalf("expected the recurring failure to be reported, got %d events", len(events))
	}
	for _, event := range events {
		if event.Reason != ReasonBundleDeployFailed {
			t.Errorf("expected only failures to be reported, got %q", event.Reason)
		}
	}
}

func TestBundleWhichLosesItsTargetsIsNotReportedAsRecovered(t *testing.T) {
	f := newFixture(t, nil)

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(failing(500, renderError)), profileOf(failing(500, renderError)))
	f.flush(t)

	// The bundle stops targeting any cluster, which leaves no deployment to
	// call ready.
	none := fleet.BundleSummary{}
	f.clock.advance(f.opts.MinInterval)
	f.emitter.ObserveBundle(context.TODO(), bundle(failing(500, renderError)), bundle(none), profileOf(none))
	f.flush(t)

	f.clock.advance(f.opts.MinInterval)
	f.emitter.ObserveBundle(context.TODO(), bundle(none), bundle(none), profileOf(none))
	f.flush(t)

	if got := f.events(t); len(got) != 1 {
		t.Fatalf("expected no recovery for a bundle without targets, got %d events", len(got))
	}
}

func TestFailureRecurringAfterARecoveryIsReportedAgain(t *testing.T) {
	f := newFixture(t, nil)
	f.opts.ReportRecovery = false

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(failing(500, renderError)), profileOf(failing(500, renderError)))
	f.flush(t)

	f.clock.advance(f.opts.MinInterval)
	f.emitter.ObserveBundle(context.TODO(), bundle(failing(500, renderError)), bundle(ready()), profileOf(ready()))
	f.flush(t)

	f.clock.advance(f.opts.MinInterval)
	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(failing(500, renderError)), profileOf(failing(500, renderError)))
	f.flush(t)

	events := f.events(t)
	if len(events) != 2 {
		t.Fatalf("expected the recurring failure to be reported, got %d events", len(events))
	}
	for _, event := range events {
		if event.Reason != ReasonBundleDeployFailed {
			t.Errorf("expected only failures to be reported, got %q", event.Reason)
		}
	}
}

func TestOngoingFailureIsNotReportedAgainAfterARestart(t *testing.T) {
	// A restart loses what was reported before it, but the persisted status
	// already showed the same failure, so it was reported then. The bundle
	// is observed on every reconcile, also when its status did not change,
	// which must not report it either.
	f := newFixture(t, nil)
	s := failing(500, renderError)

	for range 3 {
		f.emitter.ObserveBundle(context.TODO(), bundle(s), bundle(s), profileOf(s))
		f.flush(t)
		f.clock.advance(f.opts.MinInterval)
	}

	if got := reasons(f.events(t)); len(got) != 0 {
		t.Fatalf("expected the ongoing failure not to be reported again, got %v", got)
	}
}

func TestOngoingFailureIsReportedOnceItChangesAfterARestart(t *testing.T) {
	f := newFixture(t, nil)
	s := failing(500, renderError)

	f.emitter.ObserveBundle(context.TODO(), bundle(s), bundle(s), profileOf(s))
	f.flush(t)
	f.clock.advance(f.opts.MinInterval)
	f.emitter.ObserveBundle(context.TODO(), bundle(s), bundle(failing(500, nsForbidden)), profileOf(failing(500, nsForbidden)))
	f.flush(t)

	events := f.events(t)
	if len(events) != 1 || !strings.Contains(events[0].Note, nsForbidden) {
		t.Fatalf("expected the new cause to be reported, got %v", reasons(events))
	}
}

func TestRecoveryOfAnOngoingFailureIsReportedAfterARestart(t *testing.T) {
	f := newFixture(t, nil)
	s := failing(500, renderError)

	f.emitter.ObserveBundle(context.TODO(), bundle(s), bundle(s), profileOf(s))
	f.flush(t)
	f.clock.advance(f.opts.MinInterval)
	f.emitter.ObserveBundle(context.TODO(), bundle(s), bundle(ready()), profileOf(ready()))
	f.flush(t)

	got := reasons(f.events(t))
	if want := []string{ReasonBundleReady}; !slices.Equal(got, want) {
		t.Fatalf("expected only the recovery, got %v", got)
	}
}

func TestOngoingNotReadyWithAnErrorIsNotReportedAgainAfterARestart(t *testing.T) {
	f := newFixture(t, nil)
	broken := notReadyDeployment("foo", "0/1")
	broken.Summary.Error = true
	s := notReadySummary(3, broken)

	f.emitter.ObserveBundle(context.TODO(), bundle(s), bundle(s), profileOf(s))
	f.flushGrace(t)

	if got := reasons(f.events(t)); len(got) != 0 {
		t.Fatalf("expected the ongoing failure not to be reported again, got %v", got)
	}
}

func TestOngoingProgressingNotReadyIsReportedAfterARestart(t *testing.T) {
	// A deployment which is merely not ready yet may have been within its
	// grace period when the controller stopped, so it may never have been
	// reported. Reporting it is better than missing it.
	f := newFixture(t, nil)
	s := notReadySummary(3, notReadyDeployment("foo", "0/1"))

	f.emitter.ObserveBundle(context.TODO(), bundle(s), bundle(s), profileOf(s))
	f.flush(t)
	if got := reasons(f.events(t)); len(got) != 0 {
		t.Fatalf("expected the grace period to apply, got %v", got)
	}

	f.flushGrace(t)

	got := reasons(f.events(t))
	if want := []string{ReasonBundleNotReady}; !slices.Equal(got, want) {
		t.Fatalf("expected the progressing failure to be reported, got %v", got)
	}
}

func TestForgottenOngoingFailureIsNotReportedAgain(t *testing.T) {
	// Without recovery reporting, nothing keeps the entry of a reported
	// failure, and it is forgotten once it has not been observed for a
	// while. The next resync finds it unchanged.
	f := newFixture(t, nil)
	f.opts.ReportRecovery = false
	s := failing(500, renderError)

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(s), profileOf(s))
	f.flush(t)

	f.clock.advance(entryTTL + time.Minute)
	f.emitter.forgetStale()
	if f.emitter.knows(bundleUID) {
		t.Fatal("expected the bundle to be forgotten")
	}

	f.emitter.ObserveBundle(context.TODO(), bundle(s), bundle(s), profileOf(s))
	f.flush(t)

	got := reasons(f.events(t))
	if want := []string{ReasonBundleDeployFailed}; !slices.Equal(got, want) {
		t.Fatalf("expected the failure to be reported once, got %v", got)
	}
}

func TestNewCauseWithTheSameCountsIsReportedAfterARestart(t *testing.T) {
	// The persisted status only tells the state and how many deployments
	// failed, which a new cause can share with the one reported before.
	f := newFixture(t, nil)

	f.emitter.ObserveBundle(context.TODO(), bundle(failing(500, renderError)), bundle(failing(500, nsForbidden)), profileOf(failing(500, nsForbidden)))
	f.flush(t)

	events := f.events(t)
	if len(events) != 1 || !strings.Contains(events[0].Note, nsForbidden) {
		t.Fatalf("expected the new cause to be reported, got %v", reasons(events))
	}
}

func TestRestartDoesNotAwaitARecoveryWhichIsNotReported(t *testing.T) {
	f := newFixture(t, nil)
	f.opts.ReportRecovery = false

	f.emitter.ObserveBundle(context.TODO(), bundle(failing(500, renderError)), bundle(ready()), profileOf(ready()))

	if f.emitter.knows(bundleUID) {
		t.Error("expected nothing kept for a recovery which is not reported")
	}
}

func TestRecoveryAfterARestartIsReportedAcrossTransientStates(t *testing.T) {
	f := newFixture(t, nil)

	// The fixture's emitter starts empty, as after a restart: the failure is
	// only known from the persisted status, and the bundle is redeployed
	// before it is ready.
	f.emitter.ObserveBundle(context.TODO(), bundle(failing(500, renderError)), bundle(deploying()), profileOf(deploying()))
	f.flush(t)

	f.emitter.ObserveBundle(context.TODO(), bundle(deploying()), bundle(ready()), profileOf(ready()))
	f.flush(t)

	events := f.events(t)
	if len(events) != 1 {
		t.Fatalf("expected the recovery to be reported, got %d events", len(events))
	}
	if events[0].Reason != ReasonBundleReady {
		t.Errorf("unexpected reason: %s", events[0].Reason)
	}
}

func TestMinIntervalDelaysTheNextEvent(t *testing.T) {
	f := newFixture(t, nil)

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(failing(500, renderError)), profileOf(failing(500, renderError)))
	f.flush(t)

	// A new cause right after the first event has to wait for the minimum
	// interval, and is reported with the state it has by then.
	f.emitter.ObserveBundle(context.TODO(), bundle(failing(500, renderError)), bundle(failing(500, renderError, nsForbidden)), profileOf(failing(500, renderError, nsForbidden)))

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

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(failing(500, renderError)), profileOf(failing(500, renderError)))
	f.flush(t)

	if got := f.events(t); len(got) != 0 {
		t.Fatalf("expected the event to be rate limited, got %d", len(got))
	}

	f.emitter.mu.Lock()
	defer f.emitter.mu.Unlock()

	ent, ok := f.emitter.entries[bundleUID]
	if !ok || ent.pending == nil {
		t.Fatal("expected the rate limited event to stay pending")
	}
}

func TestDisabledEmitsNothing(t *testing.T) {
	f := newFixture(t, nil)
	f.opts.Enabled = false

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(failing(500, renderError)), profileOf(failing(500, renderError)))
	f.flush(t)

	if got := f.events(t); len(got) != 0 {
		t.Fatalf("expected no events, got %d", len(got))
	}
}

func TestDisablingDropsQueuedEvents(t *testing.T) {
	f := newFixture(t, nil)

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(failing(500, renderError)), profileOf(failing(500, renderError)))

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
	if events[0].Reason != ReasonBundleDeploymentFailed {
		t.Errorf("expected a deployment-scoped reason, got %q", events[0].Reason)
	}
	if !strings.HasPrefix(events[0].Note, "bundle my-repo-app: ErrApplied") {
		t.Errorf("unexpected note: %q", events[0].Note)
	}
}

func TestBundleDeploymentRecoveryIsReportedAcrossTransientStates(t *testing.T) {
	f := newFixture(t, nil)
	f.opts.PerDeployment = true

	f.emitter.ObserveBundleDeployment(context.TODO(), deployment(fleet.Pending), deployment(fleet.ErrApplied))
	f.flush(t)

	// Redeploying takes the deployment through Pending, so it does not
	// reach Ready straight from the failing state.
	f.clock.advance(f.opts.MinInterval)
	f.emitter.ObserveBundleDeployment(context.TODO(), deployment(fleet.ErrApplied), deployment(fleet.Pending))
	f.flush(t)

	f.clock.advance(f.opts.MinInterval)
	f.emitter.ObserveBundleDeployment(context.TODO(), deployment(fleet.Pending), deployment(fleet.Ready))
	f.flush(t)

	events := f.events(t)
	if len(events) != 2 {
		t.Fatalf("expected the failure and its recovery to be reported, got %d events", len(events))
	}
	if events[1].Type != corev1.EventTypeNormal || events[1].Reason != ReasonBundleDeploymentReady {
		t.Errorf("unexpected type/reason: %s/%s", events[1].Type, events[1].Reason)
	}
	if events[1].Note != "bundle my-repo-app: Ready" {
		t.Errorf("unexpected note: %q", events[1].Note)
	}
}

func TestBundleDeploymentRecoveryAfterARestartIsReportedAcrossTransientStates(t *testing.T) {
	f := newFixture(t, nil)
	f.opts.PerDeployment = true

	// As for bundles, the failure is only known from the persisted status.
	f.emitter.ObserveBundleDeployment(context.TODO(), deployment(fleet.ErrApplied), deployment(fleet.Pending))
	f.flush(t)

	f.emitter.ObserveBundleDeployment(context.TODO(), deployment(fleet.Pending), deployment(fleet.Ready))
	f.flush(t)

	events := f.events(t)
	if len(events) != 1 {
		t.Fatalf("expected the recovery to be reported, got %d events", len(events))
	}
	if events[0].Reason != ReasonBundleDeploymentReady {
		t.Errorf("unexpected reason: %s", events[0].Reason)
	}
}

func TestBundleDeploymentOngoingFailureIsNotReportedAgainAfterARestart(t *testing.T) {
	f := newFixture(t, nil)
	f.opts.PerDeployment = true

	f.emitter.ObserveBundleDeployment(context.TODO(), deployment(fleet.ErrApplied), deployment(fleet.ErrApplied))
	f.flush(t)
	f.clock.advance(f.opts.MinInterval)
	f.emitter.ObserveBundleDeployment(context.TODO(), deployment(fleet.ErrApplied), deployment(fleet.Ready))
	f.flush(t)

	got := reasons(f.events(t))
	if want := []string{ReasonBundleDeploymentReady}; !slices.Equal(got, want) {
		t.Fatalf("expected only the recovery, got %v", got)
	}
}

func TestBundleDeploymentRecoveryWhileTheControllerWasDownIsReported(t *testing.T) {
	// The deployment was not ready, with an error, and that was reported.
	// It recovered while the controller was down: only its display state
	// is persisted from before, its conditions and resources are already
	// those of a ready deployment.
	f := newFixture(t, nil)
	f.opts.PerDeployment = true

	old := deployment(fleet.Ready)
	old.Status.Display.State = string(fleet.NotReady)

	f.emitter.ObserveBundleDeployment(context.TODO(), old, deployment(fleet.Ready))
	f.flush(t)

	got := reasons(f.events(t))
	if want := []string{ReasonBundleDeploymentReady}; !slices.Equal(got, want) {
		t.Fatalf("expected the recovery, got %v", got)
	}
}

func TestBundleDeploymentOngoingProgressingNotReadyIsReportedAfterARestart(t *testing.T) {
	f := newFixture(t, nil)
	f.opts.PerDeployment = true

	f.emitter.ObserveBundleDeployment(context.TODO(), deployment(fleet.NotReady), deployment(fleet.NotReady))
	f.flushGrace(t)

	got := reasons(f.events(t))
	if want := []string{ReasonBundleDeploymentNotReady}; !slices.Equal(got, want) {
		t.Fatalf("expected the progressing failure to be reported, got %v", got)
	}
}

func TestBundleDeploymentFailureWhichChangedAcrossARestartIsReported(t *testing.T) {
	f := newFixture(t, nil)
	f.opts.PerDeployment = true

	f.emitter.ObserveBundleDeployment(context.TODO(), deployment(fleet.Pending), deployment(fleet.ErrApplied))
	f.flush(t)

	got := reasons(f.events(t))
	if want := []string{ReasonBundleDeploymentFailed}; !slices.Equal(got, want) {
		t.Fatalf("expected the failure to be reported, got %v", got)
	}
}

func TestBundleDeploymentReadyWithoutAFailureIsNotReported(t *testing.T) {
	f := newFixture(t, nil)
	f.opts.PerDeployment = true

	f.emitter.ObserveBundleDeployment(context.TODO(), deployment(fleet.Pending), deployment(fleet.Ready))
	f.flush(t)

	if got := f.events(t); len(got) != 0 {
		t.Fatalf("expected no event for a deployment which never failed, got %d", len(got))
	}
}

func TestBundleDeploymentUnchangedFailureIsReportedOnce(t *testing.T) {
	// The reconciler reports a bundle deployment whether or not its display
	// changed, and its old one only differs in the display state, so an
	// unchanged failure is observed again and again.
	f := newFixture(t, nil)
	f.opts.PerDeployment = true

	f.emitter.ObserveBundleDeployment(context.TODO(), deployment(fleet.Pending), deployment(fleet.ErrApplied))
	for range 3 {
		f.flush(t)
		f.clock.advance(f.opts.MinInterval)
		f.emitter.ObserveBundleDeployment(context.TODO(), deployment(fleet.ErrApplied), deployment(fleet.ErrApplied))
	}
	f.flush(t)

	if got := reasons(f.events(t)); len(got) != 1 {
		t.Fatalf("expected one event, got %v", got)
	}
}

func TestBundleDeploymentFailingAgainWithoutRecoveryReportingIsReported(t *testing.T) {
	f := newFixture(t, nil)
	f.opts.PerDeployment = true
	f.opts.ReportRecovery = false

	f.emitter.ObserveBundleDeployment(context.TODO(), deployment(fleet.Pending), deployment(fleet.ErrApplied))
	f.flush(t)
	f.clock.advance(f.opts.MinInterval)
	f.emitter.ObserveBundleDeployment(context.TODO(), deployment(fleet.ErrApplied), deployment(fleet.Ready))
	f.flush(t)
	f.clock.advance(f.opts.MinInterval)
	f.emitter.ObserveBundleDeployment(context.TODO(), deployment(fleet.Ready), deployment(fleet.ErrApplied))
	f.flush(t)

	got := reasons(f.events(t))
	if want := []string{ReasonBundleDeploymentFailed, ReasonBundleDeploymentFailed}; !slices.Equal(got, want) {
		t.Fatalf("expected the failure to be reported each time, got %v", got)
	}
}

func TestReasonsAreScopedToTheKindTheyAreReportedOn(t *testing.T) {
	f := newFixture(t, nil)
	f.opts.PerDeployment = true

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(failing(500, renderError)), profileOf(failing(500, renderError)))
	f.emitter.ObserveBundleDeployment(context.TODO(), deployment(fleet.Ready), deployment(fleet.NotReady))
	f.flushGrace(t)

	byKind := map[string]string{}
	for _, event := range f.events(t) {
		byKind[event.Regarding.Kind] = event.Reason
	}

	// A reason names the kind its event is about, so that alerting can
	// select on the reason alone.
	if byKind["Bundle"] != ReasonBundleDeployFailed {
		t.Errorf("unexpected reason on the bundle: %q", byKind["Bundle"])
	}
	if byKind["BundleDeployment"] != ReasonBundleDeploymentNotReady {
		t.Errorf("unexpected reason on the deployment: %q", byKind["BundleDeployment"])
	}
}

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

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(failing(500, renderError)), profileOf(failing(500, renderError)))
	f.flush(t)

	if got := f.events(t); len(got) != 0 {
		t.Fatalf("expected the first attempt to fail, got %d events", len(got))
	}
	if ent := f.bundleEntry(); ent == nil || ent.attempts != 1 || ent.pending == nil {
		t.Fatal("expected the failed attempt to be counted and the event to be queued for a retry")
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

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(failing(500, renderError)), profileOf(failing(500, renderError)))
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

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(failing(500, renderError)), profileOf(failing(500, renderError)))
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

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(failing(500, renderError)), profileOf(failing(500, renderError)))
	f.flush(t)
	f.retry(2 * maxCreateAttempts)

	if got := f.events(t); len(got) != 0 {
		t.Fatalf("expected no event while creation fails, got %d", len(got))
	}

	// Whatever made creation fail is fixed, and the bundle fails for a new
	// reason. The dropped event stays lost, but giving up does not keep later
	// changes from being reported.
	f.createErr = nil
	f.emitter.ObserveBundle(context.TODO(), bundle(failing(500, renderError)), bundle(failing(500, nsForbidden)), profileOf(failing(500, nsForbidden)))

	f.flush(t)

	if got := f.events(t); len(got) != 1 {
		t.Fatalf("expected the new failure to be reported, got %d events", len(got))
	}
}

func TestForgetDropsTheObject(t *testing.T) {
	f := newFixture(t, nil)

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(failing(500, renderError)), profileOf(failing(500, renderError)))
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
			otherBundle(uid, ready()), otherBundle(

				uid, failing(1, renderError)), profileOf(

				failing(1, renderError)))

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
		otherBundle(newUID, ready()), otherBundle(

			newUID, failing(1, nsForbidden)), profileOf(

			failing(1, nsForbidden)))

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
		otherBundle(newUID, ready()), otherBundle(

			newUID, failing(1, nsForbidden)), profileOf(

			failing(1, nsForbidden)))

	// With every entry exempt, the limit is exceeded instead.
	if len(f.emitter.entries) <= f.opts.MaxTracked {
		t.Errorf("expected more than %d entries during the burst, got %d", f.opts.MaxTracked, len(f.emitter.entries))
	}

	f.flush(t)

	if got := f.eventsFor(t, newUID); got != 1 {
		t.Errorf("expected the newly tracked bundle to be reported once, got %d events", got)
	}

	// Once the events are created, the next object to be tracked brings the
	// emitter back within the limit.
	f.emitter.ObserveBundle(
		context.TODO(),
		otherBundle("after-burst", ready()), otherBundle(

			"after-burst", failing(1, nsForbidden)), profileOf(

			failing(1, nsForbidden)))

	if len(f.emitter.entries) > f.opts.MaxTracked {
		t.Errorf("expected at most %d entries after the burst, got %d", f.opts.MaxTracked, len(f.emitter.entries))
	}
}

// erroring is a resource the agent reports as failed, like a Deployment past
// its progress deadline.
func erroring(r fleet.NonReadyStatus) fleet.NonReadyStatus {
	r.Summary.Error = true
	r.Summary.Transitioning = false
	r.Summary.Message = []string{"Progress deadline exceeded"}

	return r
}

func TestRolloutWithinTheGracePeriodIsNotReported(t *testing.T) {
	// QA step 4 of issue 4455: a new bundle is not ready while its pods
	// start, then ready. Nothing failed, so nothing is reported.
	f := newFixture(t, nil)

	f.emitter.ObserveBundle(context.TODO(), bundle(deploying()), bundle(notReadySummary(1, notReadyDeployment("foo", "0/1"))), profileOf(notReadySummary(1, notReadyDeployment("foo", "0/1"))))

	f.flush(t)
	f.emitter.ObserveBundle(context.TODO(), bundle(notReadySummary(1, notReadyDeployment("foo", "0/1"))), bundle(fleet.BundleSummary{DesiredReady: 1, Ready: 1}), profileOf(fleet.BundleSummary{DesiredReady: 1, Ready: 1}))

	f.flushGrace(t)

	if events := f.events(t); len(events) != 0 {
		t.Fatalf("expected a rollout within the grace period not to be reported, got %v", reasons(events))
	}
}

func TestNotReadyPastTheGracePeriodIsReportedWithItsRecovery(t *testing.T) {
	f := newFixture(t, nil)

	f.emitter.ObserveBundle(context.TODO(), bundle(deploying()), bundle(notReadySummary(1, notReadyDeployment("foo", "0/1"))), profileOf(notReadySummary(1, notReadyDeployment("foo", "0/1"))))

	f.flush(t)
	if events := f.events(t); len(events) != 0 {
		t.Fatalf("expected nothing to be reported within the grace period, got %v", reasons(events))
	}

	f.flushGrace(t)
	f.emitter.ObserveBundle(context.TODO(), bundle(notReadySummary(1, notReadyDeployment("foo", "0/1"))), bundle(fleet.BundleSummary{DesiredReady: 1, Ready: 1}), profileOf(fleet.BundleSummary{DesiredReady: 1, Ready: 1}))

	f.flushGrace(t)

	got := reasons(f.events(t))
	if want := []string{ReasonBundleNotReady, ReasonBundleReady}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("expected %v, got %v", want, got)
	}
}

func TestErrorWithinTheGracePeriodIsReportedAfterTheDebounce(t *testing.T) {
	f := newFixture(t, nil)

	f.emitter.ObserveBundle(context.TODO(), bundle(deploying()), bundle(notReadySummary(1, notReadyDeployment("foo", "0/1"))), profileOf(notReadySummary(1, notReadyDeployment("foo", "0/1"))))

	f.clock.advance(time.Second)
	f.emitter.ObserveBundle(context.TODO(), bundle(notReadySummary(1, notReadyDeployment("foo", "0/1"))), bundle(notReadySummary(1, erroring(notReadyDeployment("foo", "0/1")))), profileOf(notReadySummary(1, erroring(notReadyDeployment("foo", "0/1")))))

	f.flush(t)

	events := f.events(t)
	if len(events) != 1 || events[0].Reason != ReasonBundleNotReady {
		t.Fatalf("expected the error to be reported after the debounce, got %v", reasons(events))
	}
	if !strings.Contains(events[0].Note, "Progress deadline exceeded") {
		t.Errorf("expected the note to describe the error, got %q", events[0].Note)
	}
}

func TestErrorTurningBackIntoProgressStartsTheGracePeriod(t *testing.T) {
	f := newFixture(t, nil)

	f.emitter.ObserveBundle(context.TODO(), bundle(deploying()), bundle(notReadySummary(1, erroring(notReadyDeployment("foo", "0/1")))), profileOf(notReadySummary(1, erroring(notReadyDeployment("foo", "0/1")))))

	f.clock.advance(time.Second)
	f.emitter.ObserveBundle(context.TODO(), bundle(notReadySummary(1, erroring(notReadyDeployment("foo", "0/1")))), bundle(notReadySummary(1, notReadyDeployment("foo", "0/1"))), profileOf(notReadySummary(1, notReadyDeployment("foo", "0/1"))))

	f.flush(t)

	if events := f.events(t); len(events) != 0 {
		t.Fatalf("expected the progress to wait for its grace period, got %v", reasons(events))
	}

	f.flushGrace(t)
	if events := f.events(t); len(events) != 1 {
		t.Fatalf("expected the progress to be reported after its grace period, got %v", reasons(events))
	}
}

func TestFailureOverWithinTheDebounceIsNotReportedNorItsRecovery(t *testing.T) {
	f := newFixture(t, nil)

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(failing(500, renderError)), profileOf(failing(500, renderError)))
	f.emitter.ObserveBundle(context.TODO(), bundle(failing(500, renderError)), bundle(ready()), profileOf(ready()))
	f.flush(t)

	if events := f.events(t); len(events) != 0 {
		t.Fatalf("expected neither the failure nor a recovery, got %v", reasons(events))
	}
}

func TestFailureOverWhileDeployingIsNotReported(t *testing.T) {
	f := newFixture(t, nil)

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(failing(500, renderError)), profileOf(failing(500, renderError)))
	f.emitter.ObserveBundle(context.TODO(), bundle(failing(500, renderError)), bundle(deploying()), profileOf(deploying()))
	f.flush(t)
	f.emitter.ObserveBundle(context.TODO(), bundle(deploying()), bundle(ready()), profileOf(ready()))
	f.flush(t)

	if events := f.events(t); len(events) != 0 {
		t.Fatalf("expected a failure superseded before it was reported to stay unreported, got %v", reasons(events))
	}
}

func TestRecoveryOfAnyFailureIsReportedAfterARestart(t *testing.T) {
	// After a restart, the emitter cannot tell whether a deployment which
	// was not ready yet was reported: only the summary's counters are
	// persisted, and the deployments it lists are a sample. Any failure is
	// assumed to have been reported, so its recovery is too.
	f := newFixture(t, nil)

	f.emitter.ObserveBundle(context.TODO(), bundle(notReadySummary(1, notReadyDeployment("foo", "0/1"))), bundle(fleet.BundleSummary{DesiredReady: 1, Ready: 1}), profileOf(fleet.BundleSummary{DesiredReady: 1, Ready: 1}))

	f.flushGrace(t)

	got := reasons(f.events(t))
	if want := []string{ReasonBundleReady}; !slices.Equal(got, want) {
		t.Fatalf("expected the recovery, got %v", got)
	}
}

func TestBundleDeploymentRolloutWithinTheGracePeriodIsNotReported(t *testing.T) {
	f := newFixture(t, nil)
	f.opts.PerDeployment = true

	f.emitter.ObserveBundleDeployment(context.TODO(), deployment(fleet.WaitApplied), deployment(fleet.NotReady))
	f.flush(t)
	f.emitter.ObserveBundleDeployment(context.TODO(), deployment(fleet.NotReady), deployment(fleet.Ready))
	f.flushGrace(t)

	if events := f.events(t); len(events) != 0 {
		t.Fatalf("expected a rollout within the grace period not to be reported, got %v", reasons(events))
	}
}

func TestBundleDeploymentErrorWithinTheGracePeriodIsReportedAfterTheDebounce(t *testing.T) {
	f := newFixture(t, nil)
	f.opts.PerDeployment = true

	progressing := deployment(fleet.NotReady)
	progressing.Status.NonReadyStatus = []fleet.NonReadyStatus{notReadyDeployment("foo", "0/1")}
	failed := deployment(fleet.NotReady)
	failed.Status.NonReadyStatus = []fleet.NonReadyStatus{erroring(notReadyDeployment("foo", "0/1"))}

	// As the reconciler passes them: old only differs from cur in the
	// display state, the resources are the agent's latest in both.
	f.emitter.ObserveBundleDeployment(context.TODO(), withDisplayState(progressing, fleet.WaitApplied), progressing)
	f.clock.advance(time.Second)
	f.emitter.ObserveBundleDeployment(context.TODO(), failed, failed)
	f.flush(t)

	events := f.events(t)
	if len(events) != 1 || events[0].Reason != ReasonBundleDeploymentNotReady {
		t.Fatalf("expected the error to be reported after the debounce, got %v", reasons(events))
	}
}

func TestBundleDeploymentErrorAfterItsRolloutWasReportedIsReported(t *testing.T) {
	f := newFixture(t, nil)
	f.opts.PerDeployment = true

	progressing := deployment(fleet.NotReady)
	progressing.Status.NonReadyStatus = []fleet.NonReadyStatus{notReadyDeployment("foo", "0/1")}
	failed := deployment(fleet.NotReady)
	failed.Status.NonReadyStatus = []fleet.NonReadyStatus{erroring(notReadyDeployment("foo", "0/1"))}

	f.emitter.ObserveBundleDeployment(context.TODO(), withDisplayState(progressing, fleet.WaitApplied), progressing)
	f.flushGrace(t)
	f.clock.advance(f.opts.MinInterval)
	f.emitter.ObserveBundleDeployment(context.TODO(), failed, failed)
	f.flush(t)

	got := reasons(f.events(t))
	if want := []string{ReasonBundleDeploymentNotReady, ReasonBundleDeploymentNotReady}; !slices.Equal(got, want) {
		t.Fatalf("expected the rollout and then its error, got %v", got)
	}
}

// withDisplayState returns a copy of a bundle deployment with another display
// state, as the reconciler's copy of it from before it updated the display.
func withDisplayState(bd *fleet.BundleDeployment, state fleet.BundleState) *fleet.BundleDeployment {
	old := bd.DeepCopy()
	old.Status.Display.State = string(state)

	return old
}

func reasons(events []eventsv1.Event) []string {
	got := make([]string, 0, len(events))
	for _, event := range events {
		got = append(got, event.Reason)
	}

	return got
}

// refuseOnce is a rate limiter which refuses the first event, after running a
// hook in between the event being claimed and being put back.
type refuseOnce struct {
	flowcontrol.RateLimiter

	whileRefusing func()
}

func (l *refuseOnce) TryAccept() bool {
	if hook := l.whileRefusing; hook != nil {
		l.whileRefusing = nil
		hook()

		return false
	}

	return true
}

func TestRecoveryWhileTheFailureIsBeingCreatedIsReported(t *testing.T) {
	f := newFixture(t, nil)

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(failing(500, renderError)), profileOf(failing(500, renderError)))
	f.whileCreating = func() {
		f.emitter.ObserveBundle(context.TODO(), bundle(failing(500, renderError)), bundle(ready()), profileOf(ready()))
	}
	f.flush(t)

	f.clock.advance(f.opts.MinInterval)
	f.flush(t)

	got := reasons(f.events(t))
	if want := []string{ReasonBundleDeployFailed, ReasonBundleReady}; !slices.Equal(got, want) {
		t.Fatalf("expected the failure and its recovery, got %v", got)
	}
}

func TestBundleDeploymentRecoveryWhileTheFailureIsBeingCreatedIsReported(t *testing.T) {
	f := newFixture(t, nil)
	f.opts.PerDeployment = true

	f.emitter.ObserveBundleDeployment(context.TODO(), deployment(fleet.Ready), deployment(fleet.ErrApplied))
	f.whileCreating = func() {
		f.emitter.ObserveBundleDeployment(context.TODO(), deployment(fleet.ErrApplied), deployment(fleet.Ready))
	}
	f.flush(t)

	f.clock.advance(f.opts.MinInterval)
	f.flush(t)

	got := reasons(f.events(t))
	if want := []string{ReasonBundleDeploymentFailed, ReasonBundleDeploymentReady}; !slices.Equal(got, want) {
		t.Fatalf("expected the failure and its recovery, got %v", got)
	}
}

func TestRecoveryWhileTheFailureIsBeingCreatedIsNotReportedIfDisabled(t *testing.T) {
	f := newFixture(t, nil)

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(failing(500, renderError)), profileOf(failing(500, renderError)))
	f.whileCreating = func() {
		f.opts.ReportRecovery = false
		f.emitter.ObserveBundle(context.TODO(), bundle(failing(500, renderError)), bundle(ready()), profileOf(ready()))
	}
	f.flush(t)

	f.clock.advance(f.opts.MinInterval)
	f.flush(t)

	got := reasons(f.events(t))
	if want := []string{ReasonBundleDeployFailed}; !slices.Equal(got, want) {
		t.Fatalf("expected only the failure, got %v", got)
	}
}

func TestFailingAgainWhileTheFailureIsBeingCreatedReportsNoRecovery(t *testing.T) {
	f := newFixture(t, nil)

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(failing(500, renderError)), profileOf(failing(500, renderError)))
	f.whileCreating = func() {
		f.emitter.ObserveBundle(context.TODO(), bundle(failing(500, renderError)), bundle(ready()), profileOf(ready()))
		f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(failing(500, renderError)), profileOf(failing(500, renderError)))
	}
	f.flush(t)

	// The failure being created is the one the bundle is back in, so it is
	// not reported a second time either.
	f.clock.advance(f.opts.MinInterval)
	f.flush(t)

	got := reasons(f.events(t))
	if want := []string{ReasonBundleDeployFailed}; !slices.Equal(got, want) {
		t.Fatalf("expected only the failure, got %v", got)
	}

	// It is still a reported failure, whose recovery is reported.
	f.emitter.ObserveBundle(context.TODO(), bundle(failing(500, renderError)), bundle(ready()), profileOf(ready()))
	f.flush(t)

	got = reasons(f.events(t))
	if want := []string{ReasonBundleDeployFailed, ReasonBundleReady}; !slices.Equal(got, want) {
		t.Fatalf("expected the failure and its recovery, got %v", got)
	}
}

func TestFailureEndingWhileItsEventIsBeingCreatedIsReportedAgainOnRecurrence(t *testing.T) {
	f := newFixture(t, nil)

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(failing(500, renderError)), profileOf(failing(500, renderError)))
	f.whileCreating = func() {
		f.emitter.ObserveBundle(context.TODO(), bundle(failing(500, renderError)), bundle(deploying()), profileOf(deploying()))
	}
	f.flush(t)

	// The same failure, after the bundle stopped failing in between, is a
	// new occurrence.
	f.clock.advance(f.opts.MinInterval)
	f.emitter.ObserveBundle(context.TODO(), bundle(deploying()), bundle(failing(500, renderError)), profileOf(failing(500, renderError)))
	f.flush(t)

	got := reasons(f.events(t))
	if want := []string{ReasonBundleDeployFailed, ReasonBundleDeployFailed}; !slices.Equal(got, want) {
		t.Fatalf("expected the failure to be reported again, got %v", got)
	}
}

func TestFailureEndingWhileItsEventFailsToBeCreatedIsNotRetried(t *testing.T) {
	f := newFixture(t, nil)
	f.createErr = serverBusy

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(failing(500, renderError)), profileOf(failing(500, renderError)))
	f.whileCreating = func() {
		f.emitter.ObserveBundle(context.TODO(), bundle(failing(500, renderError)), bundle(ready()), profileOf(ready()))
	}
	f.flush(t)

	f.createErr = nil
	f.retry(2)

	if got := reasons(f.events(t)); len(got) != 0 {
		t.Fatalf("expected neither the failure nor its recovery, got %v", got)
	}
	if ent := f.bundleEntry(); ent == nil || ent.pending != nil || ent.awaitingRecovery {
		t.Error("expected nothing to be queued nor awaited")
	}
}

func TestFailureEndingWhileItsEventIsRateLimitedIsDropped(t *testing.T) {
	limiter := &refuseOnce{}
	f := newFixture(t, limiter)

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(failing(500, renderError)), profileOf(failing(500, renderError)))
	limiter.whileRefusing = func() {
		f.emitter.ObserveBundle(context.TODO(), bundle(failing(500, renderError)), bundle(ready()), profileOf(ready()))
	}
	f.flush(t)
	f.retry(2)

	if got := reasons(f.events(t)); len(got) != 0 {
		t.Fatalf("expected neither the failure nor its recovery, got %v", got)
	}
}

func TestRecoveryIsReportedWhenTheFailureReplacingItEndsUnreported(t *testing.T) {
	f := newFixture(t, nil)

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(failing(500, renderError)), profileOf(failing(500, renderError)))
	f.flush(t)

	// The recovery waits for the minimum interval, and is replaced by
	// another failure in the meantime, which ends before it is reported.
	f.emitter.ObserveBundle(context.TODO(), bundle(failing(500, renderError)), bundle(ready()), profileOf(ready()))
	f.clock.advance(time.Second)
	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(failing(500, nsForbidden)), profileOf(failing(500, nsForbidden)))
	f.clock.advance(time.Second)
	f.emitter.ObserveBundle(context.TODO(), bundle(failing(500, nsForbidden)), bundle(ready()), profileOf(ready()))

	f.clock.advance(f.opts.MinInterval)
	f.flush(t)

	got := reasons(f.events(t))
	if want := []string{ReasonBundleDeployFailed, ReasonBundleReady}; !slices.Equal(got, want) {
		t.Fatalf("expected the reported failure and its recovery, got %v", got)
	}
}

func TestBundleDeploymentRecoveryIsReportedWhenTheFailureReplacingItEndsUnreported(t *testing.T) {
	f := newFixture(t, nil)
	f.opts.PerDeployment = true

	f.emitter.ObserveBundleDeployment(context.TODO(), deployment(fleet.Ready), deployment(fleet.ErrApplied))
	f.flush(t)

	f.emitter.ObserveBundleDeployment(context.TODO(), deployment(fleet.ErrApplied), deployment(fleet.Ready))
	f.clock.advance(time.Second)
	f.emitter.ObserveBundleDeployment(context.TODO(), deployment(fleet.Ready), deployment(fleet.NotReady))
	f.clock.advance(time.Second)
	f.emitter.ObserveBundleDeployment(context.TODO(), deployment(fleet.NotReady), deployment(fleet.Ready))

	f.clock.advance(f.opts.MinInterval)
	f.flush(t)

	got := reasons(f.events(t))
	if want := []string{ReasonBundleDeploymentFailed, ReasonBundleDeploymentReady}; !slices.Equal(got, want) {
		t.Fatalf("expected the reported failure and its recovery, got %v", got)
	}
}

func TestRecoveryIsReportedOnceItsEventIsCreated(t *testing.T) {
	f := newFixture(t, nil)

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(failing(500, renderError)), profileOf(failing(500, renderError)))
	f.flush(t)
	f.clock.advance(f.opts.MinInterval)
	f.emitter.ObserveBundle(context.TODO(), bundle(failing(500, renderError)), bundle(ready()), profileOf(ready()))
	f.flush(t)

	// Observing the recovery again is not another recovery.
	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(ready()), profileOf(ready()))
	f.emitter.ObserveBundle(context.TODO(), bundle(deploying()), bundle(ready()), profileOf(ready()))
	f.clock.advance(f.opts.MinInterval)
	f.flush(t)

	got := reasons(f.events(t))
	if want := []string{ReasonBundleDeployFailed, ReasonBundleReady}; !slices.Equal(got, want) {
		t.Fatalf("expected one recovery, got %v", got)
	}
}

func TestRecoveryOfAReportedNotReadyIsReportedAfterAnHourWithoutChange(t *testing.T) {
	f := newFixture(t, nil)

	stuck := notReadySummary(1, notReadyDeployment("foo", "0/1"))
	f.emitter.ObserveBundle(context.TODO(), bundle(fleet.BundleSummary{DesiredReady: 1, Pending: 1}), bundle(stuck), profileOf(stuck))
	f.flushGrace(t)

	// Nothing changes for longer than the emitter remembers idle objects.
	f.clock.advance(2 * entryTTL)
	f.emitter.forgetStale()

	f.emitter.ObserveBundle(context.TODO(), bundle(stuck), bundle(fleet.BundleSummary{DesiredReady: 1, Ready: 1}), profileOf(fleet.BundleSummary{DesiredReady: 1, Ready: 1}))
	f.flush(t)

	got := reasons(f.events(t))
	if want := []string{ReasonBundleNotReady, ReasonBundleReady}; !slices.Equal(got, want) {
		t.Fatalf("expected the failure and its recovery, got %v", got)
	}
}

func TestEvictionForgetsObjectsOwingARecoveryLast(t *testing.T) {
	f := newFixture(t, nil)
	f.opts.MaxTracked = 10

	// The oldest object owes a recovery, the others only remember a failure
	// which ended unreported.
	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(failing(500, renderError)), profileOf(failing(500, renderError)))
	f.flush(t)
	for i := range f.opts.MaxTracked {
		uid := types.UID(fmt.Sprintf("old-%03d", i))
		f.emitter.ObserveBundle(context.TODO(), otherBundle(uid, ready()), otherBundle(uid, failing(1, renderError)), profileOf(failing(1, renderError)))
		f.emitter.ObserveBundle(context.TODO(), otherBundle(uid, failing(1, renderError)), otherBundle(uid, ready()), profileOf(ready()))
		f.clock.advance(time.Second)
	}

	if ent := f.bundleEntry(); ent == nil || !ent.awaitingRecovery {
		t.Fatal("expected the object owing a recovery to be kept")
	}
}

func TestRecoveryOfAFailureReportedBeforeARestartSurvivesAnotherFailure(t *testing.T) {
	// The emitter is new, as after a restart, and the first change it sees
	// is from one failure to another, which ends before it is reported.
	f := newFixture(t, nil)

	f.emitter.ObserveBundle(context.TODO(), bundle(failing(500, renderError)), bundle(failing(500, nsForbidden)), profileOf(failing(500, nsForbidden)))
	f.clock.advance(time.Second)
	f.emitter.ObserveBundle(context.TODO(), bundle(failing(500, nsForbidden)), bundle(ready()), profileOf(ready()))
	f.flush(t)

	got := reasons(f.events(t))
	if want := []string{ReasonBundleReady}; !slices.Equal(got, want) {
		t.Fatalf("expected the recovery of the failure reported before the restart, got %v", got)
	}
}

func TestNothingIsReportedWithoutAProfile(t *testing.T) {
	// Without a profile, the bundle's deployments were not looked at, and
	// the summary only lists a sample of them, which events do not rely on.
	f := newFixture(t, nil)

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(failing(500, renderError)), nil)
	f.flushGrace(t)

	if got := reasons(f.events(t)); len(got) != 0 {
		t.Fatalf("expected nothing reported, got %v", got)
	}
}

func TestFailureWhichGrewAcrossARestartIsReported(t *testing.T) {
	// Restarted, and many more deployments fail than the persisted status
	// counted: that is not the failure which was reported.
	f := newFixture(t, nil)

	f.emitter.ObserveBundle(context.TODO(), bundle(failing(1, renderError)), bundle(failing(500, renderError)), profileOf(failing(500, renderError)))
	f.flush(t)

	got := reasons(f.events(t))
	if want := []string{ReasonBundleDeployFailed}; !slices.Equal(got, want) {
		t.Fatalf("expected the grown failure, got %v", got)
	}
}

func TestEventsOfAnObjectAreCreatedOneAtATime(t *testing.T) {
	f := newFixture(t, nil)

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(failing(500, renderError)), profileOf(failing(500, renderError)))
	f.whileCreating = func() {
		// Creating the event takes longer than the debounce, during
		// which another failure becomes due.
		f.emitter.ObserveBundle(context.TODO(), bundle(failing(500, renderError)), bundle(failing(500, nsForbidden)), profileOf(failing(500, nsForbidden)))
		f.clock.advance(f.opts.Debounce + time.Second)
		f.emitter.flush(context.TODO(), *f.opts)

		if got := f.events(t); len(got) != 0 {
			t.Errorf("expected nothing to be created while an event is, got %v", reasons(got))
		}
	}
	f.flush(t)

	f.clock.advance(f.opts.MinInterval)
	f.flush(t)

	events := f.events(t)
	if len(events) != 2 {
		t.Fatalf("expected both failures, one after the other, got %v", reasons(events))
	}
	if !strings.Contains(events[0].Note, renderError) || !strings.Contains(events[1].Note, nsForbidden) {
		t.Errorf("expected the failures in the order they were observed, got %q, %q", events[0].Note, events[1].Note)
	}
}

// A reported failure is replaced by another, whose event fails to be created
// while the bundle recovers. The second failure is not reported, but the first
// one still owes its recovery.
func TestRecoveryIsReportedWhenTheFailureReplacingItFailsToBeCreated(t *testing.T) {
	f := newFixture(t, nil)

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(failing(500, renderError)), profileOf(failing(500, renderError)))
	f.flush(t)

	f.emitter.ObserveBundle(context.TODO(), bundle(failing(500, renderError)), bundle(failing(500, nsForbidden)), profileOf(failing(500, nsForbidden)))
	f.createErr = serverBusy
	f.whileCreating = func() {
		f.emitter.ObserveBundle(context.TODO(), bundle(failing(500, nsForbidden)), bundle(ready()), profileOf(ready()))
	}
	f.clock.advance(f.opts.MinInterval)
	f.flush(t)

	f.createErr = nil
	f.flush(t)

	got := reasons(f.events(t))
	if want := []string{ReasonBundleDeployFailed, ReasonBundleReady}; !slices.Equal(got, want) {
		t.Fatalf("expected the reported failure and its recovery, got %v", got)
	}
	if ent := f.bundleEntry(); ent == nil || ent.pending != nil || ent.awaitingRecovery {
		t.Error("expected the recovery to be settled")
	}
}

// As above, with the second failure's event refused by the rate limiter.
func TestRecoveryIsReportedWhenTheFailureReplacingItIsRateLimited(t *testing.T) {
	limiter := &refuseOnce{}
	f := newFixture(t, limiter)

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(failing(500, renderError)), profileOf(failing(500, renderError)))
	f.flush(t)

	f.emitter.ObserveBundle(context.TODO(), bundle(failing(500, renderError)), bundle(failing(500, nsForbidden)), profileOf(failing(500, nsForbidden)))
	limiter.whileRefusing = func() {
		f.emitter.ObserveBundle(context.TODO(), bundle(failing(500, nsForbidden)), bundle(ready()), profileOf(ready()))
	}
	f.clock.advance(f.opts.MinInterval)
	f.flush(t)
	f.flush(t)

	got := reasons(f.events(t))
	if want := []string{ReasonBundleDeployFailed, ReasonBundleReady}; !slices.Equal(got, want) {
		t.Fatalf("expected the reported failure and its recovery, got %v", got)
	}
	if ent := f.bundleEntry(); ent == nil || ent.pending != nil || ent.awaitingRecovery {
		t.Error("expected the recovery to be settled")
	}
}

func TestBundleDeploymentRecoveryIsReportedWhenTheFailureReplacingItFailsToBeCreated(t *testing.T) {
	f := newFixture(t, nil)
	f.opts.PerDeployment = true

	f.emitter.ObserveBundleDeployment(context.TODO(), deployment(fleet.Ready), deployment(fleet.ErrApplied))
	f.flush(t)

	f.emitter.ObserveBundleDeployment(context.TODO(), deployment(fleet.ErrApplied), deployment(fleet.NotReady))
	f.createErr = serverBusy
	f.whileCreating = func() {
		f.emitter.ObserveBundleDeployment(context.TODO(), deployment(fleet.NotReady), deployment(fleet.Ready))
	}
	f.flushGrace(t)

	f.createErr = nil
	f.flush(t)

	got := reasons(f.events(t))
	if want := []string{ReasonBundleDeploymentFailed, ReasonBundleDeploymentReady}; !slices.Equal(got, want) {
		t.Fatalf("expected the reported failure and its recovery, got %v", got)
	}
}

// rollout is the summary of a bundle rolling out to 500 clusters. The 10
// deployments it lists are still waiting to be applied, so none of the ones
// which are not ready is listed.
func rollout() fleet.BundleSummary {
	s := fleet.BundleSummary{DesiredReady: 500, Ready: 63, NotReady: 37, WaitApplied: 400}
	for i := range 10 {
		s.NonReadyResources = append(s.NonReadyResources, fleet.NonReadyResource{
			Name:  fmt.Sprintf("fleet-default/cluster-%03d", i),
			State: fleet.WaitApplied,
		})
	}

	return s
}

func TestRolloutIsGivenTheGracePeriodWhenItsNotReadyDeploymentsAreNotListed(t *testing.T) {
	f := newFixture(t, nil)
	profile := &Profile{NotReadyProgressing: 37}

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(rollout()), profile)
	f.flush(t)

	if got := reasons(f.events(t)); len(got) != 0 {
		t.Fatalf("expected nothing within the grace period, got %v", got)
	}

	f.flushGrace(t)

	got := reasons(f.events(t))
	if want := []string{ReasonBundleNotReady}; !slices.Equal(got, want) {
		t.Fatalf("expected the failure after the grace period, got %v", got)
	}
}

func TestErrorOnADeploymentTheSummaryDoesNotListIsReportedAfterTheDebounce(t *testing.T) {
	f := newFixture(t, nil)

	// Every listed deployment is progressing, but one which is not listed
	// reports an error.
	s := notReadySummary(10, notReadyDeployment("web", "1/3"))
	s.DesiredReady, s.NotReady = 50, 37
	profile := &Profile{NotReadyProgressing: 36, NotReadyWithError: 1}

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(s), profile)
	f.flush(t)

	got := reasons(f.events(t))
	if want := []string{ReasonBundleNotReady}; !slices.Equal(got, want) {
		t.Fatalf("expected the failure after the debounce, got %v", got)
	}
}

func TestRolloutKeepsItsGracePeriodWhileTheListedDeploymentsChange(t *testing.T) {
	f := newFixture(t, nil)
	profile := &Profile{NotReadyProgressing: 37}

	// Which deployments the summary lists changes as agents apply, from
	// none which are not ready to some and back.
	listingSome := rollout()
	listingSome.NonReadyResources[0] = fleet.NonReadyResource{
		Name:           "fleet-default/cluster-000",
		State:          fleet.NotReady,
		NonReadyStatus: []fleet.NonReadyStatus{notReadyDeployment("web", "1/3")},
	}

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(rollout()), profile)
	f.clock.advance(time.Second)
	f.emitter.ObserveBundle(context.TODO(), bundle(rollout()), bundle(listingSome), profile)
	f.clock.advance(time.Second)
	f.emitter.ObserveBundle(context.TODO(), bundle(listingSome), bundle(rollout()), profile)
	f.flush(t)

	if got := reasons(f.events(t)); len(got) != 0 {
		t.Fatalf("expected nothing within the grace period, got %v", got)
	}
}

func TestFailureWhichEndsBackInTheReportedOneIsNotReported(t *testing.T) {
	// Another cause, which went away again before it was due: the bundle is
	// failing for the cause already reported, as it was all along.
	f := newFixture(t, nil)
	a := failing(500, renderError)
	b := failing(500, nsForbidden)

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(a), profileOf(a))
	f.flush(t)
	f.emitter.ObserveBundle(context.TODO(), bundle(a), bundle(b), profileOf(b))
	f.clock.advance(time.Second)
	f.emitter.ObserveBundle(context.TODO(), bundle(b), bundle(a), profileOf(a))
	f.clock.advance(f.opts.MinInterval)
	f.flush(t)

	if got := reasons(f.events(t)); len(got) != 1 {
		t.Fatalf("expected only the failure reported first, got %v", got)
	}
}

func TestErrorWhichClearsBeforeItIsDueIsNotReported(t *testing.T) {
	// A rollout was reported once its grace period passed, then reported an
	// error which cleared within the debounce, like a Deployment past its
	// progress deadline which a new rollout restarted.
	f := newFixture(t, nil)
	progressing := notReadySummary(1, notReadyDeployment("foo", "0/1"))
	failed := notReadySummary(1, erroring(notReadyDeployment("foo", "0/1")))

	f.emitter.ObserveBundle(context.TODO(), bundle(deploying()), bundle(progressing), profileOf(progressing))
	f.flushGrace(t)
	f.emitter.ObserveBundle(context.TODO(), bundle(progressing), bundle(failed), profileOf(failed))
	f.clock.advance(time.Second)
	f.emitter.ObserveBundle(context.TODO(), bundle(failed), bundle(progressing), profileOf(progressing))
	f.clock.advance(f.opts.MinInterval)
	f.flush(t)

	if got := reasons(f.events(t)); len(got) != 1 {
		t.Fatalf("expected only the rollout, got %v", got)
	}
}

func TestFailureWhichEndsBackInTheOneBeingCreatedIsNotReported(t *testing.T) {
	f := newFixture(t, nil)
	a := failing(500, renderError)
	b := failing(500, nsForbidden)

	f.emitter.ObserveBundle(context.TODO(), bundle(ready()), bundle(a), profileOf(a))
	f.whileCreating = func() {
		f.emitter.ObserveBundle(context.TODO(), bundle(a), bundle(b), profileOf(b))
		f.emitter.ObserveBundle(context.TODO(), bundle(b), bundle(a), profileOf(a))
	}
	f.flush(t)
	f.clock.advance(f.opts.MinInterval)
	f.flush(t)

	if got := reasons(f.events(t)); len(got) != 1 {
		t.Fatalf("expected only the failure being created, got %v", got)
	}
}

func TestBundleDeploymentErrorWhichClearsBeforeItIsDueIsNotReported(t *testing.T) {
	f := newFixture(t, nil)
	f.opts.PerDeployment = true

	progressing := deployment(fleet.NotReady)
	progressing.Status.NonReadyStatus = []fleet.NonReadyStatus{notReadyDeployment("foo", "0/1")}
	failed := deployment(fleet.NotReady)
	failed.Status.NonReadyStatus = []fleet.NonReadyStatus{erroring(notReadyDeployment("foo", "0/1"))}

	f.emitter.ObserveBundleDeployment(context.TODO(), withDisplayState(progressing, fleet.WaitApplied), progressing)
	f.flushGrace(t)
	f.emitter.ObserveBundleDeployment(context.TODO(), failed, failed)
	f.clock.advance(time.Second)
	f.emitter.ObserveBundleDeployment(context.TODO(), progressing, progressing)
	f.clock.advance(f.opts.MinInterval)
	f.flush(t)

	if got := reasons(f.events(t)); len(got) != 1 {
		t.Fatalf("expected only the rollout, got %v", got)
	}
}
