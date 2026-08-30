package greet

import "testing"

func TestGreet(t *testing.T) {
	if got := Greet("Ada"); got != "Hello, Ada!" {
		t.Errorf("Greet(Ada) = %q, want %q", got, "Hello, Ada!")
	}
}
