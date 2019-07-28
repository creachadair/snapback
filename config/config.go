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
	yaml "gopkg.in/yaml.v3"
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
	cacheTag   string
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
	// Check whether the in-memory cache is valid.
	ctag, err := c.Config.CacheTag()
	isValid := err == nil && ctag == c.cacheTag && c.cachedList != nil
	c.logf("Cache tag: %q, memory cache valid: %v", ctag, isValid)

	if isValid {
		return c.cachedList, nil // return cached data
	} else if c.ListCache == "" {
		return c.Config.List() // no list cache is defined
	}

	// At this point we have a cache file and no listing is loaded.
	type cacheFile struct {
		T string           `json:"cacheTag"`
		A tarsnap.Archives `json:"archiveList"`
	}
	var cf cacheFile
	data, err := ioutil.ReadFile(c.ListCache)
	if err == nil {
		err = json.Unmarshal(data, &cf)
		isValid = err == nil && cf.T == ctag

		// If we successfully loaded the cache and the tag is still valid, sort
		// the data to ensure correct order and update the cache.
		if isValid {
			c.logf("Loaded %d archives from list cache", len(cf.A))
			sort.Sort(cf.A)
			c.cacheTag = cf.T
			c.cachedList = cf.A
			return cf.A, nil
		}
		c.logf("List cache tag: %q, stored cache is invalid", cf.T)
	}

	// An error at this point means either we couldn't find the cache file, or
	// that its contents were invalid. In either case, re-fetch the real list.
	if !isValid {
		cf.A, err = c.Config.List()

		// Fetching failed; nothing more we can do here.
		if err != nil {
			return nil, err
		}
		c.logf("Loaded %d archives from tarsnap", len(cf.A))
	}

	// At this point we have a new listing that needs saved. If that fails we'll
	// log it but otherwise not complain.
	cf.T = ctag
	if bits, err := json.Marshal(&cf); err != nil {
		log.Printf("[warning] Encoding cache listing: %v", err)
	} else if err := os.MkdirAll(filepath.Dir(c.ListCache), 0700); err != nil {
		log.Printf("[warning] Creating list cache directory: %v", err)
	} else if err := atomicfile.WriteData(c.ListCache, bits, 0600); err != nil {
		log.Printf("[warning] Writing cache file: %v", err)
	}

	c.cacheTag = ctag
	c.cachedList = cf.A
	return cf.A, nil
}

// findPolicy returns the expiration rules for this backup. If it does not have
// any of its own, use the defaults. If there are no defaults, nothing expires.
func (c *Config) findPolicy(b *Backup) []*Policy {
	switch b.Policy {
	case "none":
		return b.Expiration
	case "":
		if len(b.Expiration) == 0 {
			return c.Expiration
		}
		return b.Expiration
	case "default":
		return append(c.Expiration, b.Expiration...)
	default:
		return append(c.Policy[b.Policy], b.Expiration...)
	}
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

	// Named expiration policy. If no policy is named, any explicit rules are
	// used and the default rules are ignored. Otherwise any explicit rules are
	// added to the selected policy, which is:
	//
	// If "default", the default rules are used.
	//
	// If "none", an empty policy is used.
	//
	// Any other name uses the rules from that policy.
	Policy string

	// Expand shell globs in included paths.
	GlobIncludes bool `json:"globIncludes" yaml:"glob-includes"`

	// The archive creation options for this backup.
	tarsnap.CreateOptions `yaml:",inline"`
}

// Parse decodes a *Config from the specified reader.
func Parse(r io.Reader) (*Config, error) {
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
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
