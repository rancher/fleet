package bundleevents

import (
	"strings"
	"testing"
	"unicode/utf8"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	fleetsummary "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1/summary"

	"k8s.io/apimachinery/pkg/types"
)

// notReadyDeployment is a resource the agent reports as not ready, the way it
// reports a Deployment whose pods are not available.
func notReadyDeployment(name, available string) fleet.NonReadyStatus {
	return fleet.NonReadyStatus{
		UID:        types.UID("uid-" + name),
		APIVersion: "apps/v1",
		Kind:       "Deployment",
		Namespace:  "app",
		Name:       name,
		Summary: fleetsummary.Summary{
			State:         "updating",
			Transitioning: true,
			Message:       []string{"Deployment does not have minimum availability.", "Available: " + available},
		},
	}
}

// notReadySummary is a bundle summary with one deployment per cluster which
// applied but is not ready. As the agent reports it, the deployment's message
// is empty: applying succeeded, and the reason is in its non-ready resources.
func notReadySummary(clusters int, resources ...fleet.NonReadyStatus) fleet.BundleSummary {
	s := fleet.BundleSummary{DesiredReady: clusters, NotReady: clusters}
	for i := range clusters {
		s.NonReadyResources = append(s.NonReadyResources, fleet.NonReadyResource{
			Name:           "fleet-default/cluster-" + string(rune('a'+i)),
			State:          fleet.NotReady,
			NonReadyStatus: resources,
		})
	}

	return s
}

func TestFailureNoteDescribesCountsAndCauses(t *testing.T) {
	summary := fleet.BundleSummary{
		DesiredReady: 500,
		Ready:        200,
		ErrApplied:   290,
		NotReady:     10,
		NonReadyResources: []fleet.NonReadyResource{
			{Name: "fleet-default/cluster-a", State: fleet.ErrApplied, Message: renderError},
			{Name: "fleet-default/cluster-b", State: fleet.ErrApplied, Message: nsForbidden},
			{Name: "fleet-default/cluster-c", State: fleet.NotReady, Message: "0/3 replicas ready"},
			{Name: "fleet-default/cluster-d", State: fleet.ErrApplied, Message: renderError},
		},
	}

	note := failureNote(summary, profileOf(summary), 2)

	if !strings.HasPrefix(note, "300/500 bundle deployments failing (errApplied 290, notReady 10).") {
		t.Errorf("unexpected counts: %q", note)
	}
	// The most common cause comes first, described once for every
	// deployment failing for it.
	for _, want := range []string{
		" 2 clusters, e.g. fleet-default/cluster-a: ErrApplied: " + renderError + ";",
		" fleet-default/cluster-b: ErrApplied: " + nsForbidden + ";",
	} {
		if !strings.Contains(note, want) {
			t.Errorf("expected %q in the note, got %q", want, note)
		}
	}
	if strings.Contains(note, "cluster-c") || strings.Contains(note, "cluster-d") {
		t.Errorf("expected at most 2 causes, got %q", note)
	}
	// Three causes, two fit.
	if !strings.HasSuffix(note, " (+1 more causes)") {
		t.Errorf("expected the remaining causes to be counted, got %q", note)
	}
}

func TestFailureNoteStaysWithinTheNoteLimit(t *testing.T) {
	summary := fleet.BundleSummary{DesiredReady: 10, ErrApplied: 10}
	for i := range 10 {
		summary.NonReadyResources = append(summary.NonReadyResources, fleet.NonReadyResource{
			Name:    "fleet-default/cluster-" + strings.Repeat("x", 50),
			State:   fleet.ErrApplied,
			Message: string(rune('a'+i)) + strings.Repeat(" a very long helm error", 100),
		})
	}

	note := failureNote(summary, profileOf(summary), 10)

	if len(note) > noteMaxLength {
		t.Errorf("expected the note to be at most %d bytes, got %d", noteMaxLength, len(note))
	}
	if !strings.HasSuffix(note, "more causes)") {
		t.Errorf("expected the note to end with a complete count of the causes left out, got %q", note)
	}
}

func TestFailureNoteWithoutCausesStillReportsCounts(t *testing.T) {
	note := failureNote(fleet.BundleSummary{DesiredReady: 3, ErrApplied: 3}, &Profile{}, 3)

	// No causes are known, so there is nothing to count either.
	if note != "3/3 bundle deployments failing." {
		t.Errorf("unexpected note: %q", note)
	}
}

func TestFailureNoteOmitsTheHintOnceEveryCauseFits(t *testing.T) {
	// Many deployments failing for one reason: the note describes it in
	// full, so there is nothing more to count.
	summary := fleet.BundleSummary{
		DesiredReady: 500,
		Ready:        450,
		ErrApplied:   50,
		NonReadyResources: []fleet.NonReadyResource{
			{Name: "fleet-default/cluster-a", State: fleet.ErrApplied, Message: renderError},
		},
	}

	note := failureNote(summary, profileOf(summary), 3)

	if !strings.HasPrefix(note, "50/500 bundle deployments failing.") {
		t.Errorf("unexpected counts: %q", note)
	}
	if strings.Contains(note, "more causes") {
		t.Errorf("expected no count of causes left out, got %q", note)
	}
}

func TestFailureCountsIgnoreTransientStates(t *testing.T) {
	for _, test := range []struct {
		name    string
		summary fleet.BundleSummary
		failing int
		state   fleet.BundleState
	}{
		{
			name:    "pending and waiting are not failures",
			summary: fleet.BundleSummary{DesiredReady: 5, Pending: 2, WaitApplied: 2, OutOfSync: 1},
			failing: 0,
			state:   fleet.Ready,
		},
		{
			name:    "errApplied outranks notReady",
			summary: fleet.BundleSummary{DesiredReady: 5, ErrApplied: 2, NotReady: 3},
			failing: 5,
			state:   fleet.ErrApplied,
		},
		{
			name:    "notReady on its own",
			summary: fleet.BundleSummary{DesiredReady: 5, NotReady: 3},
			failing: 3,
			state:   fleet.NotReady,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			failing, state := failureCounts(test.summary)
			if failing != test.failing || state != test.state {
				t.Errorf("expected %d/%s, got %d/%s", test.failing, test.state, failing, state)
			}
		})
	}
}

func TestCauseKeysIgnoreClusterNames(t *testing.T) {
	first := fleet.BundleSummary{NonReadyResources: []fleet.NonReadyResource{
		{Name: "fleet-default/cluster-a", State: fleet.ErrApplied, Message: renderError},
	}}
	second := fleet.BundleSummary{NonReadyResources: []fleet.NonReadyResource{
		{Name: "fleet-default/cluster-b", State: fleet.ErrApplied, Message: renderError},
		{Name: "fleet-default/cluster-c", State: fleet.ErrApplied, Message: renderError},
	}}

	if got, want := profileOf(second).causeKeys(), profileOf(first).causeKeys(); len(got) != len(want) || got[0] != want[0] {
		t.Errorf("expected the same cause on more clusters to produce the same keys, got %v and %v", got, want)
	}
}

func TestMagnitudeBuckets(t *testing.T) {
	for _, test := range []struct {
		n    int
		want string
	}{
		{n: 1, want: "1"},
		{n: 2, want: "2-5"},
		{n: 5, want: "2-5"},
		{n: 6, want: "6-20"},
		{n: 100, want: "21-100"},
		{n: 500, want: "101-1000"},
		{n: 5000, want: "1000+"},
	} {
		if got := magnitude(test.n); got != test.want {
			t.Errorf("magnitude(%d) = %q, want %q", test.n, got, test.want)
		}
	}
}

func TestTruncateKeepsValidUTF8(t *testing.T) {
	// "ü" takes two bytes, so cutting to 10 bytes, of which 7 are left once
	// the ellipsis is accounted for, would split the fourth one in half.
	truncated := truncate(strings.Repeat("ü", 20), 10)

	if len(truncated) > 10 {
		t.Errorf("expected at most 10 bytes, got %d", len(truncated))
	}
	if !strings.HasSuffix(truncated, ellipsis) {
		t.Errorf("expected an ellipsis, got %q", truncated)
	}
	if !utf8.ValidString(truncated) {
		t.Errorf("expected valid UTF-8, got %q", truncated)
	}
}

func TestFailureNoteDescribesNotReadyFromItsResources(t *testing.T) {
	note := failureNote(notReadySummary(1, notReadyDeployment("foo", "0/1")), profileOf(notReadySummary(1, notReadyDeployment("foo", "0/1"))), 3)

	want := "1/1 bundle deployments failing. fleet-default/cluster-a: NotReady: " +
		"deployment.apps app/foo [progressing] Deployment does not have minimum availability., Available: 0/1;"
	if note != want {
		t.Errorf("unexpected note:\n got %q\nwant %q", note, want)
	}
}

func TestFailureNoteCountsTheNotReadyResourcesLeftOut(t *testing.T) {
	note := failureNote(notReadySummary(1,
		notReadyDeployment("foo", "0/1"),
		notReadyDeployment("bar", "0/1"),
		notReadyDeployment("baz", "0/1"),
	), profileOf(notReadySummary(1,
		notReadyDeployment("foo", "0/1"),
		notReadyDeployment("bar", "0/1"),
		notReadyDeployment("baz", "0/1"),
	)),

		3)

	if !strings.Contains(note, "app/foo [progressing]") {
		t.Errorf("expected the first resource in the note, got %q", note)
	}
	if strings.Contains(note, "app/bar") || !strings.Contains(note, "Available: 0/1 (+2 more resources);") {
		t.Errorf("expected the other resources to be counted, got %q", note)
	}
}

func TestFailureNoteBoundsALongNotReadyCause(t *testing.T) {
	long := notReadyDeployment("foo", strings.Repeat("0/1 ", 100))
	note := failureNote(notReadySummary(1, long, notReadyDeployment("bar", "0/1")), profileOf(notReadySummary(1, long, notReadyDeployment("bar", "0/1"))), 3)

	cause := note[strings.Index(note, "NotReady: ")+len("NotReady: "):]
	if len(cause) > messageMaxLength+len(" (+1 more resources);") {
		t.Errorf("expected the cause to be bounded, got %d bytes: %q", len(cause), cause)
	}
	if !strings.HasSuffix(note, ellipsis+" (+1 more resources);") {
		t.Errorf("expected the count to survive truncation, got %q", note)
	}
}

func TestFailureNoteDescribesTheCauseOfManyNotReadyClustersOnce(t *testing.T) {
	// QA step 9 of issue 4455: four clusters, not ready for the same
	// reason, which is described.
	s := notReadySummary(4, notReadyDeployment("foo", "0/1"))
	note := failureNote(s, profileOf(s), 3)

	want := "4/4 bundle deployments failing. 4 clusters, e.g. fleet-default/cluster-a: NotReady: deployment.apps app/foo"
	if !strings.HasPrefix(note, want) {
		t.Errorf("expected the cause described once for all clusters, got %q", note)
	}
	if strings.Contains(note, "more causes") {
		t.Errorf("expected no cause left out, got %q", note)
	}
}

func TestFailureNoteWithoutAReasonLeavesNoEmptyCause(t *testing.T) {
	note := failureNote(notReadySummary(1), profileOf(notReadySummary(1)), 3)

	if note != "1/1 bundle deployments failing. fleet-default/cluster-a: NotReady;" {
		t.Errorf("unexpected note: %q", note)
	}
}

func TestCauseKeysOfNotReady(t *testing.T) {
	starting := profileOf(notReadySummary(1, notReadyDeployment("foo", "0/3"))).causeKeys()

	for _, test := range []struct {
		name    string
		summary fleet.BundleSummary
		same    bool
	}{
		{
			name:    "progress of the same resource is the same cause",
			summary: notReadySummary(1, notReadyDeployment("foo", "1/3")),
			same:    true,
		},
		{
			name:    "the same resource on more clusters is the same cause",
			summary: notReadySummary(3, notReadyDeployment("foo", "0/3")),
			same:    true,
		},
		{
			// Finding 6: which resources are merely not ready yet
			// changes as a rollout advances.
			name:    "another resource not ready yet is the same cause",
			summary: notReadySummary(1, notReadyDeployment("foo", "0/3"), notReadyDeployment("bar", "0/1")),
			same:    true,
		},
		{
			name: "another resource reporting an error is a new cause",
			summary: func() fleet.BundleSummary {
				r := notReadyDeployment("bar", "0/1")
				r.Summary.Error = true
				return notReadySummary(1, notReadyDeployment("foo", "0/3"), r)
			}(),
		},
		{
			name: "a resource turning into an error is a new cause",
			summary: func() fleet.BundleSummary {
				r := notReadyDeployment("foo", "0/3")
				r.Summary.Error = true
				return notReadySummary(1, r)
			}(),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := profileOf(test.summary).causeKeys()
			if same := strings.Join(got, "\n") == strings.Join(starting, "\n"); same != test.same {
				t.Errorf("expected same=%t, got keys %v and %v", test.same, got, starting)
			}
		})
	}
}
