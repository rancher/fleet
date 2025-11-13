/*
Copyright The Helm Authors.
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package helmdeployer

import (
	"context"
	"errors"
	"path"

	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart/common"

	"k8s.io/client-go/discovery"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// capabilities builds a Capabilities from discovery information.
func getCapabilities(ctx context.Context, c *action.Configuration) (*common.Capabilities, error) {
	if c.Capabilities != nil {
		return c.Capabilities, nil
	}
	dc, err := c.RESTClientGetter.ToDiscoveryClient()
	if err != nil {
		return nil, errors.New("could not get Kubernetes discovery client")
	}
	// force a discovery cache invalidation to always fetch the latest server version/capabilities.
	dc.Invalidate()
	kubeVersion, err := dc.ServerVersion()
	if err != nil {
		return nil, errors.New("could not get server version from Kubernetes")
	}
	// Issue #6361:
	// Client-Go emits an error when an API service is registered but unimplemented.
	// We trap that error here and print a warning. But since the discovery client continues
	// building the API object, it is correctly populated with all valid APIs.
	// See https://github.com/kubernetes/kubernetes/issues/72051#issuecomment-521157642
	apiVersions, err := getVersionSet(dc)
	if err != nil {
		if discovery.IsGroupDiscoveryFailedError(err) {
			logger := log.FromContext(ctx).WithName("helm-capabilities")
			logger.V(1).Info("WARNING: The Kubernetes server has an orphaned API service", "error", err.Error())
			logger.V(1).Info("WARNING: To fix this, kubectl delete apiservice <service-name>")
		} else {
			return nil, errors.New("could not get apiVersions from Kubernetes")
		}
	}

	c.Capabilities = common.DefaultCapabilities.Copy()
	c.Capabilities.APIVersions = apiVersions
	c.Capabilities.KubeVersion = common.KubeVersion{
		Version: kubeVersion.GitVersion,
		Major:   kubeVersion.Major,
		Minor:   kubeVersion.Minor,
	}
	return c.Capabilities, nil
}

// getVersionSet retrieves the set of available Kubernetes API versions and resources
// from the discovery client. It tolerates GroupDiscoveryFailedErrors which occur when
// some API groups are unavailable.
func getVersionSet(client discovery.ServerResourcesInterface) (common.VersionSet, error) {
	groups, resources, err := client.ServerGroupsAndResources()
	if err != nil && !discovery.IsGroupDiscoveryFailedError(err) {
		return common.DefaultVersionSet, errors.New("could not get apiVersions from Kubernetes")
	}

	// FIXME: The Kubernetes test fixture for cli appears to always return nil
	// for calls to Discovery().ServerGroupsAndResources(). So in this case, we
	// return the default API list. This is also a safe value to return in any
	// other odd-ball case.
	if len(groups) == 0 && len(resources) == 0 {
		return common.DefaultVersionSet, nil
	}

	versionMap := make(map[string]interface{})
	versions := []string{}

	// Extract the groups
	for _, g := range groups {
		for _, gv := range g.Versions {
			versionMap[gv.GroupVersion] = struct{}{}
		}
	}

	// Extract the resources
	var id string
	var ok bool
	for _, r := range resources {
		for _, rl := range r.APIResources {

			// A Kind at a GroupVersion can show up more than once. We only want
			// it displayed once in the final output.
			id = path.Join(r.GroupVersion, rl.Kind)
			if _, ok = versionMap[id]; !ok {
				versionMap[id] = struct{}{}
			}
		}
	}

	// Convert to a form that NewVersionSet can use
	for k := range versionMap {
		versions = append(versions, k)
	}

	return versions, nil
}
