package objectset

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestObjectByKey_Namespaces(t *testing.T) {
	tests := []struct {
		name           string
		objects        ObjectByKey
		wantNamespaces []string
	}{
		{
			name:           "empty",
			objects:        ObjectByKey{},
			wantNamespaces: nil,
		},
		{
			name: "1 namespace",
			objects: ObjectByKey{
				ObjectKey{Namespace: "ns1", Name: "a"}: nil,
				ObjectKey{Namespace: "ns1", Name: "b"}: nil,
			},
			wantNamespaces: []string{"ns1"},
		},
		{
			name: "many namespaces",
			objects: ObjectByKey{
				ObjectKey{Namespace: "ns1", Name: "a"}: nil,
				ObjectKey{Namespace: "ns2", Name: "b"}: nil,
			},
			wantNamespaces: []string{"ns1", "ns2"},
		},
		{
			name: "many namespaces with duplicates",
			objects: ObjectByKey{
				ObjectKey{Namespace: "ns1", Name: "a"}: nil,
				ObjectKey{Namespace: "ns2", Name: "b"}: nil,
				ObjectKey{Namespace: "ns1", Name: "c"}: nil,
			},
			wantNamespaces: []string{"ns1", "ns2"},
		},
		{
			name: "missing namespace",
			objects: ObjectByKey{
				ObjectKey{Namespace: "ns1", Name: "a"}: nil,
				ObjectKey{Name: "b"}:                   nil,
			},
			wantNamespaces: []string{"", "ns1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotNamespaces := tt.objects.Namespaces()
			assert.ElementsMatchf(t, tt.wantNamespaces, gotNamespaces, "Namespaces() = %v, want %v", gotNamespaces, tt.wantNamespaces)
		})
	}
}
