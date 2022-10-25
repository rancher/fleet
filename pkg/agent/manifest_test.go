package agent

import "testing"

func TestImageResolve(t *testing.T) {
	tests := []struct {
		systemDefaultRegistry string
		privateRepoURL        string
		image                 string
		expected              string
	}{
		{"", "", "rancher/fleet:dev", "rancher/fleet:dev"},
		{"mirror.example/", "", "mirror.example/rancher/fleet:dev", "mirror.example/rancher/fleet:dev"},
		{"mirror.example/", "local.example", "mirror.example/rancher/fleet:dev", "local.example/rancher/fleet:dev"},
	}

	for _, d := range tests {
		image := resolve(d.systemDefaultRegistry, d.privateRepoURL, d.image)
		if image != d.expected {
			t.Errorf("expected %s, got %s", d.expected, image)
		}
	}
}
