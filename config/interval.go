// Copyright (C) 2018 Michael J. Fromberger. All Rights Reserved.

package config

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// An Interval represents a time interval in seconds. An Interval can be parsed
// from a string in the format "d.dd unit" or "d unit", where unit is one of
//
//	s, sec, secs         -- seconds
//	h, hr, hour, hours   -- hours
//	d, day, days         -- days (defined as 24 hours)
//	w, wk, week, weeks   -- weeks (defined as 7 days)
//	m, mo, month, months -- months (defined as 365.25/12=30.4375 days)
//	y, yr, year, years   -- years (defined as 365.25 days)
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

func (s *Sampling) parseFrom(raw string) error {
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
	n, err := strconv.Atoi(strings.TrimSpace(ps[0]))
	if err != nil || n < 1 {
		return fmt.Errorf("invalid sample count: %q", ps[0])
	}
	p, err := parseInterval(strings.TrimSpace(ps[1]))
	if err != nil {
		return err
	}
	s.N = n
	s.Period = p
	return nil
}

// UnmarshalYAML decodes a sampling from a string of the form "n/iv".
// As special cases, "none" is allowed as an alias for 0/iv and "all" as an
// alias for 1/0.
func (s *Sampling) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var raw string
	if err := unmarshal(&raw); err != nil {
		return err
	}
	return s.parseFrom(raw)
}
