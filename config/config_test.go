package config

import "testing"

func TestPolicyOrder(t *testing.T) {
	tests := []struct {
		p1, p2 *Policy
		want   bool
	}{
		// Ordering is irreflexive.
		{&Policy{Min: 1, Max: 2}, &Policy{Min: 1, Max: 2}, false},

		// Order is primarily by width, narrower first.
		{&Policy{Min: 2, Max: 10}, &Policy{Min: 2, Max: 3}, false},
		{&Policy{Min: 2, Max: 3}, &Policy{Min: 2, Max: 10}, true},

		// On a tie of widths, the later start should come first.
		{&Policy{Min: 1, Max: 3}, &Policy{Min: 2, Max: 4}, false},
		{&Policy{Min: 2, Max: 4}, &Policy{Min: 1, Max: 3}, true},

		// One right-unbounded interval is shorter than another if it starts later.
		{&Policy{Min: 0, Max: 0}, &Policy{Min: 1, Max: 0}, false},
		{&Policy{Min: 1, Max: 0}, &Policy{Min: 0, Max: 0}, true},
	}
	for _, test := range tests {
		if got := test.p1.Less(test.p2); got != test.want {
			t.Errorf("Wrong order comparing:\n- %v\n- %v\ngot %v, want %v",
				test.p1, test.p2, got, test.want)
		}
	}
}
