package bundlereader

import (
	"bytes"
	"testing"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/util/yaml"
)

const (
	valuesOneYaml = `microService1:
  resources:
    limits:
      cpu: 500m
      memory: 500Mi
    requests:
      cpu: 256m
      memory: 256Mi

microService2:
  resources:
    limits:
      cpu: 500m
      memory: 500Mi
    requests:
      cpu: 256m
      memory: 256Mi
`
	valuesTwoYaml = `microService1:
  replicas: 1
microService2:
  replicas: 2`
)

func TestValueMerge(t *testing.T) {
	first := &fleet.GenericMap{}
	second := &fleet.GenericMap{}

	err := yaml.NewYAMLToJSONDecoder(bytes.NewBufferString(valuesOneYaml)).Decode(first)
	if err != nil {
		t.Fatalf("error during valuesOneYaml parsing %v", err)
	}

	err = yaml.NewYAMLToJSONDecoder(bytes.NewBufferString(valuesTwoYaml)).Decode(second)
	if err != nil {
		t.Fatalf("error during valuesTwoYaml parsing %v", err)
	}

	mergeMap := mergeGenericMap(first, second)

	for _, serviceName := range []string{"microService1", "microService2"} {
		serviceVals, ok := mergeMap.Data[serviceName]
		if !ok {
			t.Fatalf("unable to find parent key for service %s", serviceName)
		}
		resourceVals, ok := serviceVals.(map[string]interface{})["resources"]
		if !ok {
			t.Fatalf("unable to find key resources in values for service %s", serviceName)
		}

		limitVals, ok := resourceVals.(map[string]interface{})["limits"]
		if !ok {
			t.Fatalf("unable to find key limits in resources for service %s", serviceName)
		}

		_, ok = limitVals.(map[string]interface{})["cpu"]
		if !ok {
			t.Fatalf("unable to find key cpu in limits for service %s", serviceName)
		}

		_, ok = limitVals.(map[string]interface{})["memory"]
		if !ok {
			t.Fatalf("unable to find key memory in limits for service %s", serviceName)
		}

		requestVals, ok := resourceVals.(map[string]interface{})["requests"]
		if !ok {
			t.Fatalf("unable to find key requests in resources for service %s", serviceName)
		}

		_, ok = requestVals.(map[string]interface{})["cpu"]
		if !ok {
			t.Fatalf("unable to find key cpu in requests for service %s", serviceName)
		}

		_, ok = requestVals.(map[string]interface{})["memory"]
		if !ok {
			t.Fatalf("unable to find key memory in requests for service %s", serviceName)
		}
		_, ok = serviceVals.(map[string]interface{})["replicas"]
		if !ok {
			t.Fatalf("unable to find key replicas in values for service %s", serviceName)
		}
	}
}

func TestShouldAddAuthToRequest(t *testing.T) {
	cases := []struct {
		name             string
		helmRepoURLRegex string
		repo             string
		chart            string
		want             bool
		wantErr          bool
	}{
		{
			name:             "empty regex always returns false",
			helmRepoURLRegex: "",
			repo:             "https://charts.example.com",
			want:             false,
		},
		{
			name:             "regex matches repo URL",
			helmRepoURLRegex: "https://charts\\.example\\.com.*",
			repo:             "https://charts.example.com/stable",
			want:             true,
		},
		{
			name:             "regex does not match repo URL",
			helmRepoURLRegex: "https://charts\\.example\\.com.*",
			repo:             "https://evil.attacker.com",
			want:             false,
		},
		{
			name:             "no repo falls back to chart URL match",
			helmRepoURLRegex: "oci://registry\\.example\\.com.*",
			chart:            "oci://registry.example.com/charts/mychart",
			want:             true,
		},
		{
			name:             "regex does not match URL injected as query parameter",
			helmRepoURLRegex: "https://charts\\.example\\.com.*",
			repo:             "https://evil.attacker.com/?url=https://charts.example.com",
			want:             false,
		},
		{
			name:             "pre-anchored regex still matches",
			helmRepoURLRegex: "^https://charts\\.example\\.com.*",
			repo:             "https://charts.example.com/stable",
			want:             true,
		},
		{
			name:             "alternation anchors both alternatives",
			helmRepoURLRegex: "https://host1\\.example\\.com/.*|https://host2\\.example\\.com/.*",
			repo:             "https://host2.example.com/charts",
			want:             true,
		},
		{
			name:             "alternation does not match unanchored second alternative",
			helmRepoURLRegex: "https://host1\\.example\\.com/.*|https://host2\\.example\\.com/.*",
			repo:             "evil.com?url=https://host2.example.com/x",
			want:             false,
		},
		{
			name:             "invalid regex returns error",
			helmRepoURLRegex: "[invalid",
			repo:             "https://charts.example.com",
			wantErr:          true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := shouldAddAuthToRequest(c.helmRepoURLRegex, c.repo, c.chart)
			if c.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, c.want, got)
		})
	}
}

func TestAddRemoteChartsStripsCredentials(t *testing.T) {
	// When helmRepoURLRegex is empty, addRemoteCharts must strip Username and
	// Password from the Auth passed to each directory, while keeping transport
	// flags (BasicHTTP, InsecureSkipVerify) intact.
	auth := Auth{
		Username:           "user",
		Password:           "secret",
		SSHPrivateKey:      []byte("fake-ssh-key"),
		BasicHTTP:          true,
		InsecureSkipVerify: true,
	}

	// Use a chart path that does not exist on disk so addRemoteCharts processes
	// it, and an empty Repo so ChartURL returns the chart string directly
	// without a network call.
	charts := []*fleet.HelmOptions{
		{Chart: "/nonexistent/chart"},
	}

	dirs, err := addRemoteCharts(nil, t.TempDir(), charts, auth, "")
	require.NoError(t, err)
	require.Len(t, dirs, 1)

	got := dirs[0].auth
	assert.Empty(t, got.Username, "Username must be stripped when helmRepoURLRegex is empty")
	assert.Empty(t, got.Password, "Password must be stripped when helmRepoURLRegex is empty")
	assert.Nil(t, got.SSHPrivateKey, "SSHPrivateKey must be stripped when helmRepoURLRegex is empty")
	assert.True(t, got.BasicHTTP, "BasicHTTP must be preserved when stripping credentials")
	assert.True(t, got.InsecureSkipVerify, "InsecureSkipVerify must be preserved when stripping credentials")
}
