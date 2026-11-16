package target

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/labels"
)

func TestSelectorCache_Cleanup(t *testing.T) {
	sc := &SelectorCache{}
	for _, td := range [][]string{
		{"ns0", "cg0", "1"},
		{"ns0", "cg1", "1"},
		{"ns1", "cg0", "1"},
		{"ns1", "cg0", "2"},
		{"ns1", "cg2", "3"},
	} {
		sc.Set(td[0], td[1], td[2], labels.Everything())
	}

	assert.NotPanics(t, func() {
		sc.Retain("notExisting", map[string]struct{}{})
	}, "not existing namespace does not cause a panic")
	assert.NotContains(t, sc.namespaces, "notExisting")

	expected_len := len(sc.namespaces)
	sc.Retain("ns0", map[string]struct{}{"cg1": {}})
	assert.Len(t, sc.namespaces, expected_len, "non-active ClusterGroup should be removed")
	assert.NotContains(t, sc.namespaces["ns0"], "cg0", "non-active ClusterGroup should be removed")
	assert.Contains(t, sc.namespaces["ns0"], "cg1", "listed ClusterGroup should be kept")

	expected_len--
	sc.Retain("ns0", map[string]struct{}{})
	assert.NotContains(t, sc.namespaces, "ns0", "namespace without ClusterGroups should be removed")
	assert.Contains(t, sc.namespaces, "ns1", "other namespaces should be kept")
	assert.Len(t, sc.namespaces, expected_len, "non-active ClusterGroup should be removed")
}
