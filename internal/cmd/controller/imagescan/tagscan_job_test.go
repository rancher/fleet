package imagescan

import (
	"fmt"
	"testing"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

var alphabeticalVersions = []string{"a", "b", "c"}
func TestLatestTag(t *testing.T) {
	alphabeticalAscPolicyLowercase := fleet.ImagePolicyChoice{
		Alphabetical: &fleet.AlphabeticalPolicy{Order: "asc"},
	}
	alphabeticalAscPolicyUppercase := fleet.ImagePolicyChoice{
		Alphabetical: &fleet.AlphabeticalPolicy{Order: "ASC"},
	}
	alphabeticalDescPolicyLowercase := fleet.ImagePolicyChoice{
		Alphabetical: &fleet.AlphabeticalPolicy{Order: "desc"},
	}
	alphabeticalDescPolicyUppercase := fleet.ImagePolicyChoice{
		Alphabetical: &fleet.AlphabeticalPolicy{Order: "DESC"},
	}

	out, err := latestTag(alphabeticalAscPolicyLowercase, alphabeticalVersions)
	if err != nil {
		t.Fatalf("Error getting latest tag: %v", err)
	}
	fmt.Println(out)

	out, err = latestTag(alphabeticalAscPolicyUppercase, alphabeticalVersions)
	if err != nil {
		t.Fatalf("Error getting latest tag: %v", err)
	}
	fmt.Println(out)

	out, err = latestTag(alphabeticalDescPolicyLowercase, alphabeticalVersions)
	if err != nil {
		t.Fatalf("Error getting latest tag: %v", err)
	}
	fmt.Println(out)
	
	out, err = latestTag(alphabeticalDescPolicyUppercase, alphabeticalVersions)
	if err != nil {
		t.Fatalf("Error getting latest tag: %v", err)
	}
	fmt.Println(out)
}
