package seen

import "k8s.io/apimachinery/pkg/util/sets"

type Seen interface {
	String(value string) bool
}

func New() Seen {
	return &strings{
		s: sets.NewString(),
	}
}

type strings struct {
	s sets.String
}

func (s strings) String(value string) bool {
	if s.s.Has(value) {
		return true
	}
	s.s.Insert(value)
	return false
}
