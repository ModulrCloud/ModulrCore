package tests

import (
	"reflect"
	"testing"
)

// Same behavior as your removeFromSlice in system_contracts.
func removeFromSlice[T comparable](s []T, v T) []T {
	for i, x := range s {
		if x == v {
			return append(s[:i], s[i+1:]...)
		}
	}
	return s
}

func TestRemoveFromSlice_AssignmentMatters(t *testing.T) {
	// Wrong usage: ignoring the returned slice -> no change happens.
	// wrong := []string{"A", "B", "C"}
	// _ = removeFromSlice(wrong, "B") // BUG: result ignored
	// if !reflect.DeepEqual(wrong, []string{"A", "B", "C"}) {
	// 	t.Fatalf("expected slice unchanged when result is ignored; got %v", wrong)
	// }

	// Correct usage: assign the returned slice.
	right := []string{"A", "B", "C"}
	right = removeFromSlice(right, "B")
	if !reflect.DeepEqual(right, []string{"A", "C"}) {
		t.Fatalf("expected [A C], got %v", right)
	}
}
