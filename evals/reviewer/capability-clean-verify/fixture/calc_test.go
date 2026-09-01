package calc

import "testing"

func TestMax(t *testing.T) {
	if got := Max(3, 1); got != 3 {
		t.Errorf("Max(3,1) = %d, want 3", got)
	}
	if got := Max(1, 3); got != 3 {
		t.Errorf("Max(1,3) = %d, want 3", got)
	}
	if got := Max(1, 1); got != 1 {
		t.Errorf("Max(1,1) = %d, want 1", got)
	}
}
