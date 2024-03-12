package imagescan

import (
	"testing"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

func TestLatestTag(t *testing.T) {
	var alphabeticalVersions = []string{"a", "b", "c"}

	tests := []struct {
		name, want string
		policy     fleet.ImagePolicyChoice
	}{
		{
			name: "alphabetical asc lowercase",
			policy: fleet.ImagePolicyChoice{
				Alphabetical: &fleet.AlphabeticalPolicy{Order: "asc"},
			},
			want: "a",
		},
		{
			name: "alphabetical asc uppercase",
			policy: fleet.ImagePolicyChoice{
				Alphabetical: &fleet.AlphabeticalPolicy{Order: "ASC"},
			},
			want: "a",
		},
		{
			name: "alphabetical desc lowercase",
			policy: fleet.ImagePolicyChoice{
				Alphabetical: &fleet.AlphabeticalPolicy{Order: "desc"},
			},
			want: "c",
		},
		{
			name: "alphabetical desc uppercase",
			policy: fleet.ImagePolicyChoice{
				Alphabetical: &fleet.AlphabeticalPolicy{Order: "DESC"},
			},
			want: "c",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := latestTag(tt.policy, alphabeticalVersions)
			if err != nil {
				t.Fatalf("Error calling latestTag: %v", err)
			}

			if got != tt.want {
				t.Errorf("latestTag() = %v, want %v", got, tt.want)
			}
		})
	}
}
