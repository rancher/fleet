package bundleevents

import (
	"fmt"
	"sort"
	"strings"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

const (
	// noteMaxLength stays below the 1kB limit the API enforces on an event's note.
	noteMaxLength = 1000

	// messageMaxLength bounds a single cause, so that one long Helm error
	// cannot take up the whole note.
	messageMaxLength = 200

	// statusHint points at the bundle status, which lists more causes than
	// fit into a note.
	statusHint = "see status.summary.nonReadyResources"
)

// failureCounts returns the number of bundle deployments which failed to deploy
// or are deployed but not ready, together with the state to report for them.
// Transient states, like Pending or WaitApplied, are not failures: they are
// reported once they either settle or turn into a failure.
func failureCounts(s fleet.BundleSummary) (int, fleet.BundleState) {
	switch {
	case s.ErrApplied > 0:
		// ErrApplied outranks NotReady, see fleet.StateRank.
		return s.ErrApplied + s.NotReady, fleet.ErrApplied
	case s.NotReady > 0:
		return s.NotReady, fleet.NotReady
	}

	return 0, fleet.Ready
}

// failureReason is the reason to report for a failure state.
func failureReason(state fleet.BundleState) string {
	if state == fleet.NotReady {
		return ReasonNotReady
	}

	return ReasonDeployFailed
}

// failureFingerprint describes the failures of a summary: how many deployments
// are affected, the reason to report them under, and a fingerprint which
// changes whenever the failures are worth reporting again.
func failureFingerprint(s fleet.BundleSummary) (int, string, fingerprint) {
	failing, state := failureCounts(s)
	if failing == 0 {
		return 0, "", fingerprint{}
	}

	reason := failureReason(state)

	return failing, reason, fingerprintOf("failing", reason, magnitude(failing), causeKeys(s))
}

// failureCauses returns the failing entries of the summary's non-ready
// resources. The summary stores at most 10 of them.
func failureCauses(s fleet.BundleSummary) []fleet.NonReadyResource {
	causes := make([]fleet.NonReadyResource, 0, len(s.NonReadyResources))
	for _, r := range s.NonReadyResources {
		if r.State == fleet.ErrApplied || r.State == fleet.NotReady {
			causes = append(causes, r)
		}
	}

	return causes
}

// causeKeys returns the distinct state and message pairs of the failing
// resources, sorted, so that the same set of causes always produces the same
// keys. Cluster names are deliberately left out: the same failure on more
// clusters is not a new cause and must not produce another event.
func causeKeys(s fleet.BundleSummary) []string {
	seen := make(map[string]struct{})
	keys := make([]string, 0, len(s.NonReadyResources))

	for _, c := range failureCauses(s) {
		key := string(c.State) + "|" + c.Message
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	sort.Strings(keys)

	return keys
}

// magnitude buckets a number of failures, so that a deployment failing on one
// more cluster does not warrant another event, while an order of magnitude
// more, or fewer, failures does.
func magnitude(n int) string {
	switch {
	case n <= 1:
		return "1"
	case n <= 5:
		return "2-5"
	case n <= 20:
		return "6-20"
	case n <= 100:
		return "21-100"
	case n <= 1000:
		return "101-1000"
	}

	return "1000+"
}

// failureNote describes how many bundle deployments are failing and why, for up
// to maxCauses distinct clusters. Causes which do not fit are represented by a
// count and a pointer to the bundle status, which holds more of them.
func failureNote(s fleet.BundleSummary, maxCauses int) string {
	failing, _ := failureCounts(s)

	var b strings.Builder
	fmt.Fprintf(&b, "%d/%d bundle deployments failing", failing, s.DesiredReady)
	if s.ErrApplied > 0 && s.NotReady > 0 {
		fmt.Fprintf(&b, " (errApplied %d, notReady %d)", s.ErrApplied, s.NotReady)
	}
	b.WriteString(".")

	// Leave room for the trailing hint, which is more useful than one more cause.
	budget := noteMaxLength - len(statusHint) - 32

	causes := failureCauses(s)
	written := 0
	for _, c := range causes {
		if written >= maxCauses {
			break
		}

		cause := fmt.Sprintf(" %s: %s: %s;", c.Name, c.State, truncate(c.Message, messageMaxLength))
		if b.Len()+len(cause) > budget {
			break
		}

		b.WriteString(cause)
		written++
	}

	// The hint counts the causes left in the status, not the failing
	// deployments: the summary records at most 10 causes for any number of
	// them, so counting deployments would point at causes which are not
	// there. How many deployments are failing is already in the first
	// sentence.
	if remaining := len(causes) - written; remaining > 0 {
		fmt.Fprintf(&b, " (+%d more, %s)", remaining, statusHint)
	}

	return truncate(b.String(), noteMaxLength)
}

// readyNote describes a bundle whose deployments are all ready again.
func readyNote(s fleet.BundleSummary) string {
	return fmt.Sprintf("%d/%d bundle deployments ready", s.Ready, s.DesiredReady)
}

// deploymentNote describes the state of a single bundle deployment.
func deploymentNote(bundle string, state fleet.BundleState, message string) string {
	if bundle == "" {
		bundle = "unknown"
	}
	if message == "" {
		return fmt.Sprintf("bundle %s: %s", bundle, state)
	}

	return truncate(fmt.Sprintf("bundle %s: %s: %s", bundle, state, message), noteMaxLength)
}

// truncate shortens s to max bytes, without cutting a rune in half.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return strings.ToValidUTF8(s[:max], "")
	}

	return strings.ToValidUTF8(s[:max-3], "") + "..."
}
