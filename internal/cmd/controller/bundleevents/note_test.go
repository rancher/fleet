package bundleevents

import (
	"strings"
	"testing"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

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

	note := failureNote(summary, 3)

	if !strings.HasPrefix(note, "300/500 bundle deployments failing (errApplied 290, notReady 10).") {
		t.Errorf("unexpected counts: %q", note)
	}
	for _, want := range []string{"fleet-default/cluster-a", "fleet-default/cluster-b", "fleet-default/cluster-c"} {
		if !strings.Contains(note, want) {
			t.Errorf("expected %q in the note, got %q", want, note)
		}
	}
	if strings.Contains(note, "cluster-d") {
		t.Errorf("expected at most 3 causes, got %q", note)
	}
	// One cause is left in the status: four are recorded, three fit.
	if !strings.Contains(note, "(+1 more, see status.summary.nonReadyResources)") {
		t.Errorf("expected the remaining causes to be counted, got %q", note)
	}
}

func TestFailureNoteStaysWithinTheNoteLimit(t *testing.T) {
	summary := fleet.BundleSummary{DesiredReady: 10, ErrApplied: 10}
	for range 10 {
		summary.NonReadyResources = append(summary.NonReadyResources, fleet.NonReadyResource{
			Name:    "fleet-default/cluster-" + strings.Repeat("x", 50),
			State:   fleet.ErrApplied,
			Message: strings.Repeat("a very long helm error ", 100),
		})
	}

	note := failureNote(summary, 10)

	if len(note) > noteMaxLength {
		t.Errorf("expected the note to be at most %d bytes, got %d", noteMaxLength, len(note))
	}
	if !strings.Contains(note, "more, see status.summary.nonReadyResources") {
		t.Errorf("expected a pointer to the status, got %q", note)
	}
}

func TestFailureNoteWithoutCausesStillReportsCounts(t *testing.T) {
	note := failureNote(fleet.BundleSummary{DesiredReady: 3, ErrApplied: 3}, 3)

	// No causes are recorded, so there is nothing to point at in the status.
	if note != "3/3 bundle deployments failing." {
		t.Errorf("unexpected note: %q", note)
	}
}

func TestFailureNoteOmitsTheHintOnceEveryCauseFits(t *testing.T) {
	// Many deployments failing for the one reason the summary recorded: the
	// note describes it in full, so there is nothing more to look up.
	summary := fleet.BundleSummary{
		DesiredReady: 500,
		Ready:        450,
		ErrApplied:   50,
		NonReadyResources: []fleet.NonReadyResource{
			{Name: "fleet-default/cluster-a", State: fleet.ErrApplied, Message: renderError},
		},
	}

	note := failureNote(summary, 3)

	if !strings.HasPrefix(note, "50/500 bundle deployments failing.") {
		t.Errorf("unexpected counts: %q", note)
	}
	if strings.Contains(note, "more, "+statusHint) {
		t.Errorf("expected no pointer to the status, got %q", note)
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

	if got, want := causeKeys(second), causeKeys(first); len(got) != len(want) || got[0] != want[0] {
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
	truncated := truncate(strings.Repeat("ü", 20), 10)

	if len(truncated) > 10 {
		t.Errorf("expected at most 10 bytes, got %d", len(truncated))
	}
	if !strings.HasSuffix(truncated, "...") {
		t.Errorf("expected an ellipsis, got %q", truncated)
	}
	for _, r := range truncated {
		if r == '�' {
			t.Errorf("expected valid UTF-8, got %q", truncated)
		}
	}
}
