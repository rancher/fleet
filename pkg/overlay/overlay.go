package overlay

import (
	"fmt"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/pkg/seen"
)

func Resolve(spec *fleet.BundleSpec, overlays ...string) (map[string]fleet.BundleOverlay, []string, error) {
	allOverlays := map[string]fleet.BundleOverlay{}
	for _, overlay := range spec.Overlays {
		allOverlays[overlay.Name] = overlay
	}
	overlaySet := traverse(allOverlays, seen.New(), overlays...)
	for i := len(overlaySet) - 1; i >= 0; i-- {
		_, ok := allOverlays[overlaySet[i]]
		if !ok {
			return nil, nil, fmt.Errorf("failed to find referenced overlay %s", overlaySet[i])
		}
	}
	return allOverlays, overlaySet, nil
}

func traverse(overlays map[string]fleet.BundleOverlay, seen seen.Seen, targets ...string) []string {
	var result []string

	for _, target := range targets {
		if seen.String(target) {
			continue
		}

		result = append(result, target)
		result = append(result, traverse(overlays, seen, overlays[target].Overlays...)...)
	}

	return result
}
