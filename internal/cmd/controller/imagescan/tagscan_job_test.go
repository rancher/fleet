package imagescan

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"

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

func TestGetDigest(t *testing.T) {
	var mu sync.Mutex
	methods := map[string]int{}
	failHead := false
	reg := registry.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/manifests/") {
			mu.Lock()
			methods[r.Method]++
			fh := failHead
			mu.Unlock()
			if fh && r.Method == http.MethodHead {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
		}
		reg.ServeHTTP(w, r)
	}))
	defer srv.Close()

	ref := strings.TrimPrefix(srv.URL, "http://") + "/test/img:v1"
	img, err := random.Image(64, 1)
	if err != nil {
		t.Fatal(err)
	}
	tag, err := name.NewTag(ref)
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.Write(tag, img); err != nil {
		t.Fatal(err)
	}
	want, err := img.Digest()
	if err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	clear(methods)
	mu.Unlock()
	got, err := getDigest(ref)
	if err != nil {
		t.Fatal(err)
	}
	if got != want.String() {
		t.Errorf("got %s, want %s", got, want)
	}
	mu.Lock()
	if methods[http.MethodGet] != 0 || methods[http.MethodHead] != 1 {
		t.Errorf("expected only HEAD requests for manifest, got %v", methods)
	}
	failHead = true
	mu.Unlock()

	got, err = getDigest(ref)
	if err != nil {
		t.Fatal(err)
	}
	if got != want.String() {
		t.Errorf("fallback: got %s, want %s", got, want)
	}
}
