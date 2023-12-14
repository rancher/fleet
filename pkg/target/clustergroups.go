package target

import (
	"sync"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

type clusterGroupEntry struct {
	version int64

	selector      labels.Selector
	selectorError error
}

func (e *clusterGroupEntry) sameAs(cg *fleet.ClusterGroup) bool {
	return e.version == cg.Generation
}

func newEntry(cg *fleet.ClusterGroup) *clusterGroupEntry {
	entry := &clusterGroupEntry{version: cg.Generation}

	if cg.Spec.Selector != nil {
		entry.selector, entry.selectorError = metav1.LabelSelectorAsSelector(cg.Spec.Selector)
	}

	return entry
}

type ClusterGroupStore struct {
	mu    sync.RWMutex
	store map[string]*clusterGroupEntry
}

func newClusterGroupStore() *ClusterGroupStore {
	return &ClusterGroupStore{store: map[string]*clusterGroupEntry{}}
}

func (s *ClusterGroupStore) getEntry(key string) (*clusterGroupEntry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, ok := s.store[key]
	return entry, ok
}

func (s *ClusterGroupStore) Store(key string, cg *fleet.ClusterGroup) {
	if entry, ok := s.getEntry(key); ok && entry.sameAs(cg) {
		return
	}

	entry := newEntry(cg)
	s.setEntry(key, entry)
}

func (s *ClusterGroupStore) setEntry(key string, entry *clusterGroupEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.store[key] = entry
}

func (s *ClusterGroupStore) Delete(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.store, key)
}

func (s *ClusterGroupStore) GetSelector(cg *fleet.ClusterGroup) (labels.Selector, error) {
	key := cg.Namespace + "/" + cg.Name

	entry, found := s.getEntry(key)
	if !found || !entry.sameAs(cg) {
		entry = newEntry(cg)
		s.setEntry(key, entry)
	}

	return entry.selector, nil
}
