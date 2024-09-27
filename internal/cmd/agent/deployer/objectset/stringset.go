package objectset

var empty struct{}

// Set is an exceptionally simple `set` implementation for strings.
// It is not threadsafe, but can be used in place of a simple `map[string]struct{}`
// as long as you don't want to do too much with it.
type Set struct {
	m map[string]struct{}
}

func (s *Set) Add(ss ...string) {
	if s.m == nil {
		s.m = make(map[string]struct{}, len(ss))
	}
	for _, k := range ss {
		s.m[k] = empty
	}
}

func (s *Set) Values() []string {
	i := 0
	keys := make([]string, len(s.m))
	for key := range s.m {
		keys[i] = key
		i++
	}

	return keys
}
