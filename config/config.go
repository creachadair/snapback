// Copyright (C) 2018 Michael J. Fromberger. All Rights Reserved.

// Package config describes configuration settings for the snapback tool.
package config

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/creachadair/tarsnap"
	yaml "gopkg.in/yaml.v2"
)

var timeZero = time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)

// A Config contains settings for the snapback tool. This is the top-level
// message used to parse the config file contents as YAML.
type Config struct {
	// An ordered list of backups to be created.
	Backup []*Backup

	// Default expiration policies.
	Expiration []*Policy

	// Named expiration policy sets.
	Policy map[string][]*Policy

	// Enable verbose logging.
	Verbose bool

	// Configuration settings for the tarsnap tool.
	tarsnap.Config `yaml:",inline"`
}

// A BackupPath describes a path relative to a particular backup.
type BackupPath struct {
	Relative string
	Backup   *Backup
}

// FindPath reports the backups that claim path, or nil if there are none.
// N.B. Only the current backup set configurations are examined.
func (c *Config) FindPath(path string) []BackupPath {
	var out []BackupPath
	for _, b := range c.Backup {
		rel, ok := containsPath(b, c.WorkDir, path)
		if !ok {
			continue
		}

		// Apply any modification rules to the path, so that the caller gets the
		// name that occurs in the backup set.
		for _, m := range b.Modify {
			r, err := tarsnap.ParseRule(m)
			if err != nil {
				log.Printf("Warning: invalid substitution rule %#q: %v [ignored]", m, err)
				continue
			}
			if s, ok := r.Apply(rel); ok {
				rel = s
				break
			}
		}
		out = append(out, BackupPath{
			Relative: rel,
			Backup:   b,
		})
	}
	return out
}

// findPolicy returns the expiration rules for this backup. If it does not have
// any of its own, use the defaults. If there are no defaults, nothing expires.
func (c *Config) findPolicy(b *Backup) []*Policy {
	if len(b.Expiration) != 0 {
		return b.Expiration
	} else if b.Policy == "" || b.Policy == "default" {
		return c.Expiration
	}
	return c.Policy[b.Policy]
}

// FindExpired returns a slice of the archives in arch that are eligible for
// removal under the expiration policies in effect for c, given that now is the
// moment denoting the present.
func (c *Config) FindExpired(arch []tarsnap.Archive, now time.Time) []tarsnap.Archive {
	c.logf("Finding expired archives, %d inputs, current time %v", len(arch), now)

	// Partition the archives according to which backup owns them, to simplify
	// figuring out which rules apply to each batch.
	sets := make(map[string][]tarsnap.Archive)
	for _, a := range arch {
		if _, err := time.Parse(".20060102-1504", a.Tag); err != nil {
			c.logf("Skipping archive %q (wrong name format)", a.Name)
			continue // not the correct format
		}
		sets[a.Base] = append(sets[a.Base], a)
	}

	var match []tarsnap.Archive
	for _, b := range c.Backup {
		exp := c.findPolicy(b)
		if len(exp) == 0 {
			c.logf("No expiration rules for %s [skipping]", b.Name)
			continue // nothing to do
		}
		c.logf("Applying %d expiration rules for %s", len(exp), b.Name)

		// Now, find all the archives belonging this backup which are affected by
		// some rule, and record which if any rule applies. If no rule applies,
		// the archive is kept unconditionally. The slice for each rule is in
		// order by creation date (oldest to newest).
		rules := make(map[*Policy][]tarsnap.Archive)
		for _, a := range sets[b.Name] {
			age := durationInterval(now.Sub(a.Created))
			for _, rule := range exp {
				if rule.Min <= age && (rule.Max == 0 || rule.Max >= age) {
					rules[rule] = append(rules[rule], a)
					break
				}
			}
		}

		// Finally, apply the policy...
		for rule, batch := range rules {
			c.logf(":: %v (%d candidates)", rule, len(batch))
			match = append(match, rule.apply(c, batch)...)
		}
	}
	return match
}

func (c *Config) logf(msg string, args ...interface{}) {
	if c.Verbose {
		log.Printf(msg, args...)
	}
}

// A Policy specifies a policy for which backups to keep. When given a set of
// policies, the policy that "best" applies to an archive is the earliest,
// narrowest span of time between min and max before present, inclusive, that
// includes the creation time of the archive.
//
// For example suppose X is an archive created 7 days before present, and we
// have these policies:
//
//    P(min=1d, max=10d)
//    Q(min=4d, max=8d)
//    R(min=3d, max=6d)
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

// A Backup describes a collection of files to be backed up as a unit together.
type Backup struct {
	// The name defines the base name of the archive. A timestamp will be
	// appended to this name to obtain the complete name.
	Name string

	// Expiration policies.
	Expiration []*Policy

	// Named expiration policy (ignored if Expiration is set).
	Policy string

	// Expand shell globs in included paths.
	GlobIncludes bool `json:"globIncludes" yaml:"glob-includes"`

	// The archive creation options for this backup.
	tarsnap.CreateOptions `yaml:",inline"`
}

// Parse decodes a *Config from the specified reader.
func Parse(r io.Reader) (*Config, error) {
	dec := yaml.NewDecoder(r)
	dec.SetStrict(true)
	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		return nil, err
	}
	sortExp(cfg.Expiration)
	expand(&cfg.Keyfile)
	expand(&cfg.WorkDir)

	seen := make(map[string]bool)
	for _, b := range cfg.Backup {
		if b.Name == "" {
			return nil, errors.New("empty backup name")
		} else if seen[b.Name] {
			return nil, fmt.Errorf("repeated backup name %q", b.Name)
		}
		seen[b.Name] = true
		sortExp(b.Expiration)
		expand(&b.WorkDir)
		if b.GlobIncludes {
			expandGlobs(b, cfg.WorkDir)
		}
	}
	for _, named := range cfg.Policy {
		sortExp(named)
	}
	return &cfg, nil
}

func expand(s *string) { *s = os.ExpandEnv(*s) }

func expandGlobs(b *Backup, wd string) {
	vpath := func(inc string) string {
		if filepath.IsAbs(inc) {
			return inc
		} else if b.WorkDir != "" {
			return filepath.Join(b.WorkDir, inc)
		}
		return filepath.Join(wd, inc)
	}

	var paths []string
	for _, inc := range b.Include {
		path := vpath(inc)
		matches, _ := filepath.Glob(path)
		paths = append(paths, matches...)
	}
	b.Include = paths
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

// An Interval represents a time interval in seconds. An Interval can be parsed
// from a string in the format "d.dd unit" or "d unit", where unit is one of
//
//   s, sec, secs         -- seconds
//   h, hr, hour, hours   -- hours
//   d, day, days         -- days (defined as 24 hours)
//   w, wk, week, weeks   -- weeks (defined as 7 days)
//   m, mo, month, months -- months (defined as 365.25/12=30.4375 days)
//   y, yr, year, years   -- years (defined as 365.25 days)
//
// The space between the number and the unit is optional. Fractional values are
// permitted, and results are rounded toward zero.
type Interval int64

// UnmarshalYAML decodes an interval from a string in the format accepted by
// parseInterval.
func (iv *Interval) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var s string
	if err := unmarshal(&s); err != nil {
		return err
	}
	parsed, err := parseInterval(s)
	if err != nil {
		return err
	}
	*iv = Interval(parsed)
	return nil
}

func durationInterval(d time.Duration) Interval {
	return Interval(d / time.Second)
}

// Constants for interval notation.
const (
	Second Interval = 1
	Hour            = 3600 * Second
	Day             = 24 * Hour
	Week            = 7 * Day
	Month           = Interval(30.4375 * float64(Day))
	Year            = Interval(365.25 * float64(Day))
)

var dx = regexp.MustCompile(`^(\d+|\d*\.\d+)? ?(\w+)$`)

func parseInterval(s string) (Interval, error) {
	m := dx.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("invalid interval %q", s)
	}
	if m[1] == "" {
		m[1] = "1"
	}
	f, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, fmt.Errorf("invalid number: %v", err)
	}
	switch m[2] {
	case "s", "sec", "secs":
		f *= float64(Second)
	case "h", "hr", "hrs":
		f *= float64(Hour)
	case "d", "day", "days":
		f *= float64(Day)
	case "w", "wk", "week", "weeks":
		f *= float64(Week)
	case "m", "mo", "mon", "month", "months":
		f *= float64(Month)
	case "y", "yr", "year", "years":
		f *= float64(Year)
	default:
		return 0, fmt.Errorf("unknown unit %q", m[2])
	}
	return Interval(f), nil
}

// A Sampling denotes a rule for how frequently to sample a sequence.
type Sampling struct {
	N      int      // number of samples per period
	Period Interval // period over which to sample
}

func (s *Sampling) String() string {
	if s == nil || s.N == 0 {
		return "none"
	} else if s.Period == 0 {
		return "all"
	}
	return fmt.Sprintf("%d/%d", s.N, s.Period)
}

// UnmarshalYAML decodes a sampling from a string of the form "n/iv".
// As special cases, "none" is allowed as an alias for 0/iv and "all" as an
// alias for 1/0.
func (s *Sampling) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var raw string
	if err := unmarshal(&raw); err != nil {
		return err
	}
	if raw == "none" {
		s.N = 0
		s.Period = 0
		return nil
	} else if raw == "all" {
		s.N = 1
		s.Period = 0
		return nil
	}
	ps := strings.SplitN(raw, "/", 2)
	if len(ps) != 2 {
		return fmt.Errorf("invalid sampling format: %q", s)
	}
	n, err := strconv.Atoi(ps[0])
	if err != nil || n < 1 {
		return fmt.Errorf("invalid sample count: %q", ps[0])
	}
	p, err := parseInterval(ps[1])
	if err != nil {
		return err
	}
	s.N = n
	s.Period = p
	return nil
}
