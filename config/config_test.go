// Copyright (C) 2018 Michael J. Fromberger. All Rights Reserved.

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

func TestPolicyAssignment(t *testing.T) {
	cfg := &Config{
		Expiration: []*Policy{{Latest: 1}},
		Policy: map[string][]*Policy{
			"named":   {{Latest: 2}},
			"default": {{Latest: 666}}, // should not be assigned
		},
	}
	tests := []struct {
		input *Backup
		want  int
	}{
		// An explicit expiration.
		{input: &Backup{
			Expiration: []*Policy{{Latest: 3}},
		}, want: 3},

		// Explicit overrides policy.
		{input: &Backup{
			Expiration: []*Policy{{Latest: 4}},
			Policy:     "named",
		}, want: 4},

		// The names "default" and "" use the default policy.
		{input: &Backup{Policy: "default"}, want: 1},
		{input: &Backup{Policy: ""}, want: 1},

		// Other named policies are chosen.
		{input: &Backup{Policy: "named"}, want: 2},
	}
	for _, test := range tests {
		p := cfg.findPolicy(test.input)
		if len(p) == 0 {
			t.Errorf("Policy for %+v not found", test.input)
		} else if got, want := p[0].Latest, test.want; got != want {
			t.Errorf("Wrong policy for %+v: got %v, want %v", test.input, got, want)
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
			names = append(names, b.Backup.Name)
		}
		got := strings.Join(names, " ")
		if got != test.want {
			t.Errorf("FindPath %q: got %q, want %q", test.path, got, test.want)
		}
	}
}
