/*
Copyright 2015 The Kubernetes Authors.

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

package endpoints

import (
	"bytes"
	"crypto/md5" //nolint:gosec // Non-crypto use
	"hash"
	"sort"

	hashutil "github.com/rancher/fleet/internal/cmd/agent/deployer/internal/diff/kubernetes_vendor/pkg/util/hash"
	v1 "k8s.io/api/core/v1"
)

// LessEndpointAddress compares IP addresses lexicographically and returns true if first argument is lesser than second
func LessEndpointAddress(a, b *v1.EndpointAddress) bool {
	ipComparison := bytes.Compare([]byte(a.IP), []byte(b.IP))
	if ipComparison != 0 {
		return ipComparison < 0
	}
	if b.TargetRef == nil {
		return false
	}
	if a.TargetRef == nil {
		return true
	}
	return a.TargetRef.UID < b.TargetRef.UID
}

// SortSubsets sorts an array of EndpointSubset objects in place.  For ease of
// use it returns the input slice.
// Note: EndpointSubset is part of the deprecated Endpoints API (core/v1).
// The successor is EndpointSlice (discovery.k8s.io/v1).
//
//nolint:staticcheck // EndpointSubset is deprecated but still supported; see fleet#3760.
func SortSubsets(subsets []v1.EndpointSubset) []v1.EndpointSubset {
	for i := range subsets {
		ss := &subsets[i]
		sort.Sort(addrsByIPAndUID(ss.Addresses))
		sort.Sort(addrsByIPAndUID(ss.NotReadyAddresses))
		sort.Sort(portsByHash(ss.Ports))
	}
	sort.Sort(subsetsByHash(subsets))
	return subsets
}

func hashObject(hasher hash.Hash, obj interface{}) []byte {
	hashutil.DeepHashObject(hasher, obj)
	return hasher.Sum(nil)
}

//nolint:staticcheck,nolintlint // EndpointSubset is deprecated but still supported; see fleet#3760.
type subsetsByHash []v1.EndpointSubset

func (sl subsetsByHash) Len() int      { return len(sl) }
func (sl subsetsByHash) Swap(i, j int) { sl[i], sl[j] = sl[j], sl[i] }
func (sl subsetsByHash) Less(i, j int) bool {
	hasher := md5.New() //nolint:gosec // Non-crypto use
	h1 := hashObject(hasher, sl[i])
	h2 := hashObject(hasher, sl[j])
	return bytes.Compare(h1, h2) < 0
}

type addrsByIPAndUID []v1.EndpointAddress

func (sl addrsByIPAndUID) Len() int      { return len(sl) }
func (sl addrsByIPAndUID) Swap(i, j int) { sl[i], sl[j] = sl[j], sl[i] }
func (sl addrsByIPAndUID) Less(i, j int) bool {
	return LessEndpointAddress(&sl[i], &sl[j])
}

type portsByHash []v1.EndpointPort

func (sl portsByHash) Len() int      { return len(sl) }
func (sl portsByHash) Swap(i, j int) { sl[i], sl[j] = sl[j], sl[i] }
func (sl portsByHash) Less(i, j int) bool {
	hasher := md5.New() //nolint:gosec // Non-crypto use
	h1 := hashObject(hasher, sl[i])
	h2 := hashObject(hasher, sl[j])
	return bytes.Compare(h1, h2) < 0
}
