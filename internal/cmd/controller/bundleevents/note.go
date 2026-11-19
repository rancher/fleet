package bundleevents

import (
	"fmt"
	"sort"
	"strings"

	"github.com/rancher/fleet/internal/cmd/controller/summary"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

const (
	// noteMaxLength stays below the 1kB limit the API enforces on an event's note.
	noteMaxLength = 1000

	// messageMaxLength bounds a single cause, so that one long Helm error
	// cannot take up the whole note.
	messageMaxLength = 200

	// moreCausesFormat ends a note whose causes do not all fit, with how many
	// were left out.
	moreCausesFormat = " (+%d more causes)"

	// ellipsis marks a truncated text.
	ellipsis = "..."
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

// isFailure reports whether a state is one this package reports as a failure.
func isFailure(state fleet.BundleState) bool {
	return state == fleet.ErrApplied || state == fleet.NotReady
}

// progressingCause reports whether a deployment which is not ready is merely
// progressing. A message comes from a failed condition of the deployment, and
// an error from a resource which Kubernetes considers failed, like a Deployment
// past its progress deadline. A deployment without either is still on its way.
func progressingCause(message string, resources []fleet.NonReadyStatus) bool {
	if message != "" {
		return false
	}

	for _, r := range resources {
		if r.Summary.Error {
			return false
		}
	}

	return true
}

// recovered reports whether every bundle deployment of a bundle is ready. A
// bundle without targets has no deployments to report as recovered.
func recovered(s fleet.BundleSummary) bool {
	return s.DesiredReady > 0 && summary.IsReady(s)
}

// bundleFailureReason is the reason to report for a bundle in a failure state.
func bundleFailureReason(state fleet.BundleState) string {
	if state == fleet.NotReady {
		return ReasonBundleNotReady
	}

	return ReasonBundleDeployFailed
}

// deploymentFailureReason is the reason to report for a single bundle
// deployment in a failure state.
func deploymentFailureReason(state fleet.BundleState) string {
	if state == fleet.NotReady {
		return ReasonBundleDeploymentNotReady
	}

	return ReasonBundleDeploymentFailed
}

// failureFingerprint describes the failures of a bundle: how many deployments
// are affected, the reason to report them under, and a fingerprint which
// changes whenever the failures are worth reporting again. How many
// deployments are failing comes from the summary's counters, which count every
// deployment, and why from the profile.
func failureFingerprint(s fleet.BundleSummary, p *Profile) (int, string, fingerprint) {
	failing, state := failureCounts(s)
	if failing == 0 {
		return 0, "", fingerprint{}
	}

	reason := bundleFailureReason(state)

	return failing, reason, fingerprintOf("failing", reason, magnitude(failing), p.causeKeys())
}

// persistedFailureFingerprint describes the failures of a bundle as far as its
// persisted status tells: the reason, how many deployments are failing, and the
// causes of the deployments the summary lists, which are a sample of at most 10.
// It is only compared with itself, to tell whether a bundle's failures changed
// between two persisted statuses, when the emitter has no memory of the bundle.
func persistedFailureFingerprint(s fleet.BundleSummary) fingerprint {
	failing, state := failureCounts(s)
	if failing == 0 {
		return fingerprint{}
	}

	seen := map[string]struct{}{}
	var keys []string
	for _, r := range s.NonReadyResources {
		if !isFailure(r.State) {
			continue
		}
		key := causeKey(r.State, r.Message, r.NonReadyStatus)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	sort.Strings(keys)

	return fingerprintOf("persisted", bundleFailureReason(state), magnitude(failing), keys)
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
// to maxCauses causes, the most common first. A cause shared by several
// deployments is described once, with how many share it and the cluster of the
// first of them.
func failureNote(s fleet.BundleSummary, p *Profile, maxCauses int) string {
	failing, _ := failureCounts(s)

	var b strings.Builder
	fmt.Fprintf(&b, "%d/%d bundle deployments failing", failing, s.DesiredReady)
	if s.ErrApplied > 0 && s.NotReady > 0 {
		fmt.Fprintf(&b, " (errApplied %d, notReady %d)", s.ErrApplied, s.NotReady)
	}
	b.WriteString(".")

	causes := p.causeList()

	// Leave room for the ending, which is more useful than one more cause. No
	// more causes can be left out than there are, so this fits any count.
	budget := noteMaxLength - len(fmt.Sprintf(moreCausesFormat, len(causes)))
	written := 0
	for _, c := range causes {
		if written >= maxCauses {
			break
		}

		cause := describeCause(c)
		if b.Len()+len(cause) > budget {
			break
		}

		b.WriteString(cause)
		written++
	}

	if remaining := len(causes) - written; remaining > 0 {
		fmt.Fprintf(&b, moreCausesFormat, remaining)
	}

	return truncate(b.String(), noteMaxLength)
}

// describeCause is how a cause reads in a note.
func describeCause(c *cause) string {
	where := c.example
	if c.count > 1 {
		where = fmt.Sprintf("%d clusters, e.g. %s", c.count, c.example)
	}
	if c.message == "" {
		return fmt.Sprintf(" %s: %s;", where, c.state)
	}

	return fmt.Sprintf(" %s: %s: %s;", where, c.state, c.message)
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
	if max <= len(ellipsis) {
		return strings.ToValidUTF8(s[:max], "")
	}

	return strings.ToValidUTF8(s[:max-len(ellipsis)], "") + ellipsis
}
