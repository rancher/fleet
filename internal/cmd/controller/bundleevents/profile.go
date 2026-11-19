package bundleevents

import (
	"context"
	"fmt"
	"sort"
	"strings"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

// Profile describes the failing deployments of a bundle, counted over all of
// them. Everything events say about a bundle's failures comes from it, not from
// the bundle's summary: the summary lists at most 10 deployments, in any state
// which is not ready, in the order of their clusters' names. That is a sample,
// which says nothing reliable about the others: during a rollout to many
// clusters, the ones it lists are often still waiting to be applied, and the
// failing ones are left out.
type Profile struct {
	// ErrApplied counts deployments which failed to apply.
	ErrApplied int

	// NotReadyWithError counts deployments which applied but are not
	// ready, with a failed condition or a resource Kubernetes considers
	// failed, see progressingCause.
	NotReadyWithError int

	// NotReadyProgressing counts deployments which applied but are not
	// ready yet, with nothing reporting an error.
	NotReadyProgressing int

	// causes groups the failing deployments by why they are failing.
	causes map[string]*cause
}

// cause is one reason deployments of a bundle are failing, with how many are
// failing for it and the first of them, in the order they were added.
type cause struct {
	key     string
	state   fleet.BundleState
	message string
	example string
	count   int
}

// Add counts one bundle deployment, from the name of its cluster, its state,
// the message of its failed conditions, and the resources it reports as not
// ready. Deployments in any other state than a failure are not counted.
func (p *Profile) Add(cluster string, state fleet.BundleState, message string, nonReady []fleet.NonReadyStatus) {
	switch state {
	case fleet.ErrApplied:
		p.ErrApplied++
	case fleet.NotReady:
		if progressingCause(message, nonReady) {
			p.NotReadyProgressing++
		} else {
			p.NotReadyWithError++
		}
	default:
		return
	}

	key := causeKey(state, message, nonReady)
	if p.causes == nil {
		p.causes = map[string]*cause{}
	}
	if c, ok := p.causes[key]; ok {
		c.count++
		return
	}
	p.causes[key] = &cause{
		key:     key,
		state:   state,
		message: causeMessage(message, nonReady),
		example: cluster,
		count:   1,
	}
}

// progressing reports whether every failing deployment is merely progressing.
func (p *Profile) progressing() bool {
	return p.ErrApplied == 0 && p.NotReadyWithError == 0 && p.NotReadyProgressing > 0
}

// causeList returns the causes, the most common first, and in the order of
// their keys among equally common ones, so that the same failures always list
// the same way.
func (p *Profile) causeList() []*cause {
	causes := make([]*cause, 0, len(p.causes))
	for _, c := range p.causes {
		causes = append(causes, c)
	}
	sort.Slice(causes, func(i, j int) bool {
		if causes[i].count != causes[j].count {
			return causes[i].count > causes[j].count
		}
		return causes[i].key < causes[j].key
	})

	return causes
}

// causeKeys returns the keys of the causes, sorted, to tell whether the
// failures of a bundle have changed. How many deployments fail for each cause
// is deliberately left out: the same failure on more clusters is not a new
// cause, and how many deployments are failing is accounted for separately, see
// magnitude.
func (p *Profile) causeKeys() []string {
	keys := make([]string, 0, len(p.causes))
	for key := range p.causes {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	return keys
}

// causeKey identifies why a deployment is failing. A deployment which failed to
// apply, or has a failed condition, is identified by its message. One which is
// not ready is identified by which resources report an error, if any: which
// resources are merely not ready yet, and their messages, change as a rollout
// advances, like "Available: 1/3", and would make a rollout look like a new
// failure every time.
func causeKey(state fleet.BundleState, message string, nonReady []fleet.NonReadyStatus) string {
	if message != "" {
		return string(state) + "|" + message
	}

	var failed []string
	for _, r := range nonReady {
		if r.Summary.Error {
			failed = append(failed, fmt.Sprintf("%s/%s/%s/%s", r.APIVersion, r.Kind, r.Namespace, r.Name))
		}
	}
	if len(failed) == 0 {
		return string(state) + "|progressing"
	}
	sort.Strings(failed)

	return string(state) + "|failed:" + strings.Join(failed, ",")
}

// causeMessage describes why a deployment is failing, in at most
// messageMaxLength bytes plus a count of the resources left out.
//
// A deployment which failed to apply carries the error in its message. One
// which applied but is not ready has no message, since applying succeeded: the
// reason is in the resources the agent reported as not ready. A resource which
// reports an error describes it better than one which is merely not ready yet,
// so it is described first. The agent sorts the resources, so the one described
// is stable between updates.
func causeMessage(message string, nonReady []fleet.NonReadyStatus) string {
	if message != "" || len(nonReady) == 0 {
		return truncate(message, messageMaxLength)
	}

	first := nonReady[0]
	for _, r := range nonReady {
		if r.Summary.Error {
			first = r
			break
		}
	}

	described := truncate(first.String(), messageMaxLength)
	if more := len(nonReady) - 1; more > 0 {
		described += fmt.Sprintf(" (+%d more resources)", more)
	}

	return described
}

// profileKey is the context key of a Profile.
type profileKey struct{}

// WithProfile returns a context carrying the profile of the bundle being
// reconciled, so that whichever path persists the summary computed with it
// reports it too.
func WithProfile(ctx context.Context, p *Profile) context.Context {
	return context.WithValue(ctx, profileKey{}, p)
}

// ProfileFrom returns the profile carried by the context, or nil.
func ProfileFrom(ctx context.Context) *Profile {
	p, _ := ctx.Value(profileKey{}).(*Profile)

	return p
}
