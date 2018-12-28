// Package config describes configuration settings for the snapback tool.
package config

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"bitbucket.org/creachadair/tarsnap"
	yaml "gopkg.in/yaml.v2"
)

var timeZero = time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)

// A Config contains settings for the snapback tool.
type Config struct {
	// An ordered list of backups to be created.
	Backup []*Backup

	// Default expiration policies.
	Expiration []*Policy

	// Configuration settings for the tarsnap tool.
	tarsnap.Config `yaml:",inline"`
}

// FindExpired returns a slice of archives that exist but are are eligible for
// removal under the expiration policies in effect for c.
func (c *Config) FindExpired(arch []tarsnap.Archive) []tarsnap.Archive {
	now := time.Now()
	sets := make(map[string][]tarsnap.Archive)
	for _, a := range arch {
		ext := filepath.Ext(a.Name)
		if _, err := time.Parse(".20060102-1504", ext); err != nil {
			continue // not the correct format
		}
		tag := strings.TrimSuffix(a.Name, ext)
		sets[tag] = append(sets[tag], a)
	}

	var match []tarsnap.Archive
	for _, b := range c.Backup {
		// Find the expiration rules for this backup. If it does not have any of
		// its own, use the defaults. If there are no defaults, nothing expires.
		exp := b.Expiration
		if len(exp) == 0 {
			exp = c.Expiration
		}
		if len(exp) == 0 {
			continue // nothing to do
		}

		// Now find all the archives belonging this backup which are affected by
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

		// Now apply the policy...
		for rule, batch := range rules {
			match = append(match, rule.apply(batch)...)
		}
	}
	return match
}

// A Policy specifies a policy for which backups to keep. When given a set of
// policies, the policy that best applies to an archive is the earliest,
// narrowest span of time between min and max before present, inclusive, that
// includes the creation time of the archive. A max value of 0 means +∞.
//
// For example suppose X is an archive created 7 days before present, and we
// have policies (min=0, max=0)
type Policy struct {
	// The rest of this policy applies to backups created in the inclusive
	// interval between min and max before present. Max == 0 means +∞.
	Min Interval `yaml:"after"`
	Max Interval `yaml:"until"`

	Latest int       // keep this many of the most-recent matching archives
	Sample *Sampling // if set, samples/period to retain
}

// apply returns all the input archives that are expired by p.
func (p *Policy) apply(batch []tarsnap.Archive) []tarsnap.Archive {
	if p.Latest >= len(batch) {
		return nil // everything is too new to discard
	} else if p.Latest > 0 {
		batch = batch[:len(batch)-p.Latest]
	}
	if p.Sample == nil {
		return batch // no samples, discard everything else in range
	}

	// The width of the scaled sampling interval, where s/p = 1/ival.
	ival := p.Sample.Period / Interval(p.Sample.N)

	// Find smallest interval beginning at or before the last entry in the
	// policy window. We keep the last (most recent) entry in each interval.
	last := durationInterval(batch[len(batch)-1].Created.Sub(timeZero))
	base := ival * (last / ival)

	var drop []tarsnap.Archive
	for i := len(batch) - 2; i >= 0; i-- {
		age := durationInterval(batch[i].Created.Sub(timeZero))
		if age >= base {
			drop = append(drop, batch[i])
		} else {
			base -= ival
		}
	}
	return drop
}

// A Backup describes a collection of files to be backed up as a unit together.
type Backup struct {
	// The name defines the base name of the archive. A timestamp will be
	// appended to this name to obtain the complete name.
	Name string

	// Expiration policies.
	Expiration []*Policy

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
	seen := make(map[string]bool)
	for _, b := range cfg.Backup {
		if seen[b.Name] {
			return nil, fmt.Errorf("repeated backup name %q", b.Name)
		}
		seen[b.Name] = true
		sortExp(b.Expiration)
		expand(&b.WorkDir)
	}
	sortExp(cfg.Expiration)
	expand(&cfg.Keyfile)
	expand(&cfg.WorkDir)
	return &cfg, nil
}

func expand(s *string) { *s = os.ExpandEnv(*s) }

func sortExp(es []*Policy) {
	// Treat max == 0 as having no effective upper bound.
	for _, e := range es {
		if e.Max == 0 {
			e.Max = 1<<63 - 1
		}
	}

	// Order rules by the width of their interval, with earlier intervals first.
	sort.Slice(es, func(i, j int) bool {
		u := es[i].Max - es[i].Min
		v := es[j].Max - es[j].Min
		return u < v
	})
}

// An Interval represents a time interval in seconds.
type Interval int64

func (iv Interval) timeDuration() time.Duration {
	return time.Duration(iv) * time.Second
}

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

var dx = regexp.MustCompile(`^(?i)(\d+|\d*\.\d+)([shdwmy])$`)

func parseInterval(s string) (Interval, error) {
	m := dx.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("invalid interval %q", s)
	}
	f, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, fmt.Errorf("invalid number: %v", err)
	}
	switch m[2] {
	case "s", "S":
		f *= float64(Second)
	case "h", "H":
		f *= float64(Hour)
	case "d", "D":
		f *= float64(Day)
	case "w", "W":
		f *= float64(Week)
	case "m", "M":
		f *= float64(Month)
	case "y", "Y":
		f *= float64(Year)
	default:
		panic("unhandled key: " + m[2])
	}
	return Interval(f), nil
}

// A Sampling denotes a rule for how frequently to sample a sequence.
type Sampling struct {
	N      int      // number of samples per period
	Period Interval // period over which to sample
}

func (s Sampling) String() string { return fmt.Sprintf("%d/%d", s.N, s.Period) }

// UnmarshalYAML decodes a sampling from a string of the form "n/iv".
func (s *Sampling) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var raw string
	if err := unmarshal(&raw); err != nil {
		return err
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
