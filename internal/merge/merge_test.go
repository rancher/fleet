package merge

import (
	"strconv"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// identity is a trivial onCollide that returns the custom value.
func identity(_ int, c int) int { return c }

// sum is an onCollide that adds base and custom together.
func sum(b, c int) int { return b + c }

func key(v int) string { return strconv.Itoa(v) }

func TestByKey_EmptyCustomReturnsBase(t *testing.T) {
	base := []int{1, 2, 3}
	got := ByKey(base, nil, key, identity)
	if diff := cmp.Diff(base, got); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestByKey_EmptyBaseAppendsCustom(t *testing.T) {
	got := ByKey([]int{}, []int{4, 5}, key, identity)
	want := []int{4, 5}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestByKey_NoCollisionAppendsCustom(t *testing.T) {
	got := ByKey([]int{1, 2}, []int{3, 4}, key, identity)
	want := []int{1, 2, 3, 4}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestByKey_CollisionCallsOnCollide(t *testing.T) {
	// base={1,2}, custom={2,3}: key "2" collides, sum → 4; "3" is new → append
	got := ByKey([]int{1, 2}, []int{2, 3}, key, sum)
	want := []int{1, 4, 3}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestByKey_CollisionPreservesBaseOrder(t *testing.T) {
	// Custom arrives in reverse base-order; verify both collisions are applied
	// in-place at the base positions and the uncollided entry (20) is preserved.
	got := ByKey([]int{1, 20, 300}, []int{300, 1}, key, sum)
	want := []int{2, 20, 600} // sum(1,1)=2 at idx 0; 20 untouched; sum(300,300)=600 at idx 2
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestByKey_DuplicateCustomKeyFirstWins(t *testing.T) {
	// Two custom entries with key "9": only the first one should take effect.
	keyOf9 := func(v int) string {
		if v == 9 || v == 99 {
			return "9"
		}
		return strconv.Itoa(v)
	}
	got := ByKey([]int{1}, []int{9, 99}, keyOf9, identity)
	want := []int{1, 9} // 99 is a duplicate of key "9" and must be skipped
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestByKey_EmptyKeyAlwaysAppended(t *testing.T) {
	noKey := func(_ int) string { return "" }
	got := ByKey([]int{1}, []int{2, 2}, noKey, identity)
	// Both custom entries have empty key → always appended, including the duplicate
	want := []int{1, 2, 2}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}
