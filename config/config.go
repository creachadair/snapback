// Copyright (C) 2018 Michael J. Fromberger. All Rights Reserved.

// Package config describes configuration settings for the snapback tool.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/creachadair/atomicfile"
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

	// Cache archive listings in this file.
	ListCache  string `json:"listCache" yaml:"list-cache"`
	cachedList tarsnap.Archives

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

// FindSet returns the backup matching name, or nil if none matches.
func (c *Config) FindSet(name string) *Backup {
	for _, b := range c.Backup {
		if b.Name == name {
			return b
		}
	}
	return nil
}

// List returns a list of the known archives, using a cached copy if one is
// available and updating the cache if necessary. The resulting slice is
// ordered nondecreasing by creation time and by name.
func (c *Config) List() (tarsnap.Archives, error) {
	if c.cachedList != nil {
		return c.cachedList, nil // return cached data
	} else if c.ListCache == "" {
		return c.Config.List() // no cache is defined
	}

	// At this point we have a cache file and no listing is loaded.
	var cached tarsnap.Archives
	data, err := ioutil.ReadFile(c.ListCache)
	if err == nil {
		err = json.Unmarshal(data, &cached)

		// Successful cache load. Since we loaded these from outside, verify that
		// the order is correct.
		if err == nil {
			sort.Sort(cached)
			c.cachedList = cached
			return cached, nil
		}
	}

	// An error at this point means either we couldn't find the cache file, or
	// that its contents were invalid. In either case, re-fetch the real list.
	if err != nil {
		cached, err = c.Config.List()

		// Fetching failed; nothing more we can do here.
		if err != nil {
			return nil, err
		}
	}

	// At this point we have a new listing that needs saved. If that fails we'll
	// log it but otherwise not complain.
	if bits, err := json.Marshal(cached); err != nil {
		log.Printf("[warning] Encoding cache listing: %v", err)
	} else if err := os.MkdirAll(filepath.Dir(c.ListCache), 0700); err != nil {
		log.Printf("[warning] Creating list cache directory: %v", err)
	} else if err := atomicfile.WriteData(c.ListCache, bits, 0600); err != nil {
		log.Printf("[warning] Writing cache file: %v", err)
	}

	c.cachedList = cached
	return cached, nil
}

// InvalidateListCache marks the cached list data as invalid, forcing an update
// the next time a listing is required. The caller is responsible to call this
// method when needed.
func (c *Config) InvalidateListCache() {
	c.cachedList = nil
	if c.ListCache != "" {
		os.Truncate(c.ListCache, 0)
	}
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
	expand(&cfg.ListCache)

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
	base := b.WorkDir
	if base == "" {
		base = wd
	}
	vpath := func(inc string) string {
		if filepath.IsAbs(inc) {
			return inc
		}
		return filepath.Join(base, inc)
	}

	var paths []string
	for _, inc := range b.Include {
		path := vpath(inc)
		matches, _ := filepath.Glob(path)
		for _, match := range matches {
			if t := strings.TrimPrefix(match, base+"/"); t != match {
				match = t
			}
			paths = append(paths, match)
		}
	}
	b.Include = paths
}
