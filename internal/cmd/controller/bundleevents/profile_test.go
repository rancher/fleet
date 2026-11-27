package bundleevents

import (
	"context"
	"testing"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

func TestProfileCountsFailingDeploymentsByKind(t *testing.T) {
	failed := notReadyDeployment("web", "0/3")
	failed.Summary.Error = true

	var p Profile
	p.Add("fleet-default/a", fleet.ErrApplied, renderError, nil)
	p.Add("fleet-default/b", fleet.NotReady, "", []fleet.NonReadyStatus{notReadyDeployment("web", "1/3")})
	p.Add("fleet-default/c", fleet.NotReady, "", []fleet.NonReadyStatus{notReadyDeployment("web", "2/3")})
	p.Add("fleet-default/d", fleet.NotReady, "", []fleet.NonReadyStatus{failed})
	p.Add("fleet-default/e", fleet.NotReady, "Monitored: job failed", nil)
	for _, state := range []fleet.BundleState{fleet.Ready, fleet.Pending, fleet.WaitApplied, fleet.Modified, fleet.OutOfSync, fleet.WaitingForDependency} {
		p.Add("fleet-default/f", state, "", nil)
	}

	if p.ErrApplied != 1 || p.NotReadyWithError != 2 || p.NotReadyProgressing != 2 {
		t.Fatalf("expected 1 errApplied, 2 not ready with an error and 2 progressing, got %+v", p)
	}
	// Both progressing deployments share a cause, however far along they are.
	if got := len(p.causeKeys()); got != 4 {
		t.Errorf("expected 4 causes, got %v", p.causeKeys())
	}
}

func TestProfileGroupsDeploymentsByCause(t *testing.T) {
	var p Profile
	p.Add("fleet-default/a", fleet.ErrApplied, nsForbidden, nil)
	p.Add("fleet-default/b", fleet.ErrApplied, renderError, nil)
	p.Add("fleet-default/c", fleet.ErrApplied, renderError, nil)

	causes := p.causeList()
	if len(causes) != 2 {
		t.Fatalf("expected 2 causes, got %d", len(causes))
	}
	if c := causes[0]; c.message != renderError || c.count != 2 || c.example != "fleet-default/b" {
		t.Errorf("expected the most common cause first, with its first cluster, got %+v", *c)
	}
}

func TestProfileProgressing(t *testing.T) {
	cases := []struct {
		name    string
		profile Profile
		want    bool
	}{
		{"only progressing", Profile{NotReadyProgressing: 37}, true},
		{"one with an error", Profile{NotReadyProgressing: 36, NotReadyWithError: 1}, false},
		{"one failed to apply", Profile{NotReadyProgressing: 36, ErrApplied: 1}, false},
		{"nothing failing", Profile{}, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.profile.progressing(); got != c.want {
				t.Errorf("expected %t, got %t", c.want, got)
			}
		})
	}
}

func TestCauseMessagePrefersAResourceReportingAnError(t *testing.T) {
	failed := notReadyDeployment("db", "0/1")
	failed.Summary.Error = true

	got := causeMessage("", []fleet.NonReadyStatus{notReadyDeployment("web", "1/3"), failed})

	if want := failed.String() + " (+1 more resources)"; got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestProfileTravelsInTheContext(t *testing.T) {
	if ProfileFrom(context.TODO()) != nil {
		t.Error("expected no profile in a plain context")
	}

	p := &Profile{NotReadyProgressing: 1}
	if got := ProfileFrom(WithProfile(context.TODO(), p)); got != p {
		t.Errorf("expected the profile stored in the context, got %v", got)
	}
}
