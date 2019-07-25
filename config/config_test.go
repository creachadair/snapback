// Copyright (C) 2018 Michael J. Fromberger. All Rights Reserved.

package config

import (
	"strings"
	"testing"

	"github.com/creachadair/tarsnap"
	"github.com/google/go-cmp/cmp"
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
		want  []int
	}{
		// An explicit expiration with no named policy, uses only those rules.
		{input: &Backup{
			Expiration: []*Policy{{Latest: 3}},
		}, want: []int{3}},

		// Explicit rules extend a named policy.
		{input: &Backup{
			Expiration: []*Policy{{Latest: 4}},
			Policy:     "named",
		}, want: []int{2, 4}},

		// The name "none" produces no policy.
		{input: &Backup{Policy: "none"}, want: nil},

		// Extending "none" works.
		{input: &Backup{
			Expiration: []*Policy{{Latest: 6}},
			Policy:     "none",
		}, want: []int{6}},

		// The names "default" and "" use the default policy.
		{input: &Backup{Policy: "default"}, want: []int{1}},
		{input: &Backup{Policy: ""}, want: []int{1}},

		// If "default" is named explicitly, it is extended.
		{input: &Backup{
			Expiration: []*Policy{{Latest: 7}},
			Policy:     "default",
		}, want: []int{1, 7}},

		// Other named policies are chosen.
		{input: &Backup{Policy: "named"}, want: []int{2}},
	}
	process := func(ps []*Policy) (zs []int) {
		for _, p := range ps {
			zs = append(zs, p.Latest)
		}
		return
	}

	for _, test := range tests {
		p := cfg.findPolicy(test.input)
		got := process(p)
		if diff := cmp.Diff(got, test.want); diff != "" {
			t.Errorf("Wrong policy for %+v: (-want, +got)\n%s", test, diff)
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

func TestFindSet(t *testing.T) {
	cfg := &Config{
		Backup: []*Backup{
			{Name: "important"},
			{Name: "ancillary"},
			{Name: "miscellaneous"},
		},
	}
	tests := []struct {
		name string
		ok   bool
	}{
		{"", false},
		{"important", true},
		{"stupid", false},
		{"ancillary", true},
		{"MISCELLANEOUS", false},
	}
	for _, test := range tests {
		got := cfg.FindSet(test.name)
		if ok := (got != nil); ok != test.ok {
			t.Errorf("FindSet(%q): found %v, want %v", test.name, ok, test.ok)
		} else if test.ok && got.Name != test.name {
			t.Errorf("FindSet(%[1]q): got name %q, want %[1]q", test.name, got.Name)
		}
	}
}
