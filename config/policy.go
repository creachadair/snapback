// Copyright (C) 2018 Michael J. Fromberger. All Rights Reserved.

package config

import (
	"fmt"
	"sort"

	"github.com/creachadair/tarsnap"
)

// A Policy specifies a policy for which backups to keep. When given a set of
// policies, the policy that "best" applies to an archive is the earliest,
// narrowest span of time between min and max before present, inclusive, that
// includes the creation time of the archive.
//
// For example suppose X is an archive created 7 days before present, and we
// have these policies:
//
//	P(min=1d, max=10d)
//	Q(min=4d, max=8d)
//	R(min=3d, max=6d)
//
// Archive X will be governed by policy Q. R is ineligible because it does not
// span the creation time of X, and Q is preferable to P because Q is only 4
// days wide whereas P is 9 days wide.
//
// A policy with a max value of 0 is assumed to end at time +∞.
type Policy struct {
	// The rest of this policy applies to backups created in the inclusive
	// interval between min and max before present. Max == 0 means +∞.
	Min Interval `yaml:"after"`
	Max Interval `yaml:"until"`

	// If positive, keep up to this many of most-recent matching archives.
	Latest int

	// If set, retain the specified number of samples per period within this
	// range. Sample ranges are based on the Unix epoch so that they do not move
	// over time, and the latest-created archive in each window is selected as
	// the candidate for that window.
	Sample *Sampling
}

// apply returns all the input archives that are expired by p.
func (p *Policy) apply(c *Config, batch []tarsnap.Archive) []tarsnap.Archive {
	if p.Latest >= len(batch) {
		c.logf("+ keep %d, all candidates are recent", len(batch))
		return nil
	} else if p.Latest > 0 {
		batch = batch[:len(batch)-p.Latest]
		c.logf("+ keep latest %d, %d left", p.Latest, len(batch))
	}
	if p.Sample == nil || p.Sample.N == 0 {
		c.logf("- drop %d, no sampling is enabled", len(batch))
		return batch // no samples, discard everything else in range
	} else if p.Sample.Period == 0 {
		c.logf("+ keep all %d, sample period is zero", len(batch))
		return nil
	}

	// The width of the scaled sampling interval, where s/p = 1/ival.
	ival := p.Sample.Period / Interval(p.Sample.N)

	// Find the smallest interval beginning at or before the last entry in the
	// policy window. We keep the last (most recent) entry in each interval.
	// Note that we work backward because the archives are ordered by creation
	// timestamp in ascending order (smaller timestamps are older).
	i := len(batch) - 1
	last := durationInterval(batch[i].Created.Sub(timeZero))
	base := ival * (last / ival)
	c.logf("+ keep %q by sampling rule %v [base %v]", batch[i].Name, p.Sample, base)

	var drop []tarsnap.Archive
	for i--; i >= 0; i-- {
		age := durationInterval(batch[i].Created.Sub(timeZero))
		if age >= base {
			drop = append(drop, batch[i])
			c.logf("- drop %q by sampling rule %v [%v > %v]", batch[i].Name, p.Sample, age, base)
		} else {
			// We crossed into the next bucket -- keep this representative.
			base -= ival
			c.logf("+ keep %q by sampling rule %v [base %v]", batch[i].Name, p.Sample, base)
		}
	}
	return drop
}

// String renders the policy in human-readable form.
func (p *Policy) String() string {
	max := "∞"
	if p.Max != forever {
		max = fmt.Sprint(max)
	}
	return fmt.Sprintf("rule [%v..%s] keep %d sample %s", p.Min, max, p.Latest, p.Sample)
}

// Less reports whether p precedes q in canonical order. Policies are ordered
// by the width of their interval, with ties broken by start time.
func (p *Policy) Less(q *Policy) bool {
	u, v := p.Max-p.Min, q.Max-q.Min
	if u == v {
		return p.Min > q.Min
	}
	return u < v
}

const forever = 1<<63 - 1

func sortExp(es []*Policy) {
	// Treat max == 0 as having no effective upper bound.
	for _, e := range es {
		if e.Max == 0 {
			e.Max = forever
		}
	}

	// Order rules by the width of their interval, with earlier intervals first.
	sort.Slice(es, func(i, j int) bool {
		return es[i].Less(es[j])
	})
}
