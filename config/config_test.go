package config

import (
	"strings"
	"testing"

	"bitbucket.org/creachadair/tarsnap"
)

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

func TestFindPath(t *testing.T) {
	cfg := &Config{
		Backup: []*Backup{{
			Name: "alpha",
			CreateOptions: tarsnap.CreateOptions{
				WorkDir: "/home/rooty",
				Include: []string{"bar/baz", "frob.cc"},
				Exclude: []string{"bar/baz/nuut/**"},
			},
		}, {
			Name: "bravo",
			CreateOptions: tarsnap.CreateOptions{
				Include: []string{"foo/quux", "bar/baz/frob", "bar/baz/nuut"},
				Exclude: []string{"foo/quux/zort/em.h"},
			},
		}},
		Config: tarsnap.Config{
			WorkDir: "/diabolo",
		},
	}

	tests := []struct {
		path string
		want string // backup set names
	}{
		// A path that isn't found anywhere.
		{"nonesuch", ""},

		// A path that matches the first backup only.
		{"frob.cc", "alpha"},

		// A path that matches the second backup only.
		{"foo/quux/apple.py", "bravo"},

		// A path that matches the second, but is excluded.
		{"foo/quux/zort/em.h", ""},

		// A path that matches both, but is excluded from one.
		{"bar/baz/nuut/test.h", "bravo"},

		// A path that matches both.
		{"bar/baz/frob/nut.py", "alpha bravo"},

		// Absolute paths are relativized.
		{"/diabolo/foo/quux/meeple", "bravo"},
		{"/home/rooty/frob.cc", "alpha"},
	}

	for _, test := range tests {
		var names []string
		for _, b := range cfg.FindPath(test.path) {
			names = append(names, b.Name)
		}
		got := strings.Join(names, " ")
		if got != test.want {
			t.Errorf("FindPath %q: got %q, want %q", test.path, got, test.want)
		}
	}
}
